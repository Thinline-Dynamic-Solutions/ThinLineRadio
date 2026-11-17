package mapping

import (
	"strings"
	"testing"
)

func TestNormalizeTranscriptPlainText(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  hello  ", "HELLO"},
		{"AT 10701 EAST BOULEVARD, AT THE CLEVELAND VA MEDICAL CENTER.", "AT 10701 EAST BOULEVARD AT THE CLEVELAND VA MEDICAL CENTER"},
		{"10-20 CENTER STREET", "1020 CENTER STREET"},
		{"APT#2", "APT 2"},
		{"MAIN & ELM", "MAIN AND ELM"},
		{"U.S. ROUTE 422", "U S ROUTE 422"},
		{"STATION40", "STATION 40"},
		{"40STATION", "40 STATION"},
		{"85-E-212 STREET", "85 E 212 STREET"},
		{"O'BRIEN ROAD", "O BRIEN ROAD"},
		{"CROSS OF 88, AND BRADLEY", "CROSS OF 88 AND BRADLEY"},
		{"74-YEAR-OLD", "74 YEAR OLD"},
		// Ordinals stay glued — do not become "88 TH" / "3 RD".
		{"3235 W 88TH ST", "3235 W 88TH ST"},
		{"113 E 2ND ST", "113 E 2ND ST"},
		{"3RD STREET", "3RD STREET"},
		{"1ST AVENUE", "1ST AVENUE"},
		{"798 RICHMOND RD", "798 RICHMOND RD"},
		{"798 RICHMOND RD.", "798 RICHMOND RD"},
		{"125TH", "125TH"},
	}
	for _, tt := range tests {
		got := NormalizeTranscriptPlainText(tt.in)
		if got != tt.want {
			t.Errorf("NormalizeTranscriptPlainText(%q)=\n  %q\nwant %q", tt.in, got, tt.want)
		}
	}
}

func TestPreCleanTranscriptRunsPlainNormalizeFirst(t *testing.T) {
	got := PreCleanTranscript("AT 10701 EAST BOULEVARD, AT THE CLEVELAND VA.")
	if got == "" {
		t.Fatal("empty")
	}
	for _, bad := range []string{",", ".", "-", "#", "&", "'"} {
		if containsRune(got, bad[0]) {
			t.Errorf("PreClean still has %q in %q", bad, got)
		}
	}
}

func TestExpandLeadingTenFourAck(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"104", "10 4"},
		{"104 104", "10 4 104"},
		{"104 WELL GO WITH VIANNA TRY TO CUT HIM OFF THERE", "10 4 WELL GO WITH VIANNA TRY TO CUT HIM OFF THERE"},
		{"104 ILL HAVE HIM COME OVER HES STANDING ACROSS THE STREET", "10 4 ILL HAVE HIM COME OVER HES STANDING ACROSS THE STREET"},
		{"104 NEXT ONES GOING TO BE ISABELLA", "10 4 NEXT ONES GOING TO BE ISABELLA"},
		// House numbers and unit 104 traffic stay as-is.
		{"104 MAIN STREET", "104 MAIN STREET"},
		{"104 IS 4689 CARIBBEE AVENUE NORTHEAST", "104 IS 4689 CARIBBEE AVENUE NORTHEAST"},
		{"104 EN ROUTE AT 16", "104 EN ROUTE AT 16"},
		{"104 BY THE AUTO ZONE", "104 BY THE AUTO ZONE"},
		{"1041 COPY", "1041 COPY"},
		{"COPY 104", "COPY 104"},
	}
	for _, tt := range tests {
		if got := expandLeadingTenFourAck(tt.in); got != tt.want {
			t.Errorf("expandLeadingTenFourAck(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestPreCleanTranscriptKeepsTenFourAckApart(t *testing.T) {
	for _, in := range []string{"104 WELL GO WITH VIANNA", "10-4, we'll go with Vianna"} {
		if got := PreCleanTranscript(in); !strings.HasPrefix(got, "10 4 ") {
			t.Errorf("PreCleanTranscript(%q)=%q want 10 4 prefix", in, got)
		}
	}
}

func TestTenFourAckIsRadioChatter(t *testing.T) {
	for _, in := range []string{
		"104 WELL GO WITH VIANNA TRY TO CUT HIM OFF THERE",
		"104 ILL HAVE HIM COME OVER HES STANDING ACROSS THE STREET",
	} {
		if !TranscriptIsRadioChatter(PreCleanTranscript(in)) {
			t.Errorf("TranscriptIsRadioChatter(PreClean(%q)) = false, want true", in)
		}
	}
	if TranscriptIsRadioChatter(PreCleanTranscript("104 MAIN STREET FOR A FALL")) {
		t.Error("real 104 MAIN STREET dispatch treated as chatter")
	}
}

func containsRune(s string, r byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == r {
			return true
		}
	}
	return false
}
