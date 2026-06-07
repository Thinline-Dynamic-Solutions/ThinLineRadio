// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Local prod-sample tone discover debug (skips if export dir missing).

package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func formatCandidates(cands []toneLearnCandidate) string {
	if len(cands) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		switch c.PatternType {
		case toneLearnPatternABPair:
			parts = append(parts, fmt.Sprintf("ab_pair A=%.1fHz/%.2fs B=%.1fHz/%.2fs", c.AFrequency, c.ADuration, c.BFrequency, c.BDuration))
		case toneLearnPatternLong:
			parts = append(parts, fmt.Sprintf("long %.1fHz/%.2fs", c.LongFrequency, c.LongDuration))
		default:
			parts = append(parts, string(c.PatternType))
		}
	}
	return strings.Join(parts, "; ")
}

func formatTonesSummary(tones []Tone) string {
	if len(tones) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tones))
	for _, t := range tones {
		parts = append(parts, fmt.Sprintf("%.1fHz/%.2fs", t.Frequency, t.Duration))
	}
	return strings.Join(parts, ", ")
}

func TestDiscoverProdLFDSpectralProbe(t *testing.T) {
	audioDir := "/tmp/tlr-debug/tlr-lfd-export/audio"
	if _, err := os.Stat(audioDir); err != nil {
		t.Skipf("missing %s: %v", audioDir, err)
	}

	detector := NewToneDetector()
	cfg := DefaultAutoLearnToneSetConfig()
	cfg.normalize()
	const systemId, talkgroupId uint64 = 44, 2764

	entries, err := os.ReadDir(audioDir)
	if err != nil {
		t.Fatal(err)
	}

	found := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".mp3") {
			continue
		}
		audio, err := os.ReadFile(filepath.Join(audioDir, ent.Name()))
		if err != nil {
			continue
		}
		tones, err := detector.Discover(audio, "audio/mpeg")
		if err != nil {
			t.Logf("%s discover err: %v", ent.Name(), err)
			continue
		}
		cands := extractToneLearnCandidates(tones, cfg, systemId, talkgroupId)
		if len(tones) == 0 {
			continue
		}
		found++
		t.Logf("%s tones=%d [%s] candidates=%d [%s]", ent.Name(), len(tones), formatTonesSummary(tones), len(cands), formatCandidates(cands))
	}
	t.Logf("files with tones: %d / %d", found, len(entries))

	// Aggregate candidates across all exported calls (mimics history analyze).
	type agg struct {
		cand toneLearnCandidate
		ids  []uint64
	}
	bySig := map[string]*agg{}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".mp3") {
			continue
		}
		callIdStr := strings.SplitN(ent.Name(), "_", 2)[0]
		var callId uint64
		fmt.Sscanf(callIdStr, "%d", &callId)
		audio, _ := os.ReadFile(filepath.Join(audioDir, ent.Name()))
		tones, err := detector.Discover(audio, "audio/mpeg")
		if err != nil || len(tones) == 0 {
			continue
		}
		for _, cand := range extractToneLearnCandidates(tones, cfg, systemId, talkgroupId) {
			a := bySig[cand.SignatureHash]
			if a == nil {
				bySig[cand.SignatureHash] = &agg{cand: cand}
				a = bySig[cand.SignatureHash]
			}
			dup := false
			for _, id := range a.ids {
				if id == callId {
					dup = true
					break
				}
			}
			if !dup {
				a.ids = append(a.ids, callId)
			}
		}
	}
	for sig, a := range bySig {
		t.Logf("signature %s… calls=%d pattern=%s", sig[:8], len(a.ids), toneLearnPatternDescription(a.cand))
	}
	t.Logf("unique ab/long patterns: %d (need %d calls each)", len(bySig), cfg.CallsRequired)
}

func TestDiscoverProdLFD(t *testing.T) {
	audioDir := "/tmp/tlr-debug/tlr-lfd-export/audio"
	csvPath := "/tmp/tlr-debug/tlr-lfd-export/calls_46038_last7d_top20.csv"
	if _, err := os.Stat(audioDir); err != nil {
		t.Skipf("missing %s: %v", audioDir, err)
	}

	// systemId 44, talkgroupId 2764 from export CSV (talkgroupRef 46038)
	const systemId, talkgroupId uint64 = 44, 2764

	filenameByBase := map[string]string{}
	if f, err := os.Open(csvPath); err == nil {
		defer f.Close()
		r := csv.NewReader(f)
		rows, err := r.ReadAll()
		if err == nil && len(rows) > 1 {
			for _, row := range rows[1:] {
				if len(row) < 7 {
					continue
				}
				callId := row[0]
				audioFilename := strings.TrimSpace(row[6])
				if audioFilename != "" {
					filenameByBase[callId] = audioFilename
				}
			}
		}
	}

	cfg := DefaultAutoLearnToneSetConfig()
	cfg.normalize()
	detector := NewToneDetector()

	entries, err := os.ReadDir(audioDir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	mimeVariants := []struct {
		label string
		mime  string
		from  string // "fixed" or "history"
	}{
		{label: "history(empty mime + csv filename)", mime: "", from: "history"},
		{label: "audio/mp4", mime: "audio/mp4", from: "fixed"},
		{label: "audio/mpeg", mime: "audio/mpeg", from: "fixed"},
		{label: "empty", mime: "", from: "fixed"},
	}

	t.Logf("cfg: A=[%.2f,%.2f]s B=[%.2f,%.2f]s long>=%.2fs tol=%.0fHz callsRequired=%d",
		cfg.AToneMinDuration, cfg.AToneMaxDuration,
		cfg.BToneMinDuration, cfg.BToneMaxDuration,
		cfg.LongToneMinDuration, cfg.FrequencyToleranceHz, cfg.CallsRequired)

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".mp3") {
			continue
		}
		path := filepath.Join(audioDir, ent.Name())
		audio, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", ent.Name(), err)
			continue
		}

		callId := strings.SplitN(ent.Name(), "_", 2)[0]
		csvFilename := filenameByBase[callId]
		if csvFilename == "" {
			csvFilename = ent.Name()
		}

		t.Logf("\n=== file=%s bytes=%d csvFilename=%q ===", ent.Name(), len(audio), csvFilename)

		for _, v := range mimeVariants {
			mime := v.mime
			if v.from == "history" {
				mime = toneHistoryAudioMime("", csvFilename)
			}
			tones, derr := detector.Discover(audio, mime)
			if derr != nil {
				t.Logf("  mime=%q (%s): DISCOVER ERR: %v", mime, v.label, derr)
				continue
			}
			cands := extractToneLearnCandidates(tones, cfg, systemId, talkgroupId)
			t.Logf("  mime=%q (%s): tones=%d [%s] candidates=%d [%s]",
				mime, v.label, len(tones), formatTonesSummary(tones), len(cands), formatCandidates(cands))
		}
	}
}

// learnedLFDToneSet is the A+B pair auto-learn found on 45383606 (Lordstown paging).
func learnedLFDToneSet() ToneSet {
	cfg := DefaultAutoLearnToneSetConfig()
	cfg.normalize()
	return ToneSet{
		Id:        "debug-lfd-learned",
		Label:     "LFD learned (1257/1124)",
		Tolerance: cfg.FrequencyToleranceHz,
		ATone: &ToneSpec{
			Frequency:   1257,
			MinDuration: cfg.AToneMinDuration,
			MaxDuration: cfg.AToneMaxDuration,
		},
		BTone: &ToneSpec{
			Frequency:   1124,
			MinDuration: cfg.BToneMinDuration,
			MaxDuration: cfg.BToneMaxDuration,
		},
	}
}

// TestProductionDetectLearnedLFD runs production Detect (not Discover) against exported
// LFD audio using the learned 1257/1124 Hz tone set — no production code changes.
func TestProductionDetectLearnedLFD(t *testing.T) {
	audioDir := "/tmp/tlr-debug/tlr-lfd-export/audio"
	if _, err := os.Stat(audioDir); err != nil {
		t.Skipf("missing %s: %v", audioDir, err)
	}

	toneSet := learnedLFDToneSet()
	detector := NewToneDetector()

	entries, err := os.ReadDir(audioDir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	matched := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".mp3") {
			continue
		}
		audio, err := os.ReadFile(filepath.Join(audioDir, ent.Name()))
		if err != nil {
			continue
		}
		seq, err := detector.Detect(audio, "audio/mpeg", []ToneSet{toneSet})
		if err != nil {
			t.Logf("%s Detect ERR: %v", ent.Name(), err)
			continue
		}
		hit := seq != nil && seq.HasTones && detector.MatchToneSet(seq, []ToneSet{toneSet}) != nil
		if hit {
			matched++
		}
		summary := "no match"
		if seq != nil && seq.HasTones {
			summary = formatTonesSummary(seq.Tones)
			if m := detector.MatchToneSet(seq, []ToneSet{toneSet}); m != nil {
				summary += " => MATCHED " + m.Label
			} else {
				summary += " => tones but no tone-set match"
			}
		} else {
			summary = "no tones (HasTones=false)"
		}
		t.Logf("%s: %s", ent.Name(), summary)
	}
	t.Logf("production Detect matched learned tone set: %d / %d files", matched, len(entries))
}
