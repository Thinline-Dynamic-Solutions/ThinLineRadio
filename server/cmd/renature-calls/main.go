// Re-run call-nature classification for recent calls carrying one label.
// Run on the server host next to thinline-radio.ini; dry run unless -apply.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/ini.v1"

	"rdio-scanner/server/mapping"
)

type pendingCall struct {
	id         int64
	transcript string
	newNature  string
}

func main() {
	iniPath := flag.String("ini", "thinline-radio.ini", "database config ini")
	label := flag.String("label", "FOLLOW UP", "incident nature to re-review")
	hours := flag.Int("hours", 48, "look back this many hours")
	workers := flag.Int("workers", 6, "parallel OpenAI requests")
	apply := flag.Bool("apply", false, "write new natures (default is a dry run)")
	limit := flag.Int("limit", 0, "only re-review this many of the newest calls (0 = all)")
	flag.Parse()

	cfg, err := ini.Load(*iniPath)
	if err != nil {
		fatalf("load ini %s: %v", *iniPath, err)
	}
	sec := cfg.Section("")
	db, err := sql.Open("pgx", fmt.Sprintf(
		"postgresql://%s:%s@%s:%d/%s",
		sec.Key("db_user").String(),
		sec.Key("db_pass").String(),
		sec.Key("db_host").String(),
		sec.Key("db_port").MustInt(5432),
		sec.Key("db_name").String(),
	))
	if err != nil {
		fatalf("db open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fatalf("db ping: %v", err)
	}

	in := loadProcessInput(db)
	fmt.Printf("labels=%d terms=%d openai_classify=%v model=%s\n",
		len(in.NatureCodes), len(in.NatureMatchTerms), in.CallNatureOpenAIClassify, in.OpenAIModel)

	since := time.Now().Add(-time.Duration(*hours) * time.Hour).UnixMilli()
	rows, err := db.Query(`SELECT "callId", COALESCE("transcript", '') FROM "calls"
		WHERE "incidentNature" = $1 AND "timestamp" > $2 ORDER BY "callId"`, strings.ToUpper(*label), since)
	if err != nil {
		fatalf("select calls: %v", err)
	}
	var calls []*pendingCall
	for rows.Next() {
		c := &pendingCall{}
		if rows.Scan(&c.id, &c.transcript) == nil {
			calls = append(calls, c)
		}
	}
	rows.Close()
	fmt.Printf("calls labeled %q in last %dh: %d\n", *label, *hours, len(calls))
	if *limit > 0 && len(calls) > *limit {
		calls = calls[len(calls)-*limit:]
		fmt.Printf("re-reviewing newest %d\n", len(calls))
	}

	jobs := make(chan *pendingCall)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				c.newNature = strings.ToUpper(strings.TrimSpace(mapping.ResolveIncidentNature(c.transcript, in)))
			}
		}()
	}
	for _, c := range calls {
		jobs <- c
	}
	close(jobs)
	wg.Wait()

	counts := map[string]int{}
	for _, c := range calls {
		key := c.newNature
		if key == "" {
			key = "(cleared)"
		}
		counts[key]++
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range counts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	fmt.Println("new natures:")
	for _, e := range sorted {
		fmt.Printf("%6d %s\n", e.v, e.k)
	}
	fmt.Println("samples:")
	shown := 0
	for _, c := range calls {
		if shown >= 30 || c.newNature == strings.ToUpper(*label) {
			continue
		}
		shown++
		tx := c.transcript
		if len(tx) > 140 {
			tx = tx[:140]
		}
		nn := c.newNature
		if nn == "" {
			nn = "(cleared)"
		}
		fmt.Printf("%d -> %s | %s\n", c.id, nn, tx)
	}

	if !*apply {
		fmt.Println("dry run: no changes written")
		return
	}
	now := time.Now().UnixMilli()
	updated := 0
	for _, c := range calls {
		if c.newNature == strings.ToUpper(*label) {
			continue
		}
		if _, err := db.Exec(`UPDATE "calls" SET "incidentNature" = $1, "incidentNatureReviewedAt" = $2
			WHERE "callId" = $3 AND "incidentNature" = $4`, c.newNature, now, c.id, strings.ToUpper(*label)); err != nil {
			fatalf("update call %d: %v", c.id, err)
		}
		updated++
	}
	fmt.Printf("updated %d calls\n", updated)
}

func loadProcessInput(db *sql.DB) mapping.ProcessInput {
	in := mapping.ProcessInput{NaturePhraseToLabel: map[string]string{}}
	rows, err := db.Query(`SELECT "label", "phrases" FROM "callNatures" WHERE "enabled" ORDER BY "order", "label"`)
	if err != nil {
		fatalf("load call natures: %v", err)
	}
	seen := map[string]bool{}
	addTerm := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			in.NatureMatchTerms = append(in.NatureMatchTerms, t)
		}
	}
	for rows.Next() {
		var label, phrasesJSON string
		if rows.Scan(&label, &phrasesJSON) != nil {
			continue
		}
		label = strings.ToUpper(strings.TrimSpace(label))
		if label == "" {
			continue
		}
		in.NatureCodes = append(in.NatureCodes, label)
		in.NaturePhraseToLabel[label] = label
		addTerm(label)
		var phrases []string
		_ = json.Unmarshal([]byte(phrasesJSON), &phrases)
		for _, p := range phrases {
			p = strings.ToUpper(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			in.NaturePhraseToLabel[p] = label
			addTerm(p)
		}
	}
	rows.Close()
	sort.Strings(in.NatureCodes)

	var raw string
	if db.QueryRow(`SELECT "value" FROM "options" WHERE "key"='openAIIntegration'`).Scan(&raw) == nil {
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) == nil {
			in.OpenAIKey, _ = m["apiKey"].(string)
			in.OpenAIModel, _ = m["model"].(string)
		}
	}
	in.OpenAIKey = strings.TrimSpace(in.OpenAIKey)
	hasKey := in.OpenAIKey != ""
	classify := hasKey
	if db.QueryRow(`SELECT "value" FROM "options" WHERE "key"='mappingIntegration'`).Scan(&raw) == nil && raw != "" {
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) == nil {
			classify = false
			if v, ok := m["callNatureOpenAIClassify"].(bool); ok {
				classify = v
			}
			if !classify {
				if eng, ok := m["mappingEngine"].(string); ok && strings.EqualFold(eng, "local") {
					classify = hasKey
				}
			}
			if in.OpenAIModel == "" {
				in.OpenAIModel, _ = m["openAIModel"].(string)
			}
		}
	}
	in.CallNatureOpenAIClassify = classify && hasKey
	return in
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
