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
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

type Downstream struct {
	Id         uint64
	Apikey     string
	Disabled   bool
	Name       string
	Order      uint
	Systems    any
	Url        string
	controller *Controller
}

func NewDownstream(controller *Controller) *Downstream {
	return &Downstream{
		controller: controller,
	}
}

func (downstream *Downstream) FromMap(m map[string]any) *Downstream {
	// Handle both "id" and "_id" fields for backward compatibility
	if v, ok := m["id"].(float64); ok {
		downstream.Id = uint64(v)
	} else if v, ok := m["_id"].(float64); ok {
		downstream.Id = uint64(v)
	}

	switch v := m["apikey"].(type) {
	case string:
		downstream.Apikey = v
	}

	switch v := m["disabled"].(type) {
	case bool:
		downstream.Disabled = v
	}

	switch v := m["name"].(type) {
	case string:
		downstream.Name = v
	}

	switch v := m["order"].(type) {
	case float64:
		downstream.Order = uint(v)
	}

	downstream.Systems = m["systems"]

	switch v := m["url"].(type) {
	case string:
		downstream.Url = v
	}

	return downstream
}

func (downstream *Downstream) HasAccess(call *Call) bool {
	if downstream.Disabled {
		return false
	}

	switch v := downstream.Systems.(type) {
	case []any:
		for _, f := range v {
			switch v := f.(type) {
			case map[string]any:
				switch id := v["id"].(type) {
				case float64:
					systemRef := uint(id)

					var callSystemRef uint
					if call.System != nil {
						callSystemRef = call.System.SystemRef
					} else if call.Meta.SystemRef > 0 {
						callSystemRef = call.Meta.SystemRef
					} else if call.SystemId > 0 {
						callSystemRef = call.SystemId
					}

					if callSystemRef > 0 && callSystemRef == systemRef {
						switch tg := v["talkgroups"].(type) {
						case string:
							if tg == "*" {
								return true
							}
						case []any:
							var callTalkgroupRef uint
							if call.Talkgroup != nil {
								callTalkgroupRef = call.Talkgroup.TalkgroupRef
							} else if call.Meta.TalkgroupRef > 0 {
								callTalkgroupRef = call.Meta.TalkgroupRef
							} else if call.TalkgroupId > 0 {
								callTalkgroupRef = call.TalkgroupId
							}

							for _, f := range tg {
								switch tg := f.(type) {
								case float64:
									if callTalkgroupRef > 0 && uint(tg) == callTalkgroupRef {
										return true
									}
								}
							}
						}
					}
				}
			}
		}

	case string:
		if v == "*" {
			return true
		}

	}

	return false
}

func (downstream *Downstream) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"id":       downstream.Id,
		"apikey":   downstream.Apikey,
		"disabled": downstream.Disabled,
		"name":     downstream.Name,
		"systems":  downstream.Systems,
		"url":      downstream.Url,
	}

	if downstream.Order > 0 {
		m["order"] = downstream.Order
	}

	return json.Marshal(m)
}

func (downstream *Downstream) Send(call *Call) error {
	var buf = bytes.Buffer{}

	formatError := func(err error) error {
		return fmt.Errorf("downstream.send: %s", err.Error())
	}

	if downstream.controller == nil {
		return formatError(errors.New("no controller available"))
	}

	if downstream.Disabled {
		return nil
	}

	mw := multipart.NewWriter(&buf)

	if w, err := mw.CreateFormFile("audio", call.AudioFilename); err == nil {
		if _, err = w.Write(call.Audio); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// Use v6 field names for universal compatibility (v7 parser accepts both)
	if w, err := mw.CreateFormField("audioName"); err == nil {
		if _, err = w.Write([]byte(call.AudioFilename)); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	if w, err := mw.CreateFormField("audioType"); err == nil {
		if _, err = w.Write([]byte(call.AudioMime)); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// pre v7 comptability
	if w, err := mw.CreateFormField("dateTime"); err == nil {
		if _, err = w.Write([]byte(call.Timestamp.Format(time.RFC3339))); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// Only send frequencies if there are valid ones (matching v6 behavior)
	// Build frequency objects in v6 format to prevent empty objects {}
	validFreqs := []map[string]any{}
	for _, freq := range call.Frequencies {
		// Only include if we have a valid frequency value
		if freq.Frequency > 0 {
			freqMap := map[string]any{
				"errorCount": freq.Errors,
				"freq":       freq.Frequency,
				"pos":        freq.Offset,
				"spikeCount": freq.Spikes,
			}
			validFreqs = append(validFreqs, freqMap)
		}
	}

	// Only send if we have valid frequencies (let v6 store as nil if not sent)
	if len(validFreqs) > 0 {
		if w, err := mw.CreateFormField("frequencies"); err == nil {
			if b, err := json.Marshal(validFreqs); err == nil {
				if _, err = w.Write(b); err != nil {
					return formatError(err)
				}
			} else {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	if call.Frequency > 0 {
		if w, err := mw.CreateFormField("frequency"); err == nil {
			if _, err = w.Write([]byte(fmt.Sprintf("%d", call.Frequency))); err != nil {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	if w, err := mw.CreateFormField("key"); err == nil {
		if _, err = w.Write([]byte(downstream.Apikey)); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// Tag the call as forwarded so the receiving TLR server does not re-forward it,
	// preventing circular downstream loops between servers.
	if w, err := mw.CreateFormField("tlrForwarded"); err == nil {
		if _, err = w.Write([]byte("1")); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// Only send patches if there are any (matching v6 behavior)
	if len(call.Patches) > 0 {
		if w, err := mw.CreateFormField("patches"); err == nil {
			if b, err := json.Marshal(call.Patches); err == nil {
				if _, err = w.Write(b); err != nil {
					return formatError(err)
				}
			} else {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	if w, err := mw.CreateFormField("system"); err == nil {
		if _, err = w.Write([]byte(fmt.Sprintf("%v", call.System.SystemRef))); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// Only send systemLabel if not empty (matching v6 switch behavior)
	if call.System.Label != "" {
		if w, err := mw.CreateFormField("systemLabel"); err == nil {
			if _, err = w.Write([]byte(call.System.Label)); err != nil {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	if w, err := mw.CreateFormField("talkgroup"); err == nil {
		if _, err = w.Write([]byte(fmt.Sprintf("%v", call.Talkgroup.TalkgroupRef))); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// v6 compatibility - only send talkgroupGroup if not empty (matching v6 switch behavior)
	var labels = []string{}
	for _, id := range call.Talkgroup.GroupIds {
		if group, ok := downstream.controller.Groups.GetGroupById(id); ok {
			labels = append(labels, group.Label)
		}
	}
	talkgroupGroup := strings.Join(labels, ",")
	if talkgroupGroup != "" {
		if w, err := mw.CreateFormField("talkgroupGroup"); err == nil {
			if _, err = w.Write([]byte(talkgroupGroup)); err != nil {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	// Only send talkgroupLabel if not empty (matching v6 switch behavior)
	if call.Talkgroup.Label != "" {
		if w, err := mw.CreateFormField("talkgroupLabel"); err == nil {
			if _, err = w.Write([]byte(call.Talkgroup.Label)); err != nil {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	// Only send talkgroupName if not empty (matching v6 switch behavior)
	if call.Talkgroup.Name != "" {
		if w, err := mw.CreateFormField("talkgroupName"); err == nil {
			if _, err = w.Write([]byte(call.Talkgroup.Name)); err != nil {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	// Only send talkgroupTag if tag exists (matching v6 switch behavior)
	if tag, ok := downstream.controller.Tags.GetTagById(call.Talkgroup.TagId); ok {
		if tag.Label != "" {
			if w, err := mw.CreateFormField("talkgroupTag"); err == nil {
				if _, err = w.Write([]byte(tag.Label)); err != nil {
					return formatError(err)
				}
			} else {
				return formatError(err)
			}
		}
	}

	if w, err := mw.CreateFormField("timestamp"); err == nil {
		if _, err = w.Write([]byte(fmt.Sprintf("%d", call.Timestamp.UnixMilli()))); err != nil {
			return formatError(err)
		}
	} else {
		return formatError(err)
	}

	// DON'T send units field - v6 doesn't understand it
	// Instead, only send source/sources which v6 expects

	// CRITICAL: Only send source/sources if we have units, AND at least one has UnitRef > 0
	// This ensures v6 stores them as nil when there's no valid unit data, matching native v6 behavior
	// The mobile app rejects calls with source:0 but accepts source:null
	hasValidUnits := false
	for _, unit := range call.Units {
		if unit.UnitRef > 0 {
			hasValidUnits = true
			break
		}
	}

	if hasValidUnits {
		// Send source field (v6 format) - use first unit's UnitRef > 0
		var firstValidUnit *CallUnit
		for i := range call.Units {
			if call.Units[i].UnitRef > 0 {
				firstValidUnit = &call.Units[i]
				break
			}
		}

		if firstValidUnit != nil {
			if w, err := mw.CreateFormField("source"); err == nil {
				if _, err = w.Write([]byte(fmt.Sprintf("%d", firstValidUnit.UnitRef))); err != nil {
					return formatError(err)
				}
			} else {
				return formatError(err)
			}
		}

		// Send sources array (v6 format) - only include units with UnitRef > 0
		sources := []map[string]any{}
		for _, unit := range call.Units {
			if unit.UnitRef > 0 {
				sources = append(sources, map[string]any{
					"pos": unit.Offset,
					"src": unit.UnitRef,
				})
			}
		}

		if len(sources) > 0 {
			if w, err := mw.CreateFormField("sources"); err == nil {
				if b, err := json.Marshal(sources); err == nil {
					if _, err = w.Write(b); err != nil {
						return formatError(err)
					}
				} else {
					return formatError(err)
				}
			} else {
				return formatError(err)
			}
		}
	}
	// If no valid units, DON'T send source/sources at all - let v6 store them as nil

	if err := mw.Close(); err != nil {
		return formatError(err)
	}

	if u, err := url.Parse(downstream.Url); err == nil {
		u.Path = path.Join(u.Path, "/api/call-upload")

		c := http.Client{Timeout: 30 * time.Second}

		if res, err := c.Post(u.String(), mw.FormDataContentType(), &buf); err == nil {
			if res.StatusCode != http.StatusOK {
				return formatError(fmt.Errorf("bad status: %s", res.Status))
			}

		} else {
			return formatError(err)
		}

	} else {
		return formatError(err)
	}

	return nil
}

type Downstreams struct {
	List       []*Downstream
	controller *Controller
	mutex      sync.Mutex
}

func NewDownstreams(controller *Controller) *Downstreams {
	return &Downstreams{
		List:       []*Downstream{},
		controller: controller,
		mutex:      sync.Mutex{},
	}
}

func (downstreams *Downstreams) FromMap(f []any) *Downstreams {
	downstreams.mutex.Lock()
	defer downstreams.mutex.Unlock()

	downstreams.List = []*Downstream{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			downstream := NewDownstream(downstreams.controller).FromMap(m)
			downstreams.List = append(downstreams.List, downstream)
		}
	}

	return downstreams
}

func (downstreams *Downstreams) Read(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
	)

	downstreams.mutex.Lock()
	defer downstreams.mutex.Unlock()

	downstreams.List = []*Downstream{}

	formatError := downstreams.errorFormatter("read")

	query = `SELECT "downstreamId", "apikey", "disabled", "name", "order", "systems", "url" FROM "downstreams"`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		var (
			downstream = NewDownstream(downstreams.controller)
			name       sql.NullString
			systems    string
		)

		if err = rows.Scan(&downstream.Id, &downstream.Apikey, &downstream.Disabled, &name, &downstream.Order, &systems, &downstream.Url); err != nil {
			break
		}

		if name.Valid {
			downstream.Name = name.String
		}

		if len(systems) > 0 {
			json.Unmarshal([]byte(systems), &downstream.Systems)
		}

		downstreams.List = append(downstreams.List, downstream)
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	sort.Slice(downstreams.List, func(i int, j int) bool {
		return downstreams.List[i].Order < downstreams.List[j].Order
	})

	return nil
}

func (downstreams *Downstreams) Send(controller *Controller, call *Call) {
	for _, downstream := range downstreams.List {
		if !downstream.HasAccess(call) {
			continue
		}
		ds := downstream // capture loop variable
		go func() {
			label := fmt.Sprintf("downstream: system=%d talkgroup=%d file=%s to %s", call.System.SystemRef, call.Talkgroup.TalkgroupRef, call.AudioFilename, ds.Url)
			if err := ds.Send(call); err == nil {
				controller.Logs.LogEvent(LogLevelInfo, label+" success")
			} else {
				controller.Logs.LogEvent(LogLevelError, label+" "+err.Error())
			}
		}()
	}
}

func (downstreams *Downstreams) Write(db *Database) error {
	var (
		downstreamIds = []uint64{}
		err           error
		query         string
		rows          *sql.Rows
		tx            *sql.Tx
	)

	downstreams.mutex.Lock()
	defer downstreams.mutex.Unlock()

	formatError := downstreams.errorFormatter("write")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "downstreamId" FROM "downstreams"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		var downstreamId uint64
		if err = rows.Scan(&downstreamId); err != nil {
			break
		}
		remove := true
		for _, downstream := range downstreams.List {
			if downstream.Id == 0 || downstream.Id == downstreamId {
				remove = false
				break
			}
		}
		if remove {
			downstreamIds = append(downstreamIds, downstreamId)
		}
	}

	rows.Close()

	if err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	if len(downstreamIds) > 0 {
		if b, err := json.Marshal(downstreamIds); err == nil {
			in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")
			query = fmt.Sprintf(`DELETE FROM "downstreams" WHERE "downstreamId" IN %s`, in)
			if _, err = tx.Exec(query); err != nil {
				tx.Rollback()
				return formatError(err, query)
			}
		}
	}

	for _, downstream := range downstreams.List {
		var (
			count   uint
			systems string
		)

		if downstream.Systems != nil {
			if b, err := json.Marshal(downstream.Systems); err == nil {
				systems = string(b)
			}
		}

		if downstream.Id > 0 {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "downstreams" WHERE "downstreamId" = %d`, downstream.Id)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}
		}

		if count == 0 {
			if downstream.Id > 0 {
				// Preserve the explicit ID when inserting
				query = fmt.Sprintf(`INSERT INTO "downstreams" ("downstreamId", "apikey", "disabled", "name", "order", "systems", "url") VALUES (%d, '%s', %t, '%s', %d, '%s', '%s')`, downstream.Id, escapeQuotes(downstream.Apikey), downstream.Disabled, escapeQuotes(downstream.Name), downstream.Order, systems, escapeQuotes(downstream.Url))
			} else {
				// Let database assign auto-increment ID
				query = fmt.Sprintf(`INSERT INTO "downstreams" ("apikey", "disabled", "name", "order", "systems", "url") VALUES ('%s', %t, '%s', %d, '%s', '%s')`, escapeQuotes(downstream.Apikey), downstream.Disabled, escapeQuotes(downstream.Name), downstream.Order, systems, escapeQuotes(downstream.Url))
			}
			if _, err = tx.Exec(query); err != nil {
				break
			}

		} else {
			query = fmt.Sprintf(`UPDATE "downstreams" SET "apikey" = '%s', "disabled" = %t, "name" = '%s', "order" = %d, "systems" = '%s', "url" = '%s' WHERE "downstreamId" = %d`, escapeQuotes(downstream.Apikey), downstream.Disabled, escapeQuotes(downstream.Name), downstream.Order, systems, escapeQuotes(downstream.Url), downstream.Id)
			if _, err = tx.Exec(query); err != nil {
				break
			}
		}
	}

	if err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func (downstreams *Downstreams) errorFormatter(label string) func(err error, query string) error {
	return func(err error, query string) error {
		s := fmt.Sprintf("downstreams.%s: %s", label, err.Error())

		if len(query) > 0 {
			s = fmt.Sprintf("%s in %s", s, query)
		}

		return errors.New(s)
	}
}
