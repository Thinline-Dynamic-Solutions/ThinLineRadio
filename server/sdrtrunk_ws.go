// Copyright (C) 2026 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const sdrtrunkSocketPath = "/api/sdrtrunk"

var sdrtrunkUpgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

type sdrtrunkCommandResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type sdrtrunkSession struct {
	conn     *websocket.Conn
	apiKeyId uint64
	writeMu  sync.Mutex
	pending  map[string]chan sdrtrunkCommandResult
	pendMu   sync.Mutex
}

func (session *sdrtrunkSession) write(payload any) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	_ = session.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return session.conn.WriteJSON(payload)
}

func (session *sdrtrunkSession) deliver(id string, result sdrtrunkCommandResult) {
	session.pendMu.Lock()
	ch := session.pending[id]
	delete(session.pending, id)
	session.pendMu.Unlock()
	if ch != nil {
		ch <- result
	}
}

type sdrtrunkBrowser struct {
	conn     *websocket.Conn
	apiKeyId uint64
	writeMu  sync.Mutex
}

func (browser *sdrtrunkBrowser) write(payload any) {
	browser.writeMu.Lock()
	defer browser.writeMu.Unlock()
	_ = browser.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = browser.conn.WriteJSON(payload)
}

type sdrtrunkHub struct {
	mu       sync.Mutex
	sessions map[uint64]*sdrtrunkSession
	browsers map[uint64]map[*sdrtrunkBrowser]struct{}
}

func newSdrtrunkHub() *sdrtrunkHub {
	return &sdrtrunkHub{
		sessions: map[uint64]*sdrtrunkSession{},
		browsers: map[uint64]map[*sdrtrunkBrowser]struct{}{},
	}
}

func (controller *Controller) sdrtrunkHub() *sdrtrunkHub {
	controller.sdrtrunkHubOnce.Do(func() {
		controller.sdrtrunkSessions = newSdrtrunkHub()
	})
	return controller.sdrtrunkSessions
}

func (hub *sdrtrunkHub) register(apiKeyId uint64, session *sdrtrunkSession) {
	hub.mu.Lock()
	previous := hub.sessions[apiKeyId]
	hub.sessions[apiKeyId] = session
	hub.mu.Unlock()
	if previous != nil && previous != session {
		_ = previous.conn.Close()
	}
}

func (hub *sdrtrunkHub) unregister(apiKeyId uint64, session *sdrtrunkSession) {
	hub.mu.Lock()
	if hub.sessions[apiKeyId] == session {
		delete(hub.sessions, apiKeyId)
	}
	hub.mu.Unlock()
}

func (hub *sdrtrunkHub) connected(apiKeyId uint64) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	_, ok := hub.sessions[apiKeyId]
	return ok
}

func (hub *sdrtrunkHub) forward(apiKeyId uint64, payload any) {
	hub.mu.Lock()
	session := hub.sessions[apiKeyId]
	hub.mu.Unlock()
	if session != nil {
		_ = session.write(payload)
	}
}

func (hub *sdrtrunkHub) relay(apiKeyId uint64, payload []byte) {
	hub.mu.Lock()
	watchers := hub.browsers[apiKeyId]
	browsers := make([]*sdrtrunkBrowser, 0, len(watchers))
	for browser := range watchers {
		browsers = append(browsers, browser)
	}
	hub.mu.Unlock()

	for _, browser := range browsers {
		browser.writeMu.Lock()
		_ = browser.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = browser.conn.WriteMessage(websocket.TextMessage, payload)
		browser.writeMu.Unlock()
	}
}

func (hub *sdrtrunkHub) addBrowser(browser *sdrtrunkBrowser) int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	set := hub.browsers[browser.apiKeyId]
	if set == nil {
		set = map[*sdrtrunkBrowser]struct{}{}
		hub.browsers[browser.apiKeyId] = set
	}
	set[browser] = struct{}{}
	return len(set)
}

func (hub *sdrtrunkHub) removeBrowser(browser *sdrtrunkBrowser) int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	set := hub.browsers[browser.apiKeyId]
	if set == nil {
		return 0
	}
	delete(set, browser)
	return len(set)
}

func (hub *sdrtrunkHub) command(apiKeyId uint64, fields map[string]any) (sdrtrunkCommandResult, error) {
	hub.mu.Lock()
	session := hub.sessions[apiKeyId]
	hub.mu.Unlock()
	if session == nil {
		return sdrtrunkCommandResult{}, fmt.Errorf("SDRTrunk is not connected")
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	resultCh := make(chan sdrtrunkCommandResult, 1)
	session.pendMu.Lock()
	if session.pending == nil {
		session.pending = map[string]chan sdrtrunkCommandResult{}
	}
	session.pending[id] = resultCh
	session.pendMu.Unlock()

	payload := map[string]any{
		"type": "command",
		"id":   id,
	}
	for key, value := range fields {
		payload[key] = value
	}
	if err := session.write(payload); err != nil {
		session.deliver(id, sdrtrunkCommandResult{OK: false, Error: "write failed"})
		return sdrtrunkCommandResult{}, err
	}

	select {
	case result := <-resultCh:
		return result, nil
	case <-time.After(12 * time.Second):
		session.deliver(id, sdrtrunkCommandResult{})
		return sdrtrunkCommandResult{}, fmt.Errorf("SDRTrunk did not answer")
	}
}

func (api *Api) SdrtrunkSocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := sdrtrunkUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, helloBytes, err := conn.ReadMessage()
	if err != nil {
		return
	}

	var hello struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if json.Unmarshal(helloBytes, &hello) != nil || hello.Type != "hello" {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "expected hello"})
		return
	}

	apikey, ok := api.Controller.Apikeys.GetApikey(hello.Key)
	if !ok || !apikey.IsSite() {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "use a site control API key"})
		return
	}

	session := &sdrtrunkSession{conn: conn, apiKeyId: apikey.Id, pending: map[string]chan sdrtrunkCommandResult{}}
	hub := api.Controller.sdrtrunkHub()
	hub.register(apikey.Id, session)
	defer func() {
		api.Controller.core().sessionClosed(apikey.Id)
		hub.unregister(apikey.Id, session)
	}()

	if err := session.write(map[string]any{"type": "welcome"}); err != nil {
		return
	}
	api.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("sdrtrunk websocket connected for '%s'", apikey.Ident))

	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				session.writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				session.writeMu.Unlock()
				if err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	conn.SetReadLimit(8 << 20)
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})

	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var envelope struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if json.Unmarshal(payload, &envelope) != nil {
			continue
		}

		switch envelope.Type {
		case "result":
			session.deliver(envelope.ID, sdrtrunkCommandResult{OK: envelope.OK, Error: envelope.Error})
		case "core.hello", "core.bye", "call.started", "call.ready", "call.cancelled":
			api.Controller.core().onMessage(apikey.Id, envelope.Type, payload)
		case "health":
			payload = sdrtrunkForceReportKey(payload, hello.Key, "health")
			if code, message := api.ingestSdrtrunkBody(payload); code != http.StatusOK {
				_ = session.write(map[string]any{"type": "error", "error": message})
			}
		case "upload":
			if code, message := api.ingestSdrtrunkBody(payload); code != http.StatusOK {
				_ = session.write(map[string]any{"type": "error", "error": message})
			}
		case "state", "spectrum", "playlist", "aliases", "desktop", "audio", "settings":
			hub.relay(apikey.Id, payload)
		}
	}
}

func sdrtrunkForceReportKey(payload []byte, key, role string) []byte {
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		return payload
	}
	body["key"] = key
	body["role"] = role
	rewritten, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return rewritten
}

func (api *Api) ingestSdrtrunkBody(body []byte) (int, string) {
	var report sdrtrunkHealthRequest
	if err := json.Unmarshal(body, &report); err != nil {
		return http.StatusBadRequest, "Invalid health report"
	}

	report.Key = strings.TrimSpace(report.Key)
	report.Role = strings.TrimSpace(report.Role)
	if report.Key == "" {
		return http.StatusBadRequest, "Health report requires an API key"
	}

	apikey, ok := api.Controller.Apikeys.GetApikey(report.Key)
	if !ok {
		return http.StatusUnauthorized, "Invalid API key."
	}
	if report.Role == "health" && !apikey.IsSite() {
		return http.StatusUnauthorized, "Site control requires a site control API key."
	}
	if report.Role == "upload" && apikey.IsSite() {
		return http.StatusUnauthorized, "Upload check-in requires an upload API key."
	}
	if report.Role != "health" && (report.System == 0 || !apikeyAllowsSystem(apikey, report.System)) {
		return http.StatusUnauthorized, "Invalid API key for system."
	}

	api.Controller.applySdrtrunkHealth(apikey, report, body)
	return http.StatusOK, "ok"
}

func (admin *Admin) SdrtrunkLiveHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := sdrtrunkUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, helloBytes, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello struct {
		Token    string `json:"token"`
		ApiKeyId uint64 `json:"apiKeyId"`
	}
	if json.Unmarshal(helloBytes, &hello) != nil || !admin.ValidateToken(hello.Token) || hello.ApiKeyId == 0 {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "unauthorized"})
		return
	}

	browser := &sdrtrunkBrowser{conn: conn, apiKeyId: hello.ApiKeyId}
	hub := admin.Controller.sdrtrunkHub()
	if hub.addBrowser(browser) == 1 {
		_, _ = hub.command(hello.ApiKeyId, map[string]any{"command": "watch", "enabled": true})
	}
	defer func() {
		if hub.removeBrowser(browser) == 0 {
			_, _ = hub.command(hello.ApiKeyId, map[string]any{"command": "desktop", "enabled": false})
			_, _ = hub.command(hello.ApiKeyId, map[string]any{"command": "spectrum", "enabled": false})
			_, _ = hub.command(hello.ApiKeyId, map[string]any{"command": "watch", "enabled": false})
		}
	}()
	_ = conn.WriteJSON(map[string]any{"type": "welcome", "connected": hub.connected(hello.ApiKeyId)})

	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var request map[string]any
		if json.Unmarshal(payload, &request) != nil {
			continue
		}
		if request["type"] == "input" {
			request["type"] = "command"
			request["command"] = "desktopInput"
			hub.forward(hello.ApiKeyId, request)
			continue
		}
		if request["type"] != "command" {
			continue
		}
		delete(request, "type")
		go func(fields map[string]any) {
			result, err := hub.command(hello.ApiKeyId, fields)
			if err != nil {
				browser.write(map[string]any{"type": "result", "ok": false, "error": err.Error(), "command": fields["command"]})
				return
			}
			browser.write(map[string]any{"type": "result", "ok": result.OK, "error": result.Error, "command": fields["command"]})
		}(request)
	}
}

func (admin *Admin) SdrtrunkCommandHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.ValidateToken(admin.GetAuthorization(r)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var request struct {
		ApiKeyId uint64 `json:"apiKeyId"`
		Command  string `json:"command"`
		Name     string `json:"name"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid command\n"))
		return
	}
	if request.Command != "report" && request.Command != "setStream" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Unknown command\n"))
		return
	}

	result, err := admin.Controller.sdrtrunkHub().command(request.ApiKeyId, map[string]any{
		"command": request.Command,
		"name":    request.Name,
		"enabled": request.Enabled,
	})
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(err.Error() + "\n"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": result.OK, "error": result.Error})
}
