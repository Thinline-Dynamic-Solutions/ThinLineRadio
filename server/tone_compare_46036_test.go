// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Direct Detect on exported audio using production tone-sets.json.
// Compares tone frequencies (Hz/duration) — not department labels.

package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const toneCompareFreqTolHz = 20.0
const toneCompareDurTolSec = 0.5
const toneCompareMinDur = 0.6

type compareCallRow struct {
	callId    string
	hasTones  bool
	seqJSON   string
	audioPath string
}

func load46036Export(t *testing.T) ([]compareCallRow, []ToneSet) {
	t.Helper()
	base := "/tmp/tlr-debug/tg-46036-3d"
	if _, err := os.Stat(filepath.Join(base, "calls.csv")); err != nil {
		t.Skipf("missing export: %v", err)
	}
	toneSetsJSON, _ := os.ReadFile(filepath.Join(base, "tone-sets.json"))
	toneSets, err := ParseToneSets(string(toneSetsJSON))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(filepath.Join(base, "calls.csv"))
	defer f.Close()
	rows, _ := csv.NewReader(f).ReadAll()
	var calls []compareCallRow
	for i, row := range rows {
		if i == 0 || len(row) < 8 {
			continue
		}
		calls = append(calls, compareCallRow{
			callId:    row[0],
			hasTones:  strings.EqualFold(strings.TrimSpace(row[2]), "t"),
			seqJSON:   row[3],
			audioPath: filepath.Join(base, "audio", row[0]+"_"+row[6]),
		})
	}
	return calls, toneSets
}

func parseProdTones(seqJSON string) []Tone {
	seqJSON = strings.TrimSpace(seqJSON)
	if seqJSON == "" || seqJSON == "{}" {
		return nil
	}
	var seq ToneSequence
	if err := json.Unmarshal([]byte(seqJSON), &seq); err != nil {
		return nil
	}
	var out []Tone
	for _, t := range seq.Tones {
		if t.Duration >= toneCompareMinDur {
			out = append(out, t)
		}
	}
	return out
}

func sigTones(seq *ToneSequence) []Tone {
	if seq == nil {
		return nil
	}
	var out []Tone
	for _, t := range seq.Tones {
		if t.Duration >= toneCompareMinDur {
			out = append(out, t)
		}
	}
	return out
}

func toneMatches(a, b Tone) bool {
	return math.Abs(a.Frequency-b.Frequency) <= toneCompareFreqTolHz &&
		math.Abs(a.Duration-b.Duration) <= toneCompareDurTolSec
}

func prodTonesCovered(prod, local []Tone) (bool, []string) {
	var missing []string
	for _, p := range prod {
		ok := false
		for _, l := range local {
			if toneMatches(p, l) {
				ok = true
				break
			}
		}
		if !ok {
			missing = append(missing, fmt.Sprintf("%.0fHz/%.2fs", p.Frequency, p.Duration))
		}
	}
	return len(missing) == 0, missing
}

// TestCompareProd46036DirectDetect: production tone sets + each call's audio, direct Detect only.
func TestCompareProd46036DirectDetect(t *testing.T) {
	calls, toneSets := load46036Export(t)
	t.Logf("using %d production tone sets", len(toneSets))

	detector := NewToneDetector()
	oldStdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = oldStdout }()

	var total, prodToneCalls, agreeNo, agreeTones, miss, extra int

	for _, call := range calls {
		total++
		prod := parseProdTones(call.seqJSON)
		audio, err := os.ReadFile(call.audioPath)
		if err != nil {
			continue
		}
		seq, err := detector.Detect(audio, "audio/mpeg", toneSets)
		if err != nil {
			continue
		}
		local := sigTones(seq)

		if len(prod) > 0 {
			prodToneCalls++
			if ok, missList := prodTonesCovered(prod, local); ok {
				agreeTones++
			} else {
				miss++
				if miss <= 8 {
					t.Logf("MISS %s prod=[%s] local=[%s] gap=%v",
						call.callId, formatTonesSummary(prod), formatTonesSummary(local), missList)
				}
			}
		} else if len(local) == 0 {
			agreeNo++
		} else {
			extra++
			if extra <= 5 {
				t.Logf("EXTRA %s local=[%s]", call.callId, formatTonesSummary(local))
			}
		}
	}

	os.Stdout = oldStdout
	t.Logf("calls=%d prodToneCalls=%d agreeNo=%d agreeTones=%d miss=%d extra=%d",
		total, prodToneCalls, agreeNo, agreeTones, miss, extra)
}

// TestLFDDetectTones: 78 LRDS FD export — Discover finds paging tones; Detect uses learned A/B Hz.
func TestLFDDetectTones(t *testing.T) {
	audioDir := "/tmp/tlr-debug/tlr-lfd-export/audio"
	if _, err := os.Stat(audioDir); err != nil {
		t.Skipf("missing %s", audioDir)
	}

	const wantA, wantB = 1257.0, 1124.0
	learned := learnedLFDToneSet()
	detector := NewToneDetector()
	cfg := DefaultAutoLearnToneSetConfig()
	cfg.normalize()

	oldStdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = oldStdout }()

	entries, _ := os.ReadDir(audioDir)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var discoverHit, detectHit int
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".mp3") {
			continue
		}
		audio, err := os.ReadFile(filepath.Join(audioDir, ent.Name()))
		if err != nil {
			continue
		}

		// Auto-learn path (Discover)
		tones, _ := detector.Discover(audio, "audio/mpeg")
		for _, cand := range extractToneLearnCandidates(tones, cfg, 44, 2764) {
			if cand.PatternType == toneLearnPatternABPair &&
				math.Abs(cand.AFrequency-wantA) <= 15 &&
				math.Abs(cand.BFrequency-wantB) <= 15 {
				discoverHit++
				t.Logf("Discover %s: A=%.0fHz B=%.0fHz", ent.Name(), cand.AFrequency, cand.BFrequency)
				break
			}
		}

		// Production Detect path (once tone set is configured)
		seq, _ := detector.Detect(audio, "audio/mpeg", []ToneSet{learned})
		if seq != nil && seq.HasTones {
			local := sigTones(seq)
			hasA, hasB := false, false
			for _, t := range local {
				if math.Abs(t.Frequency-wantA) <= 15 {
					hasA = true
				}
				if math.Abs(t.Frequency-wantB) <= 15 {
					hasB = true
				}
			}
			if hasA && hasB {
				detectHit++
				t.Logf("Detect %s: [%s]", ent.Name(), formatTonesSummary(local))
			}
		}
	}

	os.Stdout = oldStdout
	t.Logf("LFD files=%d discover_ab_%d/%d detect_ab_%d/%d (want A≈%.0f B≈%.0f)",
		len(entries), discoverHit, len(entries), detectHit, len(entries), wantA, wantB)
}
