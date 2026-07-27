package mapping

import "testing"

func TestAddressAlignsAbbrevExpansionsUniversal(t *testing.T) {
	cases := []struct {
		name string
		addr string
		tx   string
	}{
		{
			"SE vs SOUTHEAST",
			"6246 MINES ROAD SOUTHEAST",
			"STATION 31 SQUAD 6246 MINES ROAD SE BETWEEN JOHNNY CAKE",
		},
		{
			"SOUTHEAST vs SE",
			"6246 MINES ROAD SE",
			"6246 MINES ROAD SOUTHEAST FOR MENTAL EVAL",
		},
		{
			"RD vs ROAD",
			"100 MAIN ROAD",
			"RESPOND 100 MAIN RD FOR ALARM",
		},
		{
			"AVENUE vs AVE",
			"12 OAK AVENUE",
			"12 OAK AVE CHEST PAIN",
		},
		{
			"NORTH vs N",
			"500 NORTH MAIN STREET",
			"500 N MAIN STREET MVA",
		},
		{
			"spaced NORTH EAST vs NE",
			"200 MARKET STREET NORTHEAST",
			"200 MARKET STREET NORTH EAST ALARM",
		},
		{
			"NW vs NORTHWEST",
			"88 PINE DRIVE NORTHWEST",
			"88 PINE DRIVE NW SMOKE ALARM",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !AddressAlignsWithTranscript(tc.addr, tc.tx, nil) {
				t.Fatalf("AddressAlignsWithTranscript(%q, %q)=false", tc.addr, tc.tx)
			}
		})
	}
}

func TestAddressAlignsStillRejectsFabrication(t *testing.T) {
	tx := "STATION 31 CLEAR ON SCENE NO ADDRESS"
	if AddressAlignsWithTranscript("6246 MINES ROAD SOUTHEAST", tx, nil) {
		t.Fatal("must still reject streets never spoken")
	}
}
