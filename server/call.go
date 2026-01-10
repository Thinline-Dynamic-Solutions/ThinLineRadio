// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type CallFrequency struct {
	Id        uint64
	CallId    uint64
	Dbm       int
	Errors    uint
	Frequency uint
	Offset    float32
	Spikes    uint
}

type CallMeta struct {
	SiteId          uint64
	SiteLabel       string
	SiteRef         uint
	SystemId        uint64
	SystemLabel     string
	SystemRef       uint
	TalkgroupGroups []string
	TalkgroupId     uint64
	TalkgroupLabel  string
	TalkgroupName   string
	TalkgroupRef    uint
	TalkgroupTag    string
	UnitLabels      []string
	UnitRefs        []uint
}

type CallUnit struct {
	Id      uint64
	CallId  uint64
	Offset  float32
	UnitRef uint
}

type Call struct {
	Id            uint64
	Audio         []byte
	AudioFilename string
	AudioMime     string
	Delayed       bool
	Frequencies   []CallFrequency
	Frequency     uint
	Meta          CallMeta
	Patches       []uint
	SiteRef       uint
	System        *System
	Talkgroup     *Talkgroup
	Timestamp     time.Time
	Units         []CallUnit
	ToneSequence  *ToneSequence
	HasTones      bool
	Transcript    string
	TranscriptConfidence float64
	TranscriptionStatus string

	// Add back simple fields for compatibility with v6 uploads
	SystemId    uint `json:"system"`
	TalkgroupId uint `json:"talkgroup"`
}

func NewCall() *Call {
	return &Call{
		Frequencies: []CallFrequency{},
		Frequency:   0,
		Meta: CallMeta{
			TalkgroupGroups: []string{},
			UnitLabels:      []string{},
			UnitRefs:        []uint{},
		},
		Patches:     []uint{},
		Units:       []CallUnit{},
		SystemId:    0,
		TalkgroupId: 0,
	}
}

func (call *Call) IsValid() (ok bool, err error) {
	ok = true

	if len(call.Audio) <= 44 {
		ok = false
		err = errors.New("no audio")
	}

	if call.Timestamp.UnixMilli() == 0 {
		ok = false
		err = errors.New("no timestamp")
	}

	if call.SystemId < 1 {
		ok = false
		err = errors.New("no system")
	}

	if call.TalkgroupId < 1 {
		ok = false
		err = errors.New("no talkgroup")
	}

	return ok, err
}

func (call *Call) MarshalJSON() ([]byte, error) {
	audio := strings.ReplaceAll(fmt.Sprintf("%v", call.Audio), " ", ",")

	callMap := map[string]any{
		"id": call.Id,
		"audio": map[string]any{
			"data": json.RawMessage(audio),
			"type": "Buffer",
		},
		"audioName": call.AudioFilename,
		"audioType": call.AudioMime,
		"dateTime":  call.Timestamp.Format(time.RFC3339),
		"delayed":   call.Delayed,
		"patches":   call.Patches,
		"hasTones":  call.HasTones,
	}

	if call.ToneSequence != nil {
		callMap["toneSequence"] = call.ToneSequence
	}

	if call.Transcript != "" {
		callMap["transcript"] = call.Transcript
		callMap["transcriptConfidence"] = call.TranscriptConfidence
		callMap["transcriptionStatus"] = call.TranscriptionStatus
	}

	if len(call.Frequencies) > 0 {
		freqs := []map[string]any{}
		for _, f := range call.Frequencies {
			freq := map[string]any{
				"errorCount": f.Errors,
				"freq":       f.Frequency,
				"pos":        f.Offset,
				"spikeCount": f.Spikes,
			}

			if f.Dbm > 0 {
				freq["dbm"] = f.Dbm
			}

			freqs = append(freqs, freq)
		}

		callMap["frequencies"] = freqs
	}

	if call.SiteRef > 0 {
		callMap["site"] = call.SiteRef
	}

	if call.System != nil {
		callMap["system"] = call.System.SystemRef
	} else if call.SystemId > 0 {
		callMap["system"] = call.SystemId
	}

	if call.Talkgroup != nil {
		callMap["talkgroup"] = call.Talkgroup.TalkgroupRef
	} else if call.TalkgroupId > 0 {
		callMap["talkgroup"] = call.TalkgroupId
	}

	// Populate Units from Meta.UnitRefs if Units is empty (for JSON marshaling only)
	unitsToUse := call.Units
	if len(unitsToUse) == 0 && len(call.Meta.UnitRefs) > 0 {
		// Create temporary units from Meta.UnitRefs for marshaling
		unitsToUse = make([]CallUnit, 0, len(call.Meta.UnitRefs))
		for _, unitRef := range call.Meta.UnitRefs {
			// Include all unitRefs, even 0, to match v6 behavior
			unitsToUse = append(unitsToUse, CallUnit{
				UnitRef: unitRef,
				Offset:  0,
			})
		}
	}

	if len(unitsToUse) > 0 {
		sources := []map[string]any{}
		for _, unit := range unitsToUse {
			sources = append(sources, map[string]any{
				"pos": unit.Offset,
				"src": unit.UnitRef,
			})
		}
		callMap["sources"] = sources
		// Also set source field as fallback for frontend compatibility (v6 style)
		// Always set source from first unit, even if 0, to match v6 behavior
		callMap["source"] = unitsToUse[0].UnitRef
	} else {
		// If no units at all, set source to 0 to match v6 behavior where source is always present
		callMap["source"] = 0
	}

	if call.Frequency > 0 {
		callMap["frequency"] = call.Frequency
	}

	return json.Marshal(callMap)
}

func (call *Call) ToJson() (string, error) {
	if b, err := json.Marshal(call); err == nil {
		return string(b), nil
	} else {
		return "", fmt.Errorf("call.tojson: %v", err)
	}
}

type Calls struct {
	controller *Controller
}

func NewCalls(controller *Controller) *Calls {
	return &Calls{
		controller: controller,
	}
}

func (calls *Calls) CheckDuplicate(call *Call, msTimeFrame uint, db *Database) (bool, error) {
	var count uint64

	formatError := errorFormatter("calls", "checkduplicate")

	d := time.Duration(msTimeFrame) * time.Millisecond
	from := call.Timestamp.Add(-d)
	to := call.Timestamp.Add(d)

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := fmt.Sprintf(`SELECT COUNT(*) FROM "calls" WHERE ("timestamp" BETWEEN %d and %d) AND "systemId" = %d AND "talkgroupId" = %d`, from.UnixMilli(), to.UnixMilli(), call.System.Id, call.Talkgroup.Id)
	if err := db.Sql.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return false, formatError(err, query)
	}

	return count > 0, nil
}

func (calls *Calls) GetCall(id uint64) (*Call, error) {
	var (
		err       error
		query     string
		rows      *sql.Rows
		tx        *sql.Tx

		patch       string
		systemId    uint64
		talkgroupId uint64
		timestamp   int64
		frequency   sql.NullInt64
	)

	formatError := errorFormatter("calls", "getcall")

	// Check if this call is currently delayed
	if calls.controller.Delayer.IsCallDelayed(id) {
		return nil, formatError(fmt.Errorf("call %d is currently delayed and not available for playback", id), "")
	}

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if tx, err = calls.controller.Database.Sql.BeginTx(ctx, nil); err != nil {
		return nil, formatError(err, "")
	}

	call := Call{Id: id}

	if calls.controller.Database.Config.DbType == DbTypePostgresql {
		query = fmt.Sprintf(`SELECT c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", STRING_AGG(CAST(COALESCE(cpt."talkgroupRef", 0) AS text), ','), sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus" FROM "calls" AS c LEFT JOIN "callPatches" AS cp on cp."callId" = c."callId" LEFT JOIN "talkgroups" AS cpt ON cpt."talkgroupId" = cp."talkgroupId" LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId" LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId" WHERE c."callId" = %d GROUP BY c."callId", c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus"`, id)

	} else {
		query = fmt.Sprintf(`SELECT c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", GROUP_CONCAT(COALESCE(cpt."talkgroupRef", 0)), sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus" FROM "calls" AS c LEFT JOIN "callPatches" AS cp on cp."callId" = c."callId" LEFT JOIN "talkgroups" AS cpt ON cpt."talkgroupId" = cp."talkgroupId" LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId" LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId" WHERE c."callId" = %d GROUP BY c."callId", c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus"`, id)
	}

	var toneSequenceJson sql.NullString
	var transcript sql.NullString
	var transcriptConfidence sql.NullFloat64
	var transcriptionStatus sql.NullString
	
	if err = tx.QueryRow(query).Scan(&call.Audio, &call.AudioFilename, &call.AudioMime, &call.SiteRef, &timestamp, &patch, &systemId, &talkgroupId, &frequency, &toneSequenceJson, &call.HasTones, &transcript, &transcriptConfidence, &transcriptionStatus); err != nil && err != sql.ErrNoRows {
		tx.Rollback()
		return nil, formatError(err, query)
	}

	call.Timestamp = time.UnixMilli(timestamp)

	if frequency.Valid && frequency.Int64 > 0 {
		call.Frequency = uint(frequency.Int64)
		call.Frequencies = []CallFrequency{
			{
				Frequency: call.Frequency,
			},
		}
	}

	// Parse tone sequence
	if toneSequenceJson.Valid && toneSequenceJson.String != "" && toneSequenceJson.String != "[]" {
		var toneSequence ToneSequence
		if err := json.Unmarshal([]byte(toneSequenceJson.String), &toneSequence); err == nil {
			call.ToneSequence = &toneSequence
			call.HasTones = len(toneSequence.Tones) > 0
		}
	}

	// Load transcript
	if transcript.Valid {
		call.Transcript = transcript.String
	}
	if transcriptConfidence.Valid {
		call.TranscriptConfidence = transcriptConfidence.Float64
	}
	if transcriptionStatus.Valid {
		call.TranscriptionStatus = transcriptionStatus.String
	}

	if len(patch) > 0 {
		for _, s := range strings.Split(patch, ",") {
			if i, err := strconv.Atoi(s); err == nil && i > 0 {
				call.Patches = append(call.Patches, uint(i))
			}
		}
	}

	if system, ok := calls.controller.Systems.GetSystemById(systemId); ok {
		call.System = system

	} else {
		return nil, formatError(fmt.Errorf("cannot retrieve system id %d for call id %d", systemId, call.Id), "")
	}

	if talkgroup, ok := call.System.Talkgroups.GetTalkgroupById(talkgroupId); ok {
		call.Talkgroup = talkgroup

	} else {
		return nil, formatError(fmt.Errorf("cannot retrieve talkgroup id %d for call id %d", talkgroupId, call.Id), "")
	}

	query = fmt.Sprintf(`SELECT "offset", "unitRef" FROM "callUnits" WHERE "callId" = %d`, id)
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return nil, formatError(err, query)
	}

	for rows.Next() {
		unit := CallUnit{}

		if err = rows.Scan(&unit.Offset, &unit.UnitRef); err != nil {
			break
		}

		call.Units = append(call.Units, unit)
	}

	rows.Close()

	if err != nil {
		tx.Rollback()
		return nil, formatError(err, query)
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return nil, formatError(err, "")
	}

	return &call, nil
}

func (calls *Calls) Prune(db *Database, pruneDays uint) error {
	timestamp := time.Now().Add(-24 * time.Hour * time.Duration(pruneDays)).UnixMilli()
	query := fmt.Sprintf(`DELETE FROM "calls" WHERE "timestamp" < %d`, timestamp)

	if _, err := db.Sql.Exec(query); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (calls *Calls) Search(searchOptions *CallsSearchOptions, client *Client) (*CallsSearchResults, error) {
	const (
		ascOrder  = "ASC"
		descOrder = "DESC"
	)

	var (
		err  error
		rows *sql.Rows

		limit  uint
		offset uint
		order  string
		query  string

		timestamp int64
	)

	db := client.Controller.Database

	formatError := errorFormatter("calls", "search")

	searchResults := &CallsSearchResults{
		Options: searchOptions,
		Results: []CallsSearchResult{},
	}

	calls.controller.Logs.LogEvent(LogLevelInfo, "Client access evaluation complete")

	// Determine sort order early so we can use it for date filtering
	switch v := searchOptions.Sort.(type) {
	case int:
		if v < 0 {
			order = descOrder
		} else {
			order = ascOrder
		}
	default:
		order = ascOrder
	}

	// Build WHERE conditions using slice-based approach (like v6)
	where := []string{
		`c."systemId" > 0`,
		`c."talkgroupId" > 0`,
		`c."systemRef" > 0`,
		`c."talkgroupRef" > 0`,
		`d."callId" IS NULL`,
	}

	// System/Talkgroup filters (added first for optimal index usage)
	switch v := searchOptions.System.(type) {
	case uint:
		conditions := []string{
			fmt.Sprintf(`c."systemRef" = %d`, v),
		}
		switch v := searchOptions.Talkgroup.(type) {
		case uint:
			conditions = append(conditions, fmt.Sprintf(`c."talkgroupRef" = %d`, v))
		}
		if len(conditions) > 0 {
			where = append(where, fmt.Sprintf("(%s)", strings.Join(conditions, " AND ")))
		}
	}

	switch v := searchOptions.Group.(type) {
	case string:
		groupConditions := []string{}
		for id, m := range client.GroupsMap[v] {
			in := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", m), " ", ", "), "[", "("), "]", ")")
			groupConditions = append(groupConditions, fmt.Sprintf(`(c."systemRef" = %d AND c."talkgroupRef" IN %s)`, id, in))
		}
		if len(groupConditions) > 0 {
			where = append(where, fmt.Sprintf("(%s)", strings.Join(groupConditions, " OR ")))
		}
	}

	switch v := searchOptions.Tag.(type) {
	case string:
		tagConditions := []string{}
		for id, m := range client.TagsMap[v] {
			in := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", m), " ", ", "), "[", "("), "]", ")")
			tagConditions = append(tagConditions, fmt.Sprintf(`(c."systemRef" = %d AND c."talkgroupRef" IN %s)`, id, in))
		}
		if len(tagConditions) > 0 {
			where = append(where, fmt.Sprintf("(%s)", strings.Join(tagConditions, " OR ")))
		}
	}

	// Calculate the effective delay for this specific client
	var effectiveDelay uint = 0

	if calls.controller.requiresUserAuth() && client.User != nil {
		for _, delay := range client.User.talkgroupDelaysMap {
			if delay > 0 && (effectiveDelay == 0 || delay < effectiveDelay) {
				effectiveDelay = delay
			}
		}

		if effectiveDelay == 0 {
			for _, delay := range client.User.systemDelaysMap {
				if delay > 0 && (effectiveDelay == 0 || delay < effectiveDelay) {
					effectiveDelay = delay
				}
			}
		}

		if effectiveDelay == 0 && client.User.Delay > 0 {
			effectiveDelay = uint(client.User.Delay)
		}

		if effectiveDelay == 0 {
			effectiveDelay = calls.controller.Options.DefaultSystemDelay
		}
	} else {
		effectiveDelay = calls.controller.Options.DefaultSystemDelay
	}

	// Apply the delay filtering
	if effectiveDelay > 0 {
		now := time.Now()
		delayDuration := time.Duration(effectiveDelay) * time.Minute
		cutoffTime := now.Add(-delayDuration)
		cutoffTimeMs := cutoffTime.UnixMilli()

		// Add delay condition: only include calls older than the delay period
		where = append(where, fmt.Sprintf(`c."timestamp" <= %d`, cutoffTimeMs))
	}

	// Date filter - use simple comparisons instead of BETWEEN (like v6/Python)
	switch v := searchOptions.Date.(type) {
	case time.Time:
		selectedDateMs := v.UnixMilli()
		if order == descOrder {
			// Newest first: show calls from selected date backwards (older calls)
			where = append(where, fmt.Sprintf(`c."timestamp" <= %d`, selectedDateMs))
		} else {
			// Oldest first: show calls from selected date forward (newer calls)
			where = append(where, fmt.Sprintf(`c."timestamp" >= %d`, selectedDateMs))
		}
	default:
		// No date selected - no time filter applied
	}

	// Build final WHERE clause
	whereClause := "1=1"
	for _, condition := range where {
		whereClause += " AND " + condition
	}

	// Use the same WHERE clause for delay filtering
	delayWhere := "WHERE " + whereClause

	// Skip expensive MIN/MAX queries for DateStart/DateStop
	// Set defaults - these are informational only and not critical for functionality
	searchResults.DateStart = time.Time{}
	searchResults.DateStop = time.Time{}

	switch v := searchOptions.Limit.(type) {
	case uint:
		limit = uint(math.Min(float64(500), float64(v)))
	default:
		limit = 200
	}

	switch v := searchOptions.Offset.(type) {
	case uint:
		offset = v
	}

	// Skip COUNT(*) query to avoid querying entire database
	// We'll use hasMore flag based on whether we got exactly 'limit' results
	searchResults.Count = 0
	searchResults.HasMore = false

	// Use subquery approach for PostgreSQL
	// Query for limit+1 to determine if there are more results
	queryLimit := limit + 1
	query = fmt.Sprintf(`SELECT c."callId", c."timestamp", c."systemRef", c."talkgroupRef", c."frequency", c."siteRef", (SELECT cu."unitRef" FROM "callUnits" cu WHERE cu."callId" = c."callId" ORDER BY cu."offset" LIMIT 1) as "source" FROM "calls" AS c LEFT JOIN "delayed" AS d ON d."callId" = c."callId" %s ORDER BY c."timestamp" %s LIMIT %d OFFSET %d`, delayWhere, order, queryLimit, offset)
	
	calls.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Search RESULTS query: %s", query))

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if rows, err = db.Sql.QueryContext(ctx, query); err != nil && err != sql.ErrNoRows {
		return nil, formatError(err, query)
	}

	var totalCalls, includedCalls int

	for rows.Next() {
		searchResult := CallsSearchResult{}
		var frequency sql.NullInt64
		var siteRef sql.NullInt64
		var source sql.NullInt64
		if err = rows.Scan(&searchResult.Id, &timestamp, &searchResult.System, &searchResult.Talkgroup, &frequency, &siteRef, &source); err != nil {
			break
		}

		searchResult.Timestamp = time.UnixMilli(timestamp)
		if frequency.Valid && frequency.Int64 > 0 {
			searchResult.Frequency = uint(frequency.Int64)
		}
		if siteRef.Valid && siteRef.Int64 > 0 {
			searchResult.Site = uint(siteRef.Int64)
		}
		if source.Valid && source.Int64 > 0 {
			searchResult.Source = uint(source.Int64)
		}
		totalCalls++

		// Only include up to 'limit' results (drop the extra one we fetched)
		if includedCalls < int(limit) {
			searchResults.Results = append(searchResults.Results, searchResult)
			includedCalls++
		}
	}

	rows.Close()

	if err != nil {
		return nil, formatError(err, "")
	}

	// Set count to actual number of results returned (should be limit)
	searchResults.Count = uint(len(searchResults.Results))
	
	// If we fetched more than 'limit' rows, there are more results available
	searchResults.HasMore = totalCalls > int(limit)

	return searchResults, err
}

func (calls *Calls) WriteCall(call *Call, db *Database) (uint64, error) {
	var (
		err   error
		query string
		res   sql.Result
		tx    *sql.Tx
	)

	formatError := errorFormatter("calls", "writecall")

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if tx, err = db.Sql.BeginTx(ctx, nil); err != nil {
		return 0, formatError(err, "")
	}

	frequencyValue := call.Frequency
	if frequencyValue == 0 && len(call.Frequencies) > 0 {
		if call.Frequencies[0].Frequency > 0 {
			frequencyValue = call.Frequencies[0].Frequency
		}
	}

	if frequencyValue > 0 && call.Frequency == 0 {
		call.Frequency = frequencyValue
	}
	if frequencyValue > 0 && len(call.Frequencies) == 0 {
		call.Frequencies = []CallFrequency{
			{
				Frequency: frequencyValue,
			},
		}
	}

	// Serialize tone sequence
	toneSequenceJson := ""
	if call.ToneSequence != nil {
		if json, err := SerializeToneSequence(call.ToneSequence); err == nil {
			toneSequenceJson = json
		}
	}
	if toneSequenceJson == "" {
		toneSequenceJson = "{}"
	}

	// Default transcription status
	if call.TranscriptionStatus == "" {
		call.TranscriptionStatus = "pending"
	}

	if db.Config.DbType == DbTypePostgresql {
		query = fmt.Sprintf(`INSERT INTO "calls" ("audio", "audioFilename", "audioMime", "siteRef", "systemId", "talkgroupId", "systemRef", "talkgroupRef", "timestamp", "frequency", "toneSequence", "hasTones", "transcript", "transcriptConfidence", "transcriptionStatus") VALUES ($1, '%s', '%s', %d, %d, %d, %d, %d, %d, %d, $2, %t, $3, %.2f, '%s') RETURNING "callId"`, call.AudioFilename, call.AudioMime, call.SiteRef, call.System.Id, call.Talkgroup.Id, call.System.SystemRef, call.Talkgroup.TalkgroupRef, call.Timestamp.UnixMilli(), frequencyValue, call.HasTones, call.TranscriptConfidence, escapeQuotes(call.TranscriptionStatus))

		err = tx.QueryRow(query, call.Audio, toneSequenceJson, call.Transcript).Scan(&call.Id)

	} else {
		query = fmt.Sprintf(`INSERT INTO "calls" ("audio", "audioFilename", "audioMime", "siteRef", "systemId", "talkgroupId", "systemRef", "talkgroupRef", "timestamp", "frequency", "toneSequence", "hasTones", "transcript", "transcriptConfidence", "transcriptionStatus") VALUES (?, '%s', '%s', %d, %d, %d, %d, %d, %d, %d, ?, %t, ?, %.2f, '%s')`, call.AudioFilename, call.AudioMime, call.SiteRef, call.System.Id, call.Talkgroup.Id, call.System.SystemRef, call.Talkgroup.TalkgroupRef, call.Timestamp.UnixMilli(), frequencyValue, call.HasTones, call.TranscriptConfidence, escapeQuotes(call.TranscriptionStatus))

		if res, err = tx.Exec(query, call.Audio, toneSequenceJson, call.Transcript); err == nil {
			if id, err := res.LastInsertId(); err == nil {
				call.Id = uint64(id)
			}
		}
	}

	if err != nil {
		tx.Rollback()
		return 0, formatError(err, query)
	}

	for _, ref := range call.Patches {
		var talkgroupId sql.NullInt64
		query = fmt.Sprintf(`SELECT "talkgroupId" FROM "talkgroups" WHERE "systemId" = %d and "talkgroupRef" = %d`, call.System.Id, ref)
		if err = tx.QueryRow(query).Scan(&talkgroupId); err != nil && err != sql.ErrNoRows {
			tx.Rollback()
			return 0, formatError(err, query)
		}
		if !talkgroupId.Valid {
			continue
		}
		query = fmt.Sprintf(`INSERT INTO "callPatches" ("callId", "talkgroupId") VALUES (%d, %d)`, call.Id, talkgroupId.Int64)
		if _, err = tx.Exec(query); err != nil {
			tx.Rollback()
			return 0, formatError(err, query)
		}
	}

	for _, unit := range call.Units {
		// Skip invalid unitRef values from Trunk Recorder (e.g., -1 which wraps to 18446744073709551615)
		// Trunk Recorder sends -1 when radio ID is unknown or not determined
		// PostgreSQL bigint max is 9223372036854775807, so wrapped values exceed this
		if unit.UnitRef > 9223372036854775807 {
			continue
		}
		query = fmt.Sprintf(`INSERT INTO "callUnits" ("callId", "offset", "unitRef") VALUES (%d, %f, %d)`, call.Id, unit.Offset, unit.UnitRef)
		if _, err = tx.Exec(query); err != nil {
			tx.Rollback()
			return 0, formatError(err, query)
		}
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return 0, formatError(err, "")
	}

	return uint64(call.Id), nil
}

type CallsSearchOptions struct {
	Date      any `json:"date,omitempty"`
	Group     any `json:"group,omitempty"`
	Limit     any `json:"limit,omitempty"`
	Offset    any `json:"offset,omitempty"`
	Sort      any `json:"sort,omitempty"`
	System    any `json:"system,omitempty"`
	Tag       any `json:"tag,omitempty"`
	Talkgroup any `json:"talkgroup,omitempty"`
}

func NewCallSearchOptions() *CallsSearchOptions {
	return &CallsSearchOptions{}
}

func (searchOptions *CallsSearchOptions) fromMap(m map[string]any) *CallsSearchOptions {
	switch v := m["date"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			searchOptions.Date = t
		}
	}

	switch v := m["group"].(type) {
	case string:
		searchOptions.Group = v
	}

	switch v := m["limit"].(type) {
	case float64:
		searchOptions.Limit = uint(v)
	}

	switch v := m["offset"].(type) {
	case float64:
		searchOptions.Offset = uint(v)
	}

	switch v := m["sort"].(type) {
	case float64:
		searchOptions.Sort = int(v)
	}

	switch v := m["system"].(type) {
	case float64:
		searchOptions.System = uint(v)
	}

	switch v := m["tag"].(type) {
	case string:
		searchOptions.Tag = v
	}

	switch v := m["talkgroup"].(type) {
	case float64:
		searchOptions.Talkgroup = uint(v)
	}

	return searchOptions
}

type CallsSearchResult struct {
	Id        uint64    `json:"id"`
	System    uint      `json:"system"`
	Talkgroup uint      `json:"talkgroup"`
	Timestamp time.Time `json:"dateTime"`
	Frequency uint      `json:"frequency,omitempty"`
	Source    uint      `json:"source,omitempty"`
	Site      uint      `json:"site,omitempty"`
}

type CallsSearchResults struct {
	Count     uint                `json:"count"`
	HasMore   bool                `json:"hasMore"`
	DateStart time.Time           `json:"dateStart"`
	DateStop  time.Time           `json:"dateStop"`
	Options   *CallsSearchOptions `json:"options"`
	Results   []CallsSearchResult `json:"results"`
}
