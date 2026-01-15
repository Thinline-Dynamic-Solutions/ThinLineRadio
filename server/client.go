// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	User       *User
	AuthCount  int
	Controller *Controller
	Conn       *websocket.Conn
	Send       chan *Message
	IsAdmin    bool // Set to true when authenticated with admin token
	PinExpired bool // Set to true when user's PIN is expired
	Systems    []System
	GroupsData []Group
	GroupsMap  GroupsMap
	TagsData   []Tag
	TagsMap    TagsMap
	Livefeed   *Livefeed
	SystemsMap SystemsMap
	request    *http.Request
}

func (client *Client) Init(controller *Controller, request *http.Request, conn *websocket.Conn) error {
	const (
		pongWait   = 300 * time.Second // Increased from 60s to 5 minutes for long imports
		pingPeriod = 30 * time.Second  // Ping every 30 seconds to keep proxy/load balancer connections alive (common 2-minute timeout)
		writeWait  = 60 * time.Second  // Increased from 10s to 1 minute for long imports
	)

	if conn == nil {
		return errors.New("client.init: no websocket connection")
	}

	if controller.Clients.Count() >= int(controller.Options.MaxClients) {
		conn.Close()
		return nil
	}

	client.User = nil
	client.PinExpired = false
	client.Controller = controller
	client.Conn = conn
	client.Livefeed = NewLivefeed()
	client.Send = make(chan *Message, 8192)
	client.request = request

	go func() {
		defer func() {
			// Save state for potential reconnection before unregistering
			if client.User != nil && client.Controller != nil && client.Controller.ReconnectionMgr != nil {
				client.Controller.ReconnectionMgr.SaveDisconnectedState(client)
			}

			controller.Unregister <- client

			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("listener disconnected from ip %s", client.GetRemoteAddr()))

			client.Conn.Close()
		}()

		if err := client.Conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			return
		}

		client.Conn.SetPongHandler(func(string) error {
			if err := client.Conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
				return err
			}

			return nil
		})

		for {
			_, b, err := client.Conn.ReadMessage()
			if err != nil {
				// Log the error before closing
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("websocket read error from ip %s: %v", client.GetRemoteAddr(), err))
				}
				return
			}

			message := &Message{}
			if err = message.FromJson(b); err != nil {
				controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("client.message.fromjson error from ip %s: %v", client.GetRemoteAddr(), err))
				log.Println(fmt.Errorf("client.message.fromjson: %v", err))
				continue
			}

			if err = client.Controller.ProcessMessage(client, message); err != nil {
				controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("client.processmessage error from ip %s: %v", client.GetRemoteAddr(), err))
				log.Println(fmt.Errorf("client.processmessage: %v", err))
				continue
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(pingPeriod)

		timer := time.AfterFunc(pongWait, func() {
			client.Conn.Close()
		})

		defer func() {
			client.Send = nil

			ticker.Stop()

			if timer != nil {
				timer.Stop()
			}

			client.Conn.Close()
		}()

		for {
			select {
			case message, ok := <-client.Send:
				if !ok {
					return
				}

				if message.Command == MessageCommandConfig {
					if timer != nil {
						timer.Stop()
						timer = nil

						controller.Register <- client

						controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("new listener from ip %s", client.GetRemoteAddr()))
					}
				}

				if b, err := message.ToJson(); err != nil {
					controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("client.message.tojson error for ip %s: %v", client.GetRemoteAddr(), err))
					log.Println(fmt.Errorf("client.message.tojson: %v", err))

				} else {
					if err = client.Conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
						controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("websocket set write deadline error for ip %s: %v", client.GetRemoteAddr(), err))
						return
					}

					if err = client.Conn.WriteMessage(websocket.TextMessage, b); err != nil {
						controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("websocket write error for ip %s: %v", client.GetRemoteAddr(), err))
						return
					}
				}

			case <-ticker.C:
				// Check if connection is still open before trying to ping
				if client.Conn == nil {
					return
				}
				
				if err := client.Conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
					controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("websocket set write deadline error for ping to ip %s: %v", client.GetRemoteAddr(), err))
					return
				}

				if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					// Don't log "close sent" or "use of closed network connection" errors as warnings - they're expected when client disconnects
					errStr := err.Error()
					if !strings.Contains(errStr, "close sent") && !strings.Contains(errStr, "use of closed network connection") {
						controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("websocket ping error for ip %s: %v", client.GetRemoteAddr(), err))
					}
					return
				}
			}
		}
	}()

	return nil
}

func (client *Client) GetRemoteAddr() string {
	return GetRemoteAddr(client.request)
}

func (client *Client) SendConfig(groups *Groups, options *Options, systems *Systems, tags *Tags) {
	client.SystemsMap = systems.GetScopedSystems(client, groups, tags, options.SortTalkgroups)
	client.GroupsData = groups.GetGroupsData(&client.SystemsMap)
	client.GroupsMap = groups.GetGroupsMap(&client.SystemsMap)
	client.TagsData = tags.GetTagsData(&client.SystemsMap)
	client.TagsMap = tags.GetTagsMap(&client.SystemsMap)

	// Get the user's group pricing options if user is authenticated and has a group
	var pricingOptions []PricingOption
	if client.User != nil {
		if client.User.UserGroupId > 0 {
			if client.Controller != nil {
				userGroup := client.Controller.UserGroups.Get(client.User.UserGroupId)
				if userGroup != nil {
					if userGroup.BillingEnabled {
						pricingOptions = userGroup.GetPricingOptions()
					}
				}
			}
		}
	}

	var payload = map[string]any{
		"alerts":      Alerts,
		"branding":    options.Branding,
		"dimmerDelay": options.DimmerDelay,
		"email":       options.Email,
		"groups":      client.GroupsMap,
		"groupsData":  client.GroupsData,
		"keypadBeeps": GetKeypadBeeps(options),
		"options": map[string]any{
			"userRegistrationEnabled": options.UserRegistrationEnabled,
			"stripePaywallEnabled":    options.StripePaywallEnabled,
			"emailServiceEnabled":     options.EmailServiceEnabled,
			"emailServiceApiKey":      options.EmailServiceApiKey,
			"emailServiceDomain":      options.EmailServiceDomain,
			"emailServiceTemplateId":  options.EmailServiceTemplateId,
			"stripePublishableKey":    options.StripePublishableKey,
			"pricingOptions":          pricingOptions,
			"baseUrl":                 options.BaseUrl,
			"transcriptionEnabled":    options.TranscriptionConfig.Enabled,
		},
		"playbackGoesLive":   options.PlaybackGoesLive,
		"showListenersCount": options.ShowListenersCount,
		"systems":            client.SystemsMap,
		"tags":               client.TagsMap,
		"tagsData":           client.TagsData,
		"time12hFormat":      options.Time12hFormat,
	}

	// Include user settings if user is authenticated
	if client.User != nil && client.User.Settings != "" {
		var userSettings map[string]interface{}
		if err := json.Unmarshal([]byte(client.User.Settings), &userSettings); err == nil {
			payload["userSettings"] = userSettings
		}
	}

	// Non-blocking send to prevent deadlock
	select {
	case client.Send <- &Message{Command: MessageCommandConfig, Payload: payload}:
		// Message sent successfully
	default:
		// Channel full, skip to avoid blocking
	}
}

func (client *Client) SendListenersCount(count int) {
	// Non-blocking send to prevent deadlock
	select {
	case client.Send <- &Message{
		Command: MessagecommandListenersCount,
		Payload: count,
	}:
		// Message sent successfully
	default:
		// Channel full, skip to avoid blocking
	}
}

type Clients struct {
	Map   map[*Client]bool
	mutex sync.Mutex
}

func NewClients() *Clients {
	return &Clients{
		Map:   map[*Client]bool{},
		mutex: sync.Mutex{},
	}
}

func (clients *Clients) Add(client *Client) {
	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	clients.Map[client] = true
}

func (clients *Clients) Count() int {
	return len(clients.Map)
}

func (clients *Clients) EmitCall(controller *Controller, call *Call) {
	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	restricted := controller.requiresUserAuth()
	msg := &Message{Command: MessageCommandCall, Payload: call}

	for c := range clients.Map {
		if !c.Livefeed.IsEnabled(call) {
			continue
		}

		if restricted {
			// Check user access
			if c.User == nil || !controller.userHasAccess(c.User, call) {
				continue
			}
		}

		if controller.Delayer.CanDelayForClient(call, c) {
			controller.Delayer.DelayForClient(call, c)
		} else {
			// Non-blocking send to prevent deadlock
			select {
			case c.Send <- msg:
				// Message sent successfully
			default:
				// Channel full, skip this client to avoid blocking
				// Client will catch up on next call or disconnect
			}
		}
	}

	// Buffer call for disconnected clients within reconnection grace period
	if controller.ReconnectionMgr != nil {
		controller.ReconnectionMgr.BufferCallForDisconnected(call)
	}
}

func (clients *Clients) EmitConfig(controller *Controller) {
	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	count := len(clients.Map)
	restricted := controller.requiresUserAuth()
	showListenersCount := controller.Options.ShowListenersCount

	for c := range clients.Map {
		if restricted {
			if c.User == nil {
				msg := &Message{Command: MessageCommandPin}
				// Non-blocking send to prevent deadlock
				select {
				case c.Send <- msg:
				default:
					// Skip if channel full
				}
			} else {
				c.SendConfig(controller.Groups, controller.Options, controller.Systems, controller.Tags)
			}
		} else {
			c.SendConfig(controller.Groups, controller.Options, controller.Systems, controller.Tags)
		}

		if showListenersCount {
			c.SendListenersCount(count)
		}
	}
}

func (clients *Clients) EmitListenersCount() {
	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	count := len(clients.Map)

	for c := range clients.Map {
		c.SendListenersCount(count)
	}
}

func (clients *Clients) Remove(client *Client) {
	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	delete(clients.Map, client)
}


func (clients *Clients) UserConnectionCount(user *User) uint {
	if user == nil {
		return 0
	}

	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	var count uint
	var toRemove []*Client
	
	for c := range clients.Map {
		if c.User == user {
			// Check if connection is still alive
			// If Send channel is nil, the client has disconnected
			if c.Send == nil {
				toRemove = append(toRemove, c)
				continue
			}
			
			// Check if websocket connection is still open
			if c.Conn == nil {
				toRemove = append(toRemove, c)
				continue
			}
			
			// Connection appears to be alive
			count++
		}
	}
	
	// Remove dead connections immediately
	for _, c := range toRemove {
		delete(clients.Map, c)
		// Try to trigger unregister for proper cleanup, but don't block
		if c.Controller != nil {
			select {
			case c.Controller.Unregister <- c:
			default:
			}
		}
	}

	return count
}

// RefreshConfigForGroup refreshes configuration for all active clients belonging to users in the specified group
func (clients *Clients) RefreshConfigForGroup(controller *Controller, groupId uint64) {
	clients.mutex.Lock()
	defer clients.mutex.Unlock()

	restricted := controller.requiresUserAuth()
	showListenersCount := controller.Options.ShowListenersCount
	count := len(clients.Map)

	for c := range clients.Map {
		if c.User != nil && c.User.UserGroupId == groupId {
			if restricted {
				if c.User == nil {
					// Non-blocking send to prevent deadlock
					select {
					case c.Send <- &Message{Command: MessageCommandPin}:
					default:
						// Skip if channel full
					}
				} else {
					c.SendConfig(controller.Groups, controller.Options, controller.Systems, controller.Tags)
				}
			} else {
				c.SendConfig(controller.Groups, controller.Options, controller.Systems, controller.Tags)
			}

			if showListenersCount {
				c.SendListenersCount(count)
			}
		}
	}
}
