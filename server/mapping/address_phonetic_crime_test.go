package mapping

import (
	"strings"
	"testing"
)

func TestTranscriptIsPhoneticUnitCrimeCodeKingRobber(t *testing.T) {
	tr := "216 OVER 2 KING KING ROBBER 6315 KING KING ROBBER 6315"
	if !TranscriptIsPhoneticUnitCrimeCode(tr) {
		t.Fatal("expected phonetic unit/crime-code transcript to be rejected")
	}
	if !AddressIsPhoneticUnitCrimeMisextract("2 KING", tr) {
		t.Fatal("expected 2 KING extract to be rejected")
	}
	if !AddressIsPhoneticUnitCrimeMisextract("2 KING GEORGE AVENUE", tr) {
		t.Fatal("expected gateway-expanded King George pin to be rejected")
	}
	if !AddressStreetIsPhoneticAlphabetOnly("2 KING") {
		t.Fatal("expected bare KING street to count as phonetic-only")
	}
}

func TestApplyExtractedAddressGuardsClearsKingRobber(t *testing.T) {
	tr := "216 OVER 2 KING KING ROBBER 6315 KING KING ROBBER 6315"
	c := &CuratedAlert{Address: "2 KING GEORGE AVENUE", NatureDesc: "ROBBERY"}
	ApplyExtractedAddressGuards(c, tr, nil)
	if strings.TrimSpace(c.Address) != "" {
		t.Fatalf("address should be cleared, got %q", c.Address)
	}
}

func TestTranscriptIsPhoneticUnitCrimeCodeKeepsRealAddress(t *testing.T) {
	tr := "ENGINE 12 RESPOND TO 2 KING GEORGE AVENUE FOR A STRUCTURE FIRE"
	if TranscriptIsPhoneticUnitCrimeCode(tr) {
		t.Fatal("real King George dispatch must not be treated as unit/crime code")
	}
	tr2 := "216 OVER TO 2 KING AVENUE FOR A ROBBERY"
	if TranscriptIsPhoneticUnitCrimeCode(tr2) {
		t.Fatal("real OVER-to-address robbery dispatch must not be blocked")
	}
}
