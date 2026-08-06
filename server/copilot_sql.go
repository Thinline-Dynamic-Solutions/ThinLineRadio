// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var (
	copilotSQLBlocked = regexp.MustCompile(`(?i)\b(drop|truncate|alter|create|grant|revoke|copy|execute|call|into\s+outfile|load_file|pg_read_file|information_schema\.|pg_catalog\.)\b`)
	copilotSQLComment = regexp.MustCompile(`(?s)/\*.*?\*/|--.*?$`)
)

// Operational tables the assistant may touch. Prefer run_admin_action when an API exists.
var copilotSQLTableAllowlist = map[string]bool{
	"calls": true, "systems": true, "talkgroups": true, "units": true, "tags": true, "groups": true,
	"systemalerts": true, "logs": true, "users": true, "usergroups": true, "keywordlists": true,
	"callnatures": true, "dirwatches": true, "downstreams": true, "apikeys": true,
	"useralertpreferences": true, "devicetokens": true, "hallucinations": true,
	"unitaliassuggestions": true, "callnaturephrasesuggestions": true,
	"sites": true, "frequencies": true, "tonesets": true,
	"options": true, "rdioScannerSystems": true, "rdioscannersystems": true,
	"incidents": true, "incidentlocations": true, "boundaries": true,
}

type copilotSQLArgs struct {
	SQL       string `json:"sql"`
	Confirmed bool   `json:"confirmed"`
	Limit     int    `json:"limit"`
}

func (admin *Admin) copilotToolDBQuery(argsJSON string) (string, error) {
	var args copilotSQLArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	sqlText := strings.TrimSpace(args.SQL)
	if sqlText == "" {
		return "", fmt.Errorf("sql is required")
	}

	cleaned, err := copilotPrepareSQL(sqlText)
	if err != nil {
		admin.logCopilotSQL(sqlText, false, err.Error())
		b, _ := json.Marshal(map[string]any{"ok": false, "denied": true, "error": err.Error()})
		return string(b), nil
	}

	kind := copilotSQLKind(cleaned)
	if kind == "write" && !args.Confirmed {
		admin.logCopilotSQL(cleaned, false, "confirmation required")
		b, _ := json.Marshal(map[string]any{
			"ok":        false,
			"applied":   false,
			"needsConfirm": true,
			"error":     "confirmed must be true for INSERT/UPDATE/DELETE",
			"sql":       cleaned,
		})
		return string(b), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	db := admin.Controller.Database.Sql
	if db == nil {
		return "", fmt.Errorf("database unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch kind {
	case "select":
		if !strings.Contains(strings.ToLower(cleaned), " limit ") {
			cleaned = fmt.Sprintf("%s LIMIT %d", cleaned, limit)
		}
		rows, err := db.QueryContext(ctx, cleaned)
		if err != nil {
			admin.logCopilotSQL(cleaned, false, err.Error())
			b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
			return string(b), nil
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return "", err
		}
		results := make([]map[string]any, 0)
		for rows.Next() {
			if len(results) >= limit {
				break
			}
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			row := make(map[string]any, len(cols))
			for i, c := range cols {
				row[c] = copilotSQLValue(vals[i])
			}
			results = append(results, row)
		}
		admin.logCopilotSQL(cleaned, true, fmt.Sprintf("%d rows", len(results)))
		b, _ := json.Marshal(map[string]any{
			"ok":      true,
			"columns": cols,
			"rows":    results,
			"count":   len(results),
			"limit":   limit,
		})
		return string(b), nil

	case "write":
		res, err := db.ExecContext(ctx, cleaned)
		if err != nil {
			admin.logCopilotSQL(cleaned, false, err.Error())
			b, _ := json.Marshal(map[string]any{"ok": false, "applied": false, "error": err.Error()})
			return string(b), nil
		}
		affected, _ := res.RowsAffected()
		admin.logCopilotSQL(cleaned, true, fmt.Sprintf("%d affected", affected))
		b, _ := json.Marshal(map[string]any{
			"ok":           true,
			"applied":      true,
			"rowsAffected": affected,
		})
		return string(b), nil
	default:
		err := fmt.Errorf("unsupported statement type")
		admin.logCopilotSQL(cleaned, false, err.Error())
		b, _ := json.Marshal(map[string]any{"ok": false, "denied": true, "error": err.Error()})
		return string(b), nil
	}
}

func (admin *Admin) logCopilotSQL(sqlText string, ok bool, detail string) {
	preview := sqlText
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}
	level := LogLevelInfo
	if !ok {
		level = LogLevelWarn
	}
	admin.Controller.Logs.LogEvent(level, fmt.Sprintf("copilot db_query ok=%v detail=%s sql=%q", ok, detail, preview))
}

func copilotPrepareSQL(sqlText string) (string, error) {
	cleaned := copilotSQLComment.ReplaceAllString(sqlText, " ")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimRight(cleaned, ";")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", fmt.Errorf("empty sql")
	}
	// Reject multi-statement (semicolon outside simple check — already stripped trailing).
	if strings.Contains(cleaned, ";") {
		return "", fmt.Errorf("multi-statement SQL is not allowed")
	}
	if copilotSQLBlocked.MatchString(cleaned) {
		return "", fmt.Errorf("statement contains blocked keyword (DDL/admin functions are not allowed)")
	}
	kind := copilotSQLKind(cleaned)
	if kind == "" {
		return "", fmt.Errorf("only SELECT/WITH…SELECT and INSERT/UPDATE/DELETE are allowed")
	}
	tables := copilotSQLExtractTables(cleaned, kind)
	if len(tables) == 0 && kind == "write" {
		return "", fmt.Errorf("could not determine target table")
	}
	for _, t := range tables {
		norm := strings.ToLower(strings.Trim(t, `"'`))
		if !copilotSQLTableAllowlist[norm] {
			return "", fmt.Errorf("table %q is not on the allowlist; use run_admin_action or an existing admin tool", t)
		}
		if kind == "write" && (norm == "options" || norm == "apikeys" || norm == "users") {
			return "", fmt.Errorf("table %q is read-only via SQL; use apply_admin_change or run_admin_action", t)
		}
	}
	return cleaned, nil
}

func copilotSQLKind(sqlText string) string {
	s := strings.TrimSpace(sqlText)
	up := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(up, "SELECT"), strings.HasPrefix(up, "WITH"):
		return "select"
	case strings.HasPrefix(up, "INSERT"), strings.HasPrefix(up, "UPDATE"), strings.HasPrefix(up, "DELETE"):
		return "write"
	default:
		return ""
	}
}

func copilotSQLExtractTables(sqlText, kind string) []string {
	up := strings.ToUpper(sqlText)
	var tables []string
	switch kind {
	case "select":
		// FROM / JOIN identifiers
		re := regexp.MustCompile(`(?i)\b(?:from|join)\s+("?[a-zA-Z_][a-zA-Z0-9_]*"?)`)
		for _, m := range re.FindAllStringSubmatch(sqlText, -1) {
			if len(m) > 1 {
				tables = append(tables, m[1])
			}
		}
	case "write":
		switch {
		case strings.HasPrefix(up, "INSERT"):
			re := regexp.MustCompile(`(?i)\binto\s+("?[a-zA-Z_][a-zA-Z0-9_]*"?)`)
			if m := re.FindStringSubmatch(sqlText); len(m) > 1 {
				tables = append(tables, m[1])
			}
		case strings.HasPrefix(up, "UPDATE"):
			re := regexp.MustCompile(`(?i)\bupdate\s+("?[a-zA-Z_][a-zA-Z0-9_]*"?)`)
			if m := re.FindStringSubmatch(sqlText); len(m) > 1 {
				tables = append(tables, m[1])
			}
		case strings.HasPrefix(up, "DELETE"):
			re := regexp.MustCompile(`(?i)\bfrom\s+("?[a-zA-Z_][a-zA-Z0-9_]*"?)`)
			if m := re.FindStringSubmatch(sqlText); len(m) > 1 {
				tables = append(tables, m[1])
			}
		}
	}
	return tables
}

func copilotSQLValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		s := string(t)
		if looksSecretish(s) {
			return "[redacted]"
		}
		return s
	case string:
		if looksSecretish(t) {
			return "[redacted]"
		}
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case sql.NullString:
		if !t.Valid {
			return nil
		}
		return t.String
	case sql.NullInt64:
		if !t.Valid {
			return nil
		}
		return t.Int64
	case sql.NullFloat64:
		if !t.Valid {
			return nil
		}
		return t.Float64
	case sql.NullBool:
		if !t.Valid {
			return nil
		}
		return t.Bool
	default:
		return t
	}
}

func looksSecretish(s string) bool {
	if len(s) < 20 {
		return false
	}
	if strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") {
		return true
	}
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
	}
	return letters > 40 && !strings.Contains(s, " ")
}
