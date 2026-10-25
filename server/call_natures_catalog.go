// Copyright (C) 2025-2026 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	callNatureCatalogMinRows = 20
	callNatureCatalogMaxBody = 8 << 20
)

// remoteCallNatureCatalog is GET /call-natures on the geocode server.
type remoteCallNatureCatalog struct {
	Name    string             `json:"name"`
	Version string             `json:"version"`
	Count   int                `json:"count"`
	Natures []remoteCallNature `json:"natures"`
}

type remoteCallNature struct {
	Label         string   `json:"label"`
	Phrases       []string `json:"phrases"`
	Enabled       bool     `json:"enabled"`
	Order         int      `json:"order"`
	ExpireMinutes int      `json:"expireMinutes"`
}

type callNatureCatalogSyncResult struct {
	Version   string `json:"version"`
	Added     int    `json:"added"`
	Updated   int    `json:"updated"`
	Removed   int    `json:"removed"`
	KeptLocal int    `json:"keptLocal"`
}

func parseCallNatureCatalog(body []byte) (remoteCallNatureCatalog, error) {
	var doc remoteCallNatureCatalog
	if err := json.Unmarshal(body, &doc); err != nil {
		return doc, fmt.Errorf("call classification database was not valid JSON")
	}
	if len(doc.Natures) < callNatureCatalogMinRows {
		return doc, fmt.Errorf("call classification database looks incomplete")
	}
	return doc, nil
}

func (controller *Controller) fetchCallNatureCatalog() (remoteCallNatureCatalog, error) {
	var doc remoteCallNatureCatalog
	if controller == nil {
		return doc, fmt.Errorf("server is not ready")
	}
	base := strings.TrimRight(strings.TrimSpace(controller.NominatimGatewayURLSnapshot()), "/")
	if base == "" {
		return doc, fmt.Errorf("geocode server address is not known yet")
	}
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return doc, fmt.Errorf("geocode server address is not a URL")
	}
	req, err := http.NewRequest(http.MethodGet, base+"/call-natures", nil)
	if err != nil {
		return doc, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return doc, fmt.Errorf("could not reach the geocode server")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, callNatureCatalogMaxBody))
	if err != nil {
		return doc, fmt.Errorf("could not read the call classification database")
	}
	if resp.StatusCode != http.StatusOK {
		return doc, fmt.Errorf("geocode server returned %d for the call classification database", resp.StatusCode)
	}
	return parseCallNatureCatalog(body)
}

// applyCallNatureCatalog upserts shared categories and leaves local ones alone.
// Expire minutes already set on this server are kept. Phrase edits on a shared
// category are replaced by the downloaded database.
func applyCallNatureCatalog(db *Database, doc remoteCallNatureCatalog) (callNatureCatalogSyncResult, error) {
	var result callNatureCatalogSyncResult
	result.Version = doc.Version
	if db == nil || db.Sql == nil {
		return result, fmt.Errorf("database unavailable")
	}
	existing := map[string]struct{}{}
	rows, err := db.Sql.Query(`SELECT "label" FROM "callNatures"`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			rows.Close()
			return result, err
		}
		label = strings.ToUpper(strings.TrimSpace(label))
		if label != "" {
			existing[label] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	tx, err := db.Sql.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	labels := make([]string, 0, len(doc.Natures))
	seen := map[string]bool{}
	for _, n := range doc.Natures {
		label := strings.ToUpper(strings.TrimSpace(n.Label))
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
		phrases := sanitizeCallNaturePhrases(n.Phrases)
		if len(phrases) == 0 {
			phrases = []string{label}
		}
		phrasesJSON, _ := json.Marshal(phrases)
		expireMin := n.ExpireMinutes
		if expireMin < 0 {
			expireMin = 0
		}
		expire := clampCallNatureExpireMinutes(uint(expireMin))
		if _, ok := existing[label]; ok {
			result.Updated++
			_, err = tx.Exec(`UPDATE "callNatures"
				SET "phrases" = $1, "enabled" = $2, "order" = $3, "source" = 'catalog'
				WHERE "label" = $4`,
				string(phrasesJSON), n.Enabled, n.Order, label)
		} else {
			result.Added++
			_, err = tx.Exec(`INSERT INTO "callNatures"
				("label", "phrases", "enabled", "order", "expireMinutes", "createdAt", "source")
				VALUES ($1, $2, $3, $4, $5, $6, 'catalog')`,
				label, string(phrasesJSON), n.Enabled, n.Order, expire, now)
		}
		if err != nil {
			return result, err
		}
	}
	if len(labels) == 0 {
		return result, fmt.Errorf("call classification database had no categories")
	}
	res, err := tx.Exec(`DELETE FROM "callNatures" WHERE "source" = 'catalog' AND NOT ("label" = ANY($1))`, pq.Array(labels))
	if err != nil {
		return result, err
	}
	if n, err := res.RowsAffected(); err == nil {
		result.Removed = int(n)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM "callNatures" WHERE "source" = 'local'`).Scan(&result.KeptLocal); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func clearCallNatures(db *Database) error {
	if db == nil || db.Sql == nil {
		return fmt.Errorf("database unavailable")
	}
	_, err := db.Sql.Exec(`DELETE FROM "callNatures"`)
	return err
}
