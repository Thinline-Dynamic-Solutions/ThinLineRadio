package main

import "testing"

func parseMaskForTest(t *testing.T, mask string, filename string) *Call {
	t.Helper()

	dirwatch := NewDirwatch()
	dirwatch.Mask = mask

	call := NewCall()
	call.AudioFilename = filename

	if err := dirwatch.parseMask(call); err != nil {
		t.Fatalf("parseMask(%q, %q): %v", mask, filename, err)
	}

	return call
}

func TestParseMaskFrequencies(t *testing.T) {
	cases := []struct {
		mask      string
		filename  string
		frequency uint
		talkgroup uint
	}{
		{"#MHZ", "151.4000.wav", 151400000, 0},
		{"#KHZ", "460025.wav", 460025000, 0},
		{"#HZ", "851112500.wav", 851112500, 0},
		{"#TGMHZ", "151.4000.wav", 151400000, 151400},
		{"#TGMHZ", "851.1125.wav", 851112500, 851112},
		{"#TGMHZ", "460.0250.wav", 460025000, 460025},
		{"#TGKHZ", "154430.0.wav", 154430000, 154430},
		{"#TGHZ", "155475000.wav", 155475000, 155475},
	}

	for _, c := range cases {
		call := parseMaskForTest(t, c.mask, c.filename)

		if call.Frequency != c.frequency {
			t.Errorf("%s on %s: frequency %d, want %d", c.mask, c.filename, call.Frequency, c.frequency)
		}

		if call.Meta.TalkgroupRef != c.talkgroup {
			t.Errorf("%s on %s: talkgroup %d, want %d", c.mask, c.filename, call.Meta.TalkgroupRef, c.talkgroup)
		}
	}
}

func TestParseMaskUnitLabel(t *testing.T) {
	call := parseMaskForTest(t, "#TG_#UNIT_#UNITLBL", "1001_4321_Engine 5.wav")

	if call.Meta.TalkgroupRef != 1001 {
		t.Errorf("talkgroup %d, want 1001", call.Meta.TalkgroupRef)
	}

	if len(call.Units) != 1 || call.Units[0].UnitRef != 4321 {
		t.Fatalf("units %+v, want unit 4321", call.Units)
	}

	if len(call.Meta.UnitLabels) != 1 || call.Meta.UnitLabels[0] != "Engine 5" {
		t.Errorf("unit labels %v, want [Engine 5]", call.Meta.UnitLabels)
	}
}

func TestParseMaskEscapedBrackets(t *testing.T) {
	call := parseMaskForTest(t, `\[#TG\]`, "[1234].wav")

	if call.Meta.TalkgroupRef != 1234 {
		t.Errorf("talkgroup %d, want 1234", call.Meta.TalkgroupRef)
	}
}

func TestInvalidMasksAreRefusedWithoutPanicking(t *testing.T) {
	for _, mask := range []string{"[#DATE]", "[#TG]", "(a|b)_#TG", "#TG_(", "(extra)"} {
		dirwatch := NewDirwatch()
		dirwatch.Mask = mask

		if err := dirwatch.validateMask(); err == nil {
			t.Errorf("validateMask(%q) accepted an unreadable mask", mask)
		}

		call := NewCall()
		call.AudioFilename = "a_123.wav"

		if err := dirwatch.parseMask(call); err == nil {
			t.Errorf("parseMask(%q) returned no error", mask)
		}
	}
}

func TestValidateDirwatchList(t *testing.T) {
	valid := []any{
		map[string]any{"directory": "/a", "mask": "#TGMHZ"},
		map[string]any{"directory": "/b", "mask": ""},
		map[string]any{"directory": "/c"},
	}
	if err := validateDirwatchList(valid); err != nil {
		t.Errorf("valid list refused: %v", err)
	}

	invalid := append(valid, map[string]any{"directory": "/d", "mask": "[#TG]"})
	if err := validateDirwatchList(invalid); err == nil {
		t.Error("list with an unreadable mask accepted")
	}
}
