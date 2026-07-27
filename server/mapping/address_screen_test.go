package mapping

import "testing"

func TestTranscriptHasGeocodeAnchor(t *testing.T) {
	cases := []struct {
		name string
		tx   string
		want bool
	}{
		{"house+street", "MEDIC 2 RESPOND 5219 STATE ROUTE 303 FOR CHEST PAIN", true},
		{"typed intersection", "ENGINE 12 MASON STREET AND MAHONING STREET MVA", true},
		{"ih route", "DISABLED VEHICLE 6998 IH 35 SOUTHBOUND", true},
		{"loop route", "DISABLED 1200 LOOP 1604 WESTBOUND", true},
		{"street suffix only", "COPY CLEAR ON MAIN STREET STAND BY", false},
		{"en route chatter", "MEDIC 3 EN ROUTE TO THE HOSPITAL", false},
		{"eastbound alone", "TRAFFIC IS EASTBOUND HEAVY", false},
		{"status only", "COPY THAT WE ARE CLEAR", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TranscriptHasGeocodeAnchor(tc.tx, nil)
			if got != tc.want {
				t.Fatalf("TranscriptHasGeocodeAnchor(%q)=%v want %v", tc.tx, got, tc.want)
			}
		})
	}
}
