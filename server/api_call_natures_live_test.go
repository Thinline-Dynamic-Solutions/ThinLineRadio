//go:build integration

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"rdio-scanner/server/mapping"

	_ "github.com/lib/pq"
)

// Live DB + handler path test. Run with:
//
//	go test -tags=integration -run TestLiveCallNaturePhraseSave -count=1 -v
//
// Requires Postgres (defaults: db tlr, user postgres, password postgres).
func TestLiveCallNaturePhraseSave(t *testing.T) {
	host := envOr("TLR_DB_HOST", "localhost")
	port := envOr("TLR_DB_PORT", "5432")
	name := envOr("TLR_DB_NAME", "tlr")
	user := envOr("TLR_DB_USER", "postgres")
	pass := envOr("TLR_DB_PASS", "postgres")

	db, dbName, err := openPostgres(host, port, user, pass, name)
	if err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	defer db.Close()
	t.Logf("connected to database %q", dbName)

	var exists bool
	err = db.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_name = 'callNatures'
	)`).Scan(&exists)
	if err != nil || !exists {
		t.Skipf("callNatures table missing: %v", err)
	}

	label := fmt.Sprintf("E2E TEST %d", time.Now().UnixNano()%100000)
	phrases := []string{
		label,
		"UNIT ON SCENE",
		"PERSON WITH A GUN IN THE PARKING LOT",
		"ALARM DROP.",
		"FIRE STATION RESPONSE",
	}
	sanitized := sanitizeCallNaturePhrases(phrases)
	if len(sanitized) != len(phrases) {
		t.Fatalf("sanitize dropped phrases before insert: got %v", sanitized)
	}
	pj, _ := json.Marshal(sanitized)

	var id int64
	err = db.QueryRow(
		`INSERT INTO "callNatures" ("label", "phrases", "enabled", "order", "expireMinutes", "createdAt")
		 VALUES ($1, $2, true, 0, 0, $3) RETURNING "callNatureId"`,
		label, string(pj), time.Now().UnixMilli(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM "callNatures" WHERE "callNatureId" = $1`, id)
	})

	var raw string
	err = db.QueryRow(`SELECT "phrases" FROM "callNatures" WHERE "callNatureId" = $1`, id).Scan(&raw)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	var stored []string
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("unmarshal phrases %q: %v", raw, err)
	}
	for _, want := range phrases {
		found := false
		for _, s := range stored {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("phrase %q missing from DB after save; stored=%v", want, stored)
		}
	}

	bodyMap := map[string]any{
		"label":   label,
		"phrases": append(append([]string{}, stored...), "GUNSHOTS HEARD", "PT COMPLAINING OF CHEST PAIN"),
	}
	bodyBytes, _ := json.Marshal(bodyMap)
	var decoded map[string]any
	_ = json.Unmarshal(bodyBytes, &decoded)
	updated := sanitizeCallNaturePhrases(stringsFromAnySlice(decoded["phrases"]))
	wantExtra := []string{"GUNSHOTS HEARD", "PT COMPLAINING OF CHEST PAIN"}
	for _, w := range wantExtra {
		ok := false
		for _, u := range updated {
			if u == w {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("update sanitize lost %q; got %v", w, updated)
		}
	}
	uj, _ := json.Marshal(updated)
	_, err = db.Exec(
		`UPDATE "callNatures" SET "phrases" = $1 WHERE "callNatureId" = $2`,
		string(uj), id,
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	err = db.QueryRow(`SELECT "phrases" FROM "callNatures" WHERE "callNatureId" = $1`, id).Scan(&raw)
	if err != nil {
		t.Fatalf("reselect: %v", err)
	}
	stored = nil
	_ = json.Unmarshal([]byte(raw), &stored)
	for _, w := range wantExtra {
		found := false
		for _, s := range stored {
			if s == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("after update, %q missing; stored=%v", w, stored)
		}
	}

	var keptByOld int
	for _, p := range stored {
		if mapping.IsAcceptableCallNaturePhrase(p) {
			keptByOld++
		}
	}
	if keptByOld >= len(stored) {
		t.Fatalf("expected old mining filter to drop some of %v", stored)
	}
	t.Logf("live DB save OK id=%d phrases=%d (old filter would keep %d)", id, len(stored), keptByOld)

	req := httptest.NewRequest(http.MethodPut, "/api/call-natures/1", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	b, _ := io.ReadAll(req.Body)
	var again map[string]any
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatal(err)
	}
	final := sanitizeCallNaturePhrases(stringsFromAnySlice(again["phrases"]))
	if len(final) < 4 {
		t.Fatalf("handler decode path lost phrases: %v", final)
	}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func openPostgres(host, port, user, pass, preferred string) (*sql.DB, string, error) {
	try := []string{preferred, "tlr", "thinline_radio", "rdio_scanner", "postgres"}
	seen := map[string]bool{}
	var last error
	for _, name := range try {
		if seen[name] {
			continue
		}
		seen[name] = true
		dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, pass, name)
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			last = err
			continue
		}
		if err := db.Ping(); err != nil {
			last = err
			db.Close()
			continue
		}
		return db, name, nil
	}
	// Also try empty password
	dsn := fmt.Sprintf("host=%s port=%s user=%s password= dbname=%s sslmode=disable", host, port, user, preferred)
	db, err := sql.Open("postgres", dsn)
	if err == nil && db.Ping() == nil {
		return db, preferred, nil
	}
	if db != nil {
		db.Close()
	}
	if last == nil {
		last = err
	}
	return nil, "", last
}
