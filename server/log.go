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
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

type Log struct {
	Id       any       `json:"id"`
	DateTime time.Time `json:"dateTime"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
}

func NewLog() *Log {
	return &Log{}
}

type Logs struct {
	database *Database
	mutex    sync.Mutex
	daemon   *Daemon
}

func NewLogs() *Logs {
	return &Logs{
		mutex: sync.Mutex{},
	}
}

func (logs *Logs) LogEvent(level string, message string) error {
	logs.mutex.Lock()
	defer logs.mutex.Unlock()

	if logs.daemon != nil {
		switch level {
		case LogLevelError:
			logs.daemon.Logger.Error(message)
		case LogLevelWarn:
			logs.daemon.Logger.Warning(message)
		case LogLevelInfo:
			logs.daemon.Logger.Info(message)
		}

	} else {
		log.Println(message)
	}

	if logs.database != nil {
		l := Log{
			DateTime: time.Now().UTC(),
			Level:    level,
			Message:  message,
		}

		query := fmt.Sprintf(`INSERT INTO "logs" ("level", "message", "timestamp") VALUES ('%s', '%s', %d)`, l.Level, l.Message, l.DateTime.UnixMilli())
		if _, err := logs.database.Sql.Exec(query); err != nil {
			return fmt.Errorf("logs.logevent: %s in %s", err, query)
		}
	}

	return nil
}

func (logs *Logs) Prune(db *Database, pruneDays uint) error {
	logs.mutex.Lock()
	defer logs.mutex.Unlock()

	timestamp := time.Now().Add(-24 * time.Hour * time.Duration(pruneDays)).UnixMilli()
	query := fmt.Sprintf(`DELETE FROM "logs" WHERE "timestamp" < %d`, timestamp)

	if _, err := db.Sql.Exec(query); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (logs *Logs) PurgeAll(db *Database) error {
	logs.mutex.Lock()
	defer logs.mutex.Unlock()

	query := `DELETE FROM "logs"`

	if _, err := db.Sql.Exec(query); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (logs *Logs) DeleteByIDs(db *Database, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}

	logs.mutex.Lock()
	defer logs.mutex.Unlock()

	var placeholders []string
	var args []interface{}
	for i, id := range ids {
		if db.Config.DbType == DbTypePostgresql {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		} else {
			placeholders = append(placeholders, "?")
		}
		args = append(args, id)
	}

	query := fmt.Sprintf(`DELETE FROM "logs" WHERE "logId" IN (%s)`, strings.Join(placeholders, ", "))

	if _, err := db.Sql.Exec(query, args...); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (logs *Logs) Search(searchOptions *LogsSearchOptions, db *Database) (*LogsSearchResults, error) {
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

		whereConditions []string

		level     sql.NullString
		logId     sql.NullInt64
		message   sql.NullString
		timestamp sql.NullInt64
	)

	logs.mutex.Lock()
	defer logs.mutex.Unlock()

	formatError := errorFormatter("logs", "search")

	logResults := &LogsSearchResults{
		Options: searchOptions,
		Logs:    []Log{},
		// DateStart/DateStop are omitted (zero value) to avoid expensive MIN/MAX full-table scans.
		// The date picker in the UI will simply have no enforced min/max boundary.
	}

	// Level filter
	switch v := searchOptions.Level.(type) {
	case string:
		whereConditions = append(whereConditions, fmt.Sprintf(`"level" = '%s'`, v))
	}

	// Keyword / text search filter — case-insensitive substring match on the message.
	// PostgreSQL ILIKE is safe here because the timestamp window already limits the
	// scan to a small fraction of the table via the logs_timestamp_idx index.
	switch v := searchOptions.Search.(type) {
	case string:
		if v != "" {
			// Escape SQL wildcards in the user's term so they are treated as literals
			escaped := strings.ReplaceAll(v, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `%`, `\%`)
			escaped = strings.ReplaceAll(escaped, `_`, `\_`)
			whereConditions = append(whereConditions, fmt.Sprintf(`"message" ILIKE '%%%s%%' ESCAPE '\'`, escaped))
		}
	}

	// Sort order
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

	// Hard-clamp timestamps to the range that time.Time.MarshalJSON accepts (years 0–9999).
	// Rows outside this range have corrupt/wrong-unit timestamps and cannot be serialised;
	// filtering them in SQL avoids a json.Marshal failure that causes HTTP 417.
	const maxSafeTimestampMs = int64(253402300800000) // 9999-12-31 23:59:59 UTC in ms
	whereConditions = append(whereConditions, fmt.Sprintf(`"timestamp" > 0 AND "timestamp" < %d`, maxSafeTimestampMs))

	// Date filter
	switch v := searchOptions.Date.(type) {
	case time.Time:
		// When the user picks a specific date, show logs from that point forward (>=).
		// Sort order (ASC/DESC) controls oldest-first vs newest-first within the window.
		whereConditions = append(whereConditions, fmt.Sprintf(`"timestamp" >= %d`, v.UnixMilli()))
	default:
		// No date selected — apply a 24-hour lookback for DESC (newest-first) searches
		// to avoid a full table scan on tables with millions of rows.
		// ASC (oldest-first) has no default restriction so the user can still browse history.
		if order == descOrder {
			defaultLookback := time.Now().Add(-24 * time.Hour)
			whereConditions = append(whereConditions, fmt.Sprintf(`"timestamp" >= %d`, defaultLookback.UnixMilli()))
		}
	}

	// Build WHERE clause
	where := "TRUE"
	if len(whereConditions) > 0 {
		where = strings.Join(whereConditions, " AND ")
	}

	// Limit / offset
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

	// Fetch limit+1 rows so we can detect whether there are more pages without COUNT(*)
	queryLimit := limit + 1

	query = fmt.Sprintf(`SELECT "logId", "level", "message", "timestamp" FROM "logs" WHERE %s ORDER BY "timestamp" %s LIMIT %d OFFSET %d`, where, order, queryLimit, offset)

	// 30-second timeout matches the calls search timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if rows, err = db.Sql.QueryContext(ctx, query); err != nil && err != sql.ErrNoRows {
		return nil, formatError(err, query)
	}

	var totalRows int

	for rows.Next() {
		totalRows++

		l := NewLog()

		if err = rows.Scan(&logId, &level, &message, &timestamp); err != nil {
			continue
		}

		if logId.Valid {
			l.Id = uint64(logId.Int64)
		} else {
			continue
		}

		if level.Valid && len(level.String) > 0 {
			l.Level = level.String
		} else {
			continue
		}

		if message.Valid && len(message.String) > 0 {
			l.Message = message.String
		} else {
			continue
		}

		if timestamp.Valid && timestamp.Int64 > 0 {
			t := time.UnixMilli(timestamp.Int64)
			// Skip rows whose converted time falls outside the year range that
			// time.Time.MarshalJSON accepts (0–9999). Such rows have corrupt or
			// wrong-unit timestamps; marshalling them would return an error and
			// cause the handler to respond with HTTP 417.
			if y := t.Year(); y < 1 || y > 9999 {
				continue
			}
			l.DateTime = t
		} else {
			continue
		}

		// Only keep up to limit rows; the extra row just tells us HasMore
		if uint(len(logResults.Logs)) < limit {
			logResults.Logs = append(logResults.Logs, *l)
		}
	}

	rows.Close()

	if err != nil {
		return nil, formatError(err, "")
	}

	logResults.HasMore = totalRows > int(limit)

	// Expose an approximate total that keeps the paginator working:
	//   - exact when on the last page (HasMore == false)
	//   - offset + results + 1 when more pages exist, so the paginator shows a next-page button
	if logResults.HasMore {
		logResults.Count = uint64(offset) + uint64(len(logResults.Logs)) + 1
	} else {
		logResults.Count = uint64(offset) + uint64(len(logResults.Logs))
	}

	return logResults, nil
}

func (logs *Logs) setDaemon(d *Daemon) {
	logs.daemon = d
}

func (logs *Logs) setDatabase(d *Database) {
	logs.database = d
}

type LogsSearchOptions struct {
	Date   any `json:"date,omitempty"`
	Level  any `json:"level,omitempty"`
	Limit  any `json:"limit,omitempty"`
	Offset any `json:"offset,omitempty"`
	Search any `json:"search,omitempty"`
	Sort   any `json:"sort,omitempty"`
}

func NewLogSearchOptions() *LogsSearchOptions {
	return &LogsSearchOptions{}
}

func (searchOptions *LogsSearchOptions) FromMap(m map[string]any) *LogsSearchOptions {
	switch v := m["date"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			searchOptions.Date = t
		}
	}

	switch v := m["level"].(type) {
	case string:
		searchOptions.Level = v
	}

	switch v := m["limit"].(type) {
	case float64:
		searchOptions.Limit = uint(v)
	}

	switch v := m["offset"].(type) {
	case float64:
		searchOptions.Offset = uint(v)
	}

	switch v := m["search"].(type) {
	case string:
		searchOptions.Search = v
	}

	switch v := m["sort"].(type) {
	case float64:
		searchOptions.Sort = int(v)
	}

	return searchOptions
}

type LogsSearchResults struct {
	Count     uint64             `json:"count"`
	HasMore   bool               `json:"hasMore"`
	DateStart time.Time          `json:"dateStart"`
	DateStop  time.Time          `json:"dateStop"`
	Options   *LogsSearchOptions `json:"options"`
	Logs      []Log              `json:"logs"`
}
