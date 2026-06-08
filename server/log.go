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
	Category string    `json:"category"`
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
	category := CategorizeLogMessage(message)

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
		writeLogStdout(message)
	}

	if logs.database != nil {
		l := Log{
			DateTime: time.Now().UTC(),
			Level:    level,
			Category: category,
			Message:  message,
		}

		query := `INSERT INTO "logs" ("level", "category", "message", "timestamp") VALUES ($1, $2, $3, $4)`
		if _, err := logs.database.Sql.Exec(query, l.Level, l.Category, l.Message, l.DateTime.UnixMilli()); err != nil {
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
		category  sql.NullString
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
		whereConditions = append(whereConditions, fmt.Sprintf(`"level" = '%s'`, escapeSQLString(v)))
	}

	// Category filter
	if cats := FilterLogCategories(searchOptions.Categories); len(cats) > 0 {
		quoted := make([]string, len(cats))
		for i, c := range cats {
			quoted[i] = "'" + escapeSQLString(c) + "'"
		}
		whereConditions = append(whereConditions, `"category" IN (`+strings.Join(quoted, ",")+`)`)
	}

	// Keyword / text search filter — case-insensitive substring match on the message.
	switch v := searchOptions.Search.(type) {
	case string:
		if v != "" {
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

	const maxSafeTimestampMs = int64(253402300800000)
	whereConditions = append(whereConditions, fmt.Sprintf(`"timestamp" > 0 AND "timestamp" < %d`, maxSafeTimestampMs))

	// Date filter
	switch v := searchOptions.Date.(type) {
	case time.Time:
		whereConditions = append(whereConditions, fmt.Sprintf(`"timestamp" >= %d`, v.UnixMilli()))
	default:
		if order == descOrder {
			defaultLookback := time.Now().Add(-24 * time.Hour)
			whereConditions = append(whereConditions, fmt.Sprintf(`"timestamp" >= %d`, defaultLookback.UnixMilli()))
		}
	}

	where := "TRUE"
	if len(whereConditions) > 0 {
		where = strings.Join(whereConditions, " AND ")
	}

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

	queryLimit := limit + 1

	query = fmt.Sprintf(`SELECT "logId", "level", "category", "message", "timestamp" FROM "logs" WHERE %s ORDER BY "timestamp" %s LIMIT %d OFFSET %d`, where, order, queryLimit, offset)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if rows, err = db.Sql.QueryContext(ctx, query); err != nil && err != sql.ErrNoRows {
		return nil, formatError(err, query)
	}
	defer rows.Close()

	var totalRows int

	for rows.Next() {
		totalRows++

		l := NewLog()

		if err = rows.Scan(&logId, &level, &category, &message, &timestamp); err != nil {
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

		if category.Valid && len(category.String) > 0 {
			l.Category = category.String
		} else {
			l.Category = CategorizeLogMessage(message.String)
		}

		if message.Valid && len(message.String) > 0 {
			l.Message = message.String
		} else {
			continue
		}

		if timestamp.Valid && timestamp.Int64 > 0 {
			t := time.UnixMilli(timestamp.Int64)
			if y := t.Year(); y < 1 || y > 9999 {
				continue
			}
			l.DateTime = t
		} else {
			continue
		}

		if uint(len(logResults.Logs)) < limit {
			logResults.Logs = append(logResults.Logs, *l)
		}
	}

	if err != nil {
		return nil, formatError(err, "")
	}

	logResults.HasMore = totalRows > int(limit)

	if logResults.HasMore {
		logResults.Count = uint64(offset) + uint64(len(logResults.Logs)) + 1
	} else {
		logResults.Count = uint64(offset) + uint64(len(logResults.Logs))
	}

	return logResults, nil
}

func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

func (logs *Logs) setDaemon(d *Daemon) {
	logs.daemon = d
}

func (logs *Logs) setDatabase(d *Database) {
	logs.database = d
}

type LogsSearchOptions struct {
	Categories []string `json:"categories,omitempty"`
	Date       any      `json:"date,omitempty"`
	Level      any      `json:"level,omitempty"`
	Limit      any      `json:"limit,omitempty"`
	Offset     any      `json:"offset,omitempty"`
	Search     any      `json:"search,omitempty"`
	Sort       any      `json:"sort,omitempty"`
}

func NewLogSearchOptions() *LogsSearchOptions {
	return &LogsSearchOptions{}
}

func (searchOptions *LogsSearchOptions) FromMap(m map[string]any) *LogsSearchOptions {
	switch v := m["categories"].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				searchOptions.Categories = append(searchOptions.Categories, s)
			}
		}
	case []string:
		searchOptions.Categories = append(searchOptions.Categories, v...)
	}

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

type LogCategoryInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

func AllLogCategoryInfo() []LogCategoryInfo {
	out := make([]LogCategoryInfo, len(AllLogCategories))
	for i, key := range AllLogCategories {
		out[i] = LogCategoryInfo{
			Key:   key,
			Label: LogCategoryLabels[key],
		}
	}
	return out
}

// LogCategoryInfoForAdmin returns categories for the admin UI, omitting Central
// Management when the server is not paired with CM.
func LogCategoryInfoForAdmin(centralManagementEnabled bool) []LogCategoryInfo {
	all := AllLogCategoryInfo()
	if centralManagementEnabled {
		return all
	}
	out := make([]LogCategoryInfo, 0, len(all))
	for _, c := range all {
		if c.Key != LogCategoryCentral {
			out = append(out, c)
		}
	}
	return out
}
