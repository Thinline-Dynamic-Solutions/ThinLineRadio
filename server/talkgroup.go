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
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Talkgroup struct {
	Id                      uint64
	Delay                   uint
	Frequency               uint
	GroupIds                []uint64
	Kind                    string
	Label                   string
	Name                    string
	Order                   uint
	TagId                   uint64
	TalkgroupRef            uint
	ToneDetectionEnabled     bool
	ToneSets                 []ToneSet
	PreferredApiKeyId        *uint64 // Optional preferred API key for uploads
	ExcludeFromPreferredSite bool    // Exclude from preferred site detection (for interop/patched talkgroups)
	// Per-channel TonesToActive forwarding (forwards all tone sets for this talkgroup)
	ToneDownstreamEnabled bool   `json:"toneDownstreamEnabled"`
	ToneDownstreamURL     string `json:"toneDownstreamURL"`
	ToneDownstreamAPIKey  string `json:"toneDownstreamAPIKey"`
	// Alert cooldown: suppress repeat notifications on this talkgroup for N seconds after an alert fires.
	// 0 = disabled (default, existing behaviour unchanged).
	AlertCooldownSeconds uint `json:"alertCooldownSeconds"`

	// Cross-talkgroup voice association (Scenario 2).
	// When tones are detected on this talkgroup, the server also watches LinkedVoiceTalkgroupRef for a
	// voice call within LinkedVoiceWindowSeconds. Useful when an agency pages on a dedicated signalling
	// channel (TGID A) but dispatches voice on a separate tactical channel (TGID B).
	// 0 values disable the feature (default, fully backward compatible).
	LinkedVoiceTalkgroupRef      uint `json:"linkedVoiceTalkgroupRef"`
	LinkedVoiceWindowSeconds     uint `json:"linkedVoiceWindowSeconds"`
	LinkedVoiceMinDurationSeconds uint `json:"linkedVoiceMinDurationSeconds"`

	// Admin toggle: false suppresses all alerts & transcription for this talkgroup regardless of user prefs.
	// Default true preserves existing behaviour.
	AlertsEnabled bool `json:"alertsEnabled"`

	// Custom transcription prompt for this talkgroup. Overrides the system-level and global prompt when non-empty.
	TranscriptionPrompt string `json:"transcriptionPrompt"`
}

func NewTalkgroup() *Talkgroup {
	return &Talkgroup{
		GroupIds: []uint64{},
	}
}

func (talkgroup *Talkgroup) FromMap(m map[string]any) *Talkgroup {
	// Handle both "id" and "_id" fields for backward compatibility
	if v, ok := m["id"].(float64); ok {
		talkgroup.Id = uint64(v)
	} else if v, ok := m["_id"].(float64); ok {
		talkgroup.Id = uint64(v)
	}

	switch v := m["delay"].(type) {
	case float64:
		talkgroup.Delay = uint(v)
	}

	switch v := m["frequency"].(type) {
	case float64:
		talkgroup.Frequency = uint(v)
	}

	switch v := m["groupIds"].(type) {
	case []any:
		talkgroup.GroupIds = []uint64{}
		for _, v := range v {
			switch i := v.(type) {
			case float64:
				talkgroup.GroupIds = append(talkgroup.GroupIds, uint64(i))
			}
		}
	}

	switch v := m["type"].(type) {
	case string:
		talkgroup.Kind = v
	}

	switch v := m["label"].(type) {
	case string:
		talkgroup.Label = v
	}

	switch v := m["name"].(type) {
	case string:
		talkgroup.Name = v
	}

	switch v := m["order"].(type) {
	case float64:
		talkgroup.Order = uint(v)
	}

	switch v := m["tagId"].(type) {
	case float64:
		talkgroup.TagId = uint64(v)
	}

	switch v := m["talkgroupRef"].(type) {
	case float64:
		talkgroup.TalkgroupRef = uint(v)
	}

	switch v := m["toneDetectionEnabled"].(type) {
	case bool:
		talkgroup.ToneDetectionEnabled = v
	}

	switch v := m["toneSets"].(type) {
	case string:
		if toneSets, err := ParseToneSets(v); err == nil {
			talkgroup.ToneSets = toneSets
		}
	case []any:
		// Handle array format
		toneSetsJson, _ := json.Marshal(v)
		if toneSets, err := ParseToneSets(string(toneSetsJson)); err == nil {
			talkgroup.ToneSets = toneSets
		}
	}

	// Parse preferredApiKeyId (optional/nullable)
	switch v := m["preferredApiKeyId"].(type) {
	case float64:
		id := uint64(v)
		talkgroup.PreferredApiKeyId = &id
	case nil:
		talkgroup.PreferredApiKeyId = nil
	}

	// Parse excludeFromPreferredSite
	switch v := m["excludeFromPreferredSite"].(type) {
	case bool:
		talkgroup.ExcludeFromPreferredSite = v
	}

	// Parse per-channel TonesToActive forwarding
	switch v := m["toneDownstreamEnabled"].(type) {
	case bool:
		talkgroup.ToneDownstreamEnabled = v
	}

	switch v := m["toneDownstreamURL"].(type) {
	case string:
		talkgroup.ToneDownstreamURL = v
	}

	switch v := m["toneDownstreamAPIKey"].(type) {
	case string:
		talkgroup.ToneDownstreamAPIKey = v
	}

	switch v := m["alertCooldownSeconds"].(type) {
	case float64:
		talkgroup.AlertCooldownSeconds = uint(v)
	}

	switch v := m["linkedVoiceTalkgroupRef"].(type) {
	case float64:
		talkgroup.LinkedVoiceTalkgroupRef = uint(v)
	}

	switch v := m["linkedVoiceWindowSeconds"].(type) {
	case float64:
		talkgroup.LinkedVoiceWindowSeconds = uint(v)
	}

	switch v := m["linkedVoiceMinDurationSeconds"].(type) {
	case float64:
		talkgroup.LinkedVoiceMinDurationSeconds = uint(v)
	}

	// Parse alertsEnabled (defaults to true — no change in behaviour for existing data)
	switch v := m["alertsEnabled"].(type) {
	case bool:
		talkgroup.AlertsEnabled = v
	default:
		talkgroup.AlertsEnabled = true
	}

	// Parse transcriptionPrompt (empty string = inherit from system or global)
	switch v := m["transcriptionPrompt"].(type) {
	case string:
		talkgroup.TranscriptionPrompt = v
	}

	return talkgroup
}

func (talkgroup *Talkgroup) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"id":           talkgroup.Id,
		"groupIds":     talkgroup.GroupIds,
		"label":        talkgroup.Label,
		"name":         talkgroup.Name,
		"talkgroupRef": talkgroup.TalkgroupRef,
	}

	if talkgroup.Delay > 0 {
		m["delay"] = talkgroup.Delay
	}

	if talkgroup.Frequency > 0 {
		m["frequency"] = talkgroup.Frequency
	}

	if len(talkgroup.Kind) > 0 {
		m["type"] = talkgroup.Kind
	}

	if talkgroup.Order > 0 {
		m["order"] = talkgroup.Order
	}

	if talkgroup.TagId > 0 {
		m["tagId"] = talkgroup.TagId
	}

	m["toneDetectionEnabled"] = talkgroup.ToneDetectionEnabled

	if len(talkgroup.ToneSets) > 0 {
		if toneSetsJson, err := SerializeToneSets(talkgroup.ToneSets); err == nil {
			m["toneSets"] = json.RawMessage(toneSetsJson)
		}
	}

	// Include preferredApiKeyId if set
	if talkgroup.PreferredApiKeyId != nil {
		m["preferredApiKeyId"] = *talkgroup.PreferredApiKeyId
	} else {
		m["preferredApiKeyId"] = nil
	}

	// Include excludeFromPreferredSite
	m["excludeFromPreferredSite"] = talkgroup.ExcludeFromPreferredSite

	// Include per-channel TonesToActive forwarding
	m["toneDownstreamEnabled"] = talkgroup.ToneDownstreamEnabled
	if talkgroup.ToneDownstreamURL != "" {
		m["toneDownstreamURL"] = talkgroup.ToneDownstreamURL
	}
	if talkgroup.ToneDownstreamAPIKey != "" {
		m["toneDownstreamAPIKey"] = talkgroup.ToneDownstreamAPIKey
	}

	m["alertCooldownSeconds"] = talkgroup.AlertCooldownSeconds
	m["linkedVoiceTalkgroupRef"] = talkgroup.LinkedVoiceTalkgroupRef
	m["linkedVoiceWindowSeconds"] = talkgroup.LinkedVoiceWindowSeconds
	m["linkedVoiceMinDurationSeconds"] = talkgroup.LinkedVoiceMinDurationSeconds
	m["alertsEnabled"] = talkgroup.AlertsEnabled
	m["transcriptionPrompt"] = talkgroup.TranscriptionPrompt

	return json.Marshal(m)
}

type TalkgroupMap map[string]any

type Talkgroups struct {
	List  []*Talkgroup
	mutex sync.Mutex
}

func NewTalkgroups() *Talkgroups {
	return &Talkgroups{
		List:  []*Talkgroup{},
		mutex: sync.Mutex{},
	}
}

func (talkgroups *Talkgroups) FromMap(f []any) *Talkgroups {
	talkgroups.mutex.Lock()
	defer talkgroups.mutex.Unlock()

	talkgroups.List = []*Talkgroup{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			talkgroup := NewTalkgroup().FromMap(m)
			talkgroups.List = append(talkgroups.List, talkgroup)
		}
	}

	return talkgroups
}

func (talkgroups *Talkgroups) GetTalkgroupById(id uint64) (system *Talkgroup, ok bool) {
	talkgroups.mutex.Lock()
	defer talkgroups.mutex.Unlock()

	for _, talkgroup := range talkgroups.List {
		if talkgroup.Id == id {
			return talkgroup, true
		}
	}

	return nil, false
}

func (talkgroups *Talkgroups) GetTalkgroupByLabel(label string) (talkgroup *Talkgroup, ok bool) {
	talkgroups.mutex.Lock()
	defer talkgroups.mutex.Unlock()

	for _, talkgroup := range talkgroups.List {
		if talkgroup.Label == label {
			return talkgroup, true
		}
	}

	return nil, false
}

func (talkgroups *Talkgroups) GetTalkgroupByRef(ref uint) (talkgroup *Talkgroup, ok bool) {
	talkgroups.mutex.Lock()
	defer talkgroups.mutex.Unlock()

	for _, talkgroup := range talkgroups.List {
		if talkgroup.TalkgroupRef == ref {
			return talkgroup, true
		}
	}

	return nil, false
}

func (talkgroups *Talkgroups) ReadTx(tx *sql.Tx, systemId uint64, dbType string) error {
	var (
		err   error
		query string
		rows  *sql.Rows

		groupIds string
	)

	talkgroups.mutex.Lock()
	defer talkgroups.mutex.Unlock()

	talkgroups.List = []*Talkgroup{}

	formatError := errorFormatter("talkgroups", "read")

	if dbType == DbTypePostgresql {
		query = fmt.Sprintf(`SELECT t."talkgroupId", t."delay", t."frequency", t."label", t."name", t."order", t."tagId", t."talkgroupRef", t."type", t."toneDetectionEnabled", t."toneSets", t."preferredApiKeyId", t."excludeFromPreferredSite", t."toneDownstreamEnabled", t."toneDownstreamURL", t."toneDownstreamAPIKey", t."alertCooldownSeconds", t."linkedVoiceTalkgroupRef", t."linkedVoiceWindowSeconds", t."linkedVoiceMinDurationSeconds", t."alertsEnabled", t."transcriptionPrompt", STRING_AGG(CAST(COALESCE(tg."groupId", 0) AS text), ',') FROM "talkgroups" AS t LEFT JOIN "talkgroupGroups" AS tg ON tg."talkgroupId" = t."talkgroupId" WHERE t."systemId" = %d GROUP BY t."talkgroupId", t."preferredApiKeyId", t."excludeFromPreferredSite", t."toneDownstreamEnabled", t."toneDownstreamURL", t."toneDownstreamAPIKey", t."alertCooldownSeconds", t."linkedVoiceTalkgroupRef", t."linkedVoiceWindowSeconds", t."linkedVoiceMinDurationSeconds", t."alertsEnabled", t."transcriptionPrompt"`, systemId)

	} else {
		query = fmt.Sprintf(`SELECT t."talkgroupId", t."delay", t."frequency", t."label", t."name", t."order", t."tagId", t."talkgroupRef", t."type", t."toneDetectionEnabled", t."toneSets", t."preferredApiKeyId", t."excludeFromPreferredSite", t."toneDownstreamEnabled", t."toneDownstreamURL", t."toneDownstreamAPIKey", t."alertCooldownSeconds", t."linkedVoiceTalkgroupRef", t."linkedVoiceWindowSeconds", t."linkedVoiceMinDurationSeconds", t."alertsEnabled", t."transcriptionPrompt", GROUP_CONCAT(COALESCE(tg."groupId", 0)) FROM "talkgroups" AS t LEFT JOIN "talkgroupGroups" AS tg ON tg."talkgroupId" = t."talkgroupId" WHERE t."systemId" = %d GROUP BY t."talkgroupId"`, systemId)
	}

	if rows, err = tx.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		talkgroup := NewTalkgroup()
		var toneSetsJson string
		var preferredApiKeyId sql.NullInt64

		if err = rows.Scan(&talkgroup.Id, &talkgroup.Delay, &talkgroup.Frequency, &talkgroup.Label, &talkgroup.Name, &talkgroup.Order, &talkgroup.TagId, &talkgroup.TalkgroupRef, &talkgroup.Kind, &talkgroup.ToneDetectionEnabled, &toneSetsJson, &preferredApiKeyId, &talkgroup.ExcludeFromPreferredSite, &talkgroup.ToneDownstreamEnabled, &talkgroup.ToneDownstreamURL, &talkgroup.ToneDownstreamAPIKey, &talkgroup.AlertCooldownSeconds, &talkgroup.LinkedVoiceTalkgroupRef, &talkgroup.LinkedVoiceWindowSeconds, &talkgroup.LinkedVoiceMinDurationSeconds, &talkgroup.AlertsEnabled, &talkgroup.TranscriptionPrompt, &groupIds); err != nil {
			break
		}

		// Handle nullable preferredApiKeyId
		if preferredApiKeyId.Valid {
			id := uint64(preferredApiKeyId.Int64)
			talkgroup.PreferredApiKeyId = &id
		}

		// Parse tone sets
		if toneSetsJson != "" && toneSetsJson != "[]" {
			if toneSets, err := ParseToneSets(toneSetsJson); err == nil {
				talkgroup.ToneSets = toneSets
			}
		}

		for _, s := range strings.Split(groupIds, ",") {
			if i, err := strconv.Atoi(s); err == nil && i > 0 {
				talkgroup.GroupIds = append(talkgroup.GroupIds, uint64(i))
			}
		}

		talkgroups.List = append(talkgroups.List, talkgroup)
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	// Stable sort: primary by Order, secondary by Id to ensure consistent ordering
	sort.SliceStable(talkgroups.List, func(i int, j int) bool {
		if talkgroups.List[i].Order != talkgroups.List[j].Order {
			return talkgroups.List[i].Order < talkgroups.List[j].Order
		}
		// Secondary sort by talkgroup ID for stable ordering
		return talkgroups.List[i].Id < talkgroups.List[j].Id
	})

	return nil
}

func (talkgroups *Talkgroups) WriteTx(tx *sql.Tx, systemId uint64, dbType string) error {
	var (
		err   error
		query string
		res   sql.Result
		rows  *sql.Rows

		talkgroupGroupIds = []uint64{}
		talkgroupIds      = []uint64{}
	)

	talkgroups.mutex.Lock()
	defer talkgroups.mutex.Unlock()

	formatError := errorFormatter("talkgroups", "writetx")

	query = fmt.Sprintf(`SELECT "talkgroupId" FROM "talkgroups" WHERE "systemId" = %d`, systemId)
	if rows, err = tx.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		var talkgroupId uint64
		if err = rows.Scan(&talkgroupId); err != nil {
			break
		}
		remove := true
		for _, talkgroup := range talkgroups.List {
			if talkgroupId == 0 || talkgroup.Id == talkgroupId {
				remove = false
				break
			}
		}
		if remove {
			talkgroupIds = append(talkgroupIds, talkgroupId)
		}
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	if len(talkgroupIds) > 0 {
		if b, err := json.Marshal(talkgroupIds); err == nil {
			in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")

			query = fmt.Sprintf(`DELETE FROM "talkgroups" WHERE "talkgroupId" IN %s`, in)
			if _, err = tx.Exec(query); err != nil {
				return formatError(err, query)
			}

			query = fmt.Sprintf(`DELETE FROM "talkgroupGroups" WHERE "talkgroupId" IN %s`, in)
			if _, err = tx.Exec(query); err != nil {
				return formatError(err, query)
			}
		}
	}

	for _, talkgroup := range talkgroups.List {
		var count uint

		if talkgroup.Id > 0 {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "talkgroups" WHERE "talkgroupId" = %d`, talkgroup.Id)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}
		}

		// Validate that tagId exists - if not, use first available tag or "Untagged"
		var tagExists uint
		var validTagId uint64 = talkgroup.TagId
		if talkgroup.TagId > 0 {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "tags" WHERE "tagId" = %d`, talkgroup.TagId)
			if err = tx.QueryRow(query).Scan(&tagExists); err != nil {
				break
			}
			if tagExists == 0 {
				// Tag doesn't exist, try to get "Untagged" tag
				query = `SELECT "tagId" FROM "tags" WHERE "label" = 'Untagged' LIMIT 1`
				err = tx.QueryRow(query).Scan(&validTagId)
				if err == sql.ErrNoRows {
					// "Untagged" doesn't exist, get first available tag
					query = `SELECT "tagId" FROM "tags" ORDER BY "tagId" LIMIT 1`
					err = tx.QueryRow(query).Scan(&validTagId)
					if err == sql.ErrNoRows {
						// No tags exist at all - this should not happen if tags are written first
						// but we'll skip this talkgroup to avoid foreign key violation
						continue
					} else if err != nil {
						break
					}
				} else if err != nil {
					break
				}
			}
		} else {
			// TagId is 0 or invalid, try to get "Untagged" tag
			query = `SELECT "tagId" FROM "tags" WHERE "label" = 'Untagged' LIMIT 1`
			err = tx.QueryRow(query).Scan(&validTagId)
			if err == sql.ErrNoRows {
				// "Untagged" doesn't exist, get first available tag
				query = `SELECT "tagId" FROM "tags" ORDER BY "tagId" LIMIT 1`
				err = tx.QueryRow(query).Scan(&validTagId)
				if err == sql.ErrNoRows {
					// No tags exist at all - skip this talkgroup
					continue
				} else if err != nil {
					break
				}
			} else if err != nil {
				break
			}
		}

		// Serialize tone sets
		toneSetsJson := "[]"
		if len(talkgroup.ToneSets) > 0 {
			if json, err := SerializeToneSets(talkgroup.ToneSets); err == nil {
				toneSetsJson = json
			}
		}

		// Format preferredApiKeyId for SQL (NULL or number)
		preferredApiKeyIdSQL := "NULL"
		if talkgroup.PreferredApiKeyId != nil {
			preferredApiKeyIdSQL = fmt.Sprintf("%d", *talkgroup.PreferredApiKeyId)
		}

		if count == 0 {
			if talkgroup.Id > 0 {
				// Preserve the explicit ID when inserting
				query = fmt.Sprintf(`INSERT INTO "talkgroups" ("talkgroupId", "delay", "frequency", "label", "name", "order", "systemId", "tagId", "talkgroupRef", "type", "toneDetectionEnabled", "toneSets", "preferredApiKeyId", "excludeFromPreferredSite", "toneDownstreamEnabled", "toneDownstreamURL", "toneDownstreamAPIKey", "alertCooldownSeconds", "linkedVoiceTalkgroupRef", "linkedVoiceWindowSeconds", "linkedVoiceMinDurationSeconds", "alertsEnabled", "transcriptionPrompt") VALUES (%d, %d, %d, '%s', '%s', %d, %d, %d, %d, '%s', %t, '%s', %s, %t, %t, '%s', '%s', %d, %d, %d, %d, %t, '%s')`, talkgroup.Id, talkgroup.Delay, talkgroup.Frequency, escapeQuotes(talkgroup.Label), escapeQuotes(talkgroup.Name), talkgroup.Order, systemId, validTagId, talkgroup.TalkgroupRef, talkgroup.Kind, talkgroup.ToneDetectionEnabled, escapeQuotes(toneSetsJson), preferredApiKeyIdSQL, talkgroup.ExcludeFromPreferredSite, talkgroup.ToneDownstreamEnabled, escapeQuotes(talkgroup.ToneDownstreamURL), escapeQuotes(talkgroup.ToneDownstreamAPIKey), talkgroup.AlertCooldownSeconds, talkgroup.LinkedVoiceTalkgroupRef, talkgroup.LinkedVoiceWindowSeconds, talkgroup.LinkedVoiceMinDurationSeconds, talkgroup.AlertsEnabled, escapeQuotes(talkgroup.TranscriptionPrompt))
			} else {
				// Let database assign auto-increment ID
				query = fmt.Sprintf(`INSERT INTO "talkgroups" ("delay", "frequency", "label", "name", "order", "systemId", "tagId", "talkgroupRef", "type", "toneDetectionEnabled", "toneSets", "preferredApiKeyId", "excludeFromPreferredSite", "toneDownstreamEnabled", "toneDownstreamURL", "toneDownstreamAPIKey", "alertCooldownSeconds", "linkedVoiceTalkgroupRef", "linkedVoiceWindowSeconds", "linkedVoiceMinDurationSeconds", "alertsEnabled", "transcriptionPrompt") VALUES (%d, %d, '%s', '%s', %d, %d, %d, %d, '%s', %t, '%s', %s, %t, %t, '%s', '%s', %d, %d, %d, %d, %t, '%s')`, talkgroup.Delay, talkgroup.Frequency, escapeQuotes(talkgroup.Label), escapeQuotes(talkgroup.Name), talkgroup.Order, systemId, validTagId, talkgroup.TalkgroupRef, talkgroup.Kind, talkgroup.ToneDetectionEnabled, escapeQuotes(toneSetsJson), preferredApiKeyIdSQL, talkgroup.ExcludeFromPreferredSite, talkgroup.ToneDownstreamEnabled, escapeQuotes(talkgroup.ToneDownstreamURL), escapeQuotes(talkgroup.ToneDownstreamAPIKey), talkgroup.AlertCooldownSeconds, talkgroup.LinkedVoiceTalkgroupRef, talkgroup.LinkedVoiceWindowSeconds, talkgroup.LinkedVoiceMinDurationSeconds, talkgroup.AlertsEnabled, escapeQuotes(talkgroup.TranscriptionPrompt))
			}

			if dbType == DbTypePostgresql {
				query = query + ` RETURNING "talkgroupId"`

				if err = tx.QueryRow(query).Scan(&talkgroup.Id); err != nil {
					break
				}

			} else {
				if res, err = tx.Exec(query); err == nil {
					if id, err := res.LastInsertId(); err == nil {
						talkgroup.Id = uint64(id)
					}
				} else {
					break
				}
			}

		} else {
			// Serialize tone sets (already done above, but we're in else block so need to recalculate)
			toneSetsJson := "[]"
			if len(talkgroup.ToneSets) > 0 {
				if json, err := SerializeToneSets(talkgroup.ToneSets); err == nil {
					toneSetsJson = json
				}
			}
			// preferredApiKeyIdSQL is already calculated above
			query = fmt.Sprintf(`UPDATE "talkgroups" SET "delay" = %d, "frequency" = %d, "label" = '%s', "name" = '%s', "order" = %d, "tagId" = %d, "talkgroupRef" = %d, "type" = '%s', "toneDetectionEnabled" = %t, "toneSets" = '%s', "preferredApiKeyId" = %s, "excludeFromPreferredSite" = %t, "toneDownstreamEnabled" = %t, "toneDownstreamURL" = '%s', "toneDownstreamAPIKey" = '%s', "alertCooldownSeconds" = %d, "linkedVoiceTalkgroupRef" = %d, "linkedVoiceWindowSeconds" = %d, "linkedVoiceMinDurationSeconds" = %d, "alertsEnabled" = %t, "transcriptionPrompt" = '%s' WHERE "talkgroupId" = %d`, talkgroup.Delay, talkgroup.Frequency, escapeQuotes(talkgroup.Label), escapeQuotes(talkgroup.Name), talkgroup.Order, validTagId, talkgroup.TalkgroupRef, talkgroup.Kind, talkgroup.ToneDetectionEnabled, escapeQuotes(toneSetsJson), preferredApiKeyIdSQL, talkgroup.ExcludeFromPreferredSite, talkgroup.ToneDownstreamEnabled, escapeQuotes(talkgroup.ToneDownstreamURL), escapeQuotes(talkgroup.ToneDownstreamAPIKey), talkgroup.AlertCooldownSeconds, talkgroup.LinkedVoiceTalkgroupRef, talkgroup.LinkedVoiceWindowSeconds, talkgroup.LinkedVoiceMinDurationSeconds, talkgroup.AlertsEnabled, escapeQuotes(talkgroup.TranscriptionPrompt), talkgroup.Id)
			if _, err = tx.Exec(query); err != nil {
				break
			}
		}

		query = fmt.Sprintf(`SELECT "groupId", "talkgroupGroupId" FROM "talkgroupGroups" WHERE "talkgroupId" = %d`, talkgroup.Id)
		if rows, err = tx.Query(query); err != nil {
			break
		}

		for rows.Next() {
			var (
				groupId          uint64
				talkgroupGroupId uint64
			)
			if err = rows.Scan(&groupId, &talkgroupGroupId); err != nil {
				break
			}
			remove := true
			for _, id := range talkgroup.GroupIds {
				if id == 0 || id == talkgroupGroupId {
					remove = false
					break
				}
			}
			if remove {
				talkgroupGroupIds = append(talkgroupGroupIds, talkgroupGroupId)
			}
		}

		rows.Close()

		if err != nil {
			return formatError(err, "")
		}

		if len(talkgroupGroupIds) > 0 {
			if b, err := json.Marshal(talkgroupGroupIds); err == nil {
				in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")
				query = fmt.Sprintf(`DELETE FROM "talkgroupGroups" WHERE "talkgroupGroupId" IN %s`, in)
				if _, err = tx.Exec(query); err != nil {
					return formatError(err, query)
				}
			}
		}

		for _, groupId := range talkgroup.GroupIds {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "talkgroupGroups" WHERE "talkgroupId" = %d AND "groupId" = %d`, talkgroup.Id, groupId)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}

			if count == 0 {
				query = fmt.Sprintf(`INSERT INTO "talkgroupGroups" ("groupId", "talkgroupId") VALUES (%d, %d)`, groupId, talkgroup.Id)
				if _, err = tx.Exec(query); err != nil {
					break
				}
			}
		}

		// If the groupId inner loop encountered an error (e.g. FK violation on a deleted
		// group), break out of the outer talkgroup loop immediately.  Without this break,
		// the outer loop would continue running queries on a transaction that PostgreSQL
		// has already marked aborted (SQLSTATE 25P02), masking the real error.
		if err != nil {
			break
		}
	}

	if err != nil {
		return formatError(err, query)
	}

	return nil
}

type TalkgroupsMap []TalkgroupMap
