// Copyright (C) 2025 Thinline Dynamic Solutions

package mapping

import "testing"

func TestSanitizeFalseMedicalAlarmToBurglary(t *testing.T) {
	tx := "ORDER RESPONSE FOR AN ALARM AT TMC THE ETM AT 21593 LORRAINE ROAD SHOWING TAMPER TAMPER"
	labels := []string{"ALARM DROP MEDICAL", "FIRE ALARM DROP", "BURGLARY"}
	got := SanitizeCallNatureAssignment(tx, "ALARM DROP MEDICAL", labels)
	if got != "BURGLARY" {
		t.Fatalf("got %q want BURGLARY", got)
	}
}

func TestCutFingerWithKnifeIsBleeding(t *testing.T) {
	tx := "STATION 3 MATIC 3 GUN BE NEEDED 24041 PURITAN 27 YEAR OLD FEMALE CUT HER FINGER WITH A KNIFE BLEEDING CAN DISPATCH CLEAR 648"
	labels := []string{"PERSON WITH KNIFE", "BLEEDING", "SUICIDE", "THREATS", "DOMESTIC"}
	terms := []string{
		"PERSON WITH KNIFE", "WITH A KNIFE", "HAS A KNIFE", "PULLED A KNIFE", "ARMED WITH KNIFE",
		"BLEEDING", "SUICIDE", "THREATENING", "DOMESTIC",
	}
	phrase := map[string]string{
		"PERSON WITH KNIFE": "PERSON WITH KNIFE",
		"WITH A KNIFE":      "PERSON WITH KNIFE",
		"HAS A KNIFE":       "PERSON WITH KNIFE",
		"PULLED A KNIFE":    "PERSON WITH KNIFE",
		"ARMED WITH KNIFE":  "PERSON WITH KNIFE",
		"BLEEDING":          "BLEEDING",
		"SUICIDE":           "SUICIDE",
		"THREATENING":       "THREATS",
		"DOMESTIC":          "DOMESTIC",
	}
	got := FinalizeIncidentNatureWithMap("", tx, labels, terms, true, phrase, true)
	got = SanitizeCallNatureAssignment(tx, got, labels)
	if got != "BLEEDING" {
		t.Fatalf("cut finger got %q want BLEEDING", got)
	}

	threat := "YOU NEED THIS STAGE FOR A POSSIBLE DOMESTIC WITH A KNIFE 5427 WILSON"
	got = FinalizeIncidentNatureWithMap("", threat, labels, terms, true, phrase, true)
	got = SanitizeCallNatureAssignment(threat, got, labels)
	if got != "PERSON WITH KNIFE" {
		t.Fatalf("domestic knife got %q want PERSON WITH KNIFE", got)
	}

	pulled := "SUBJECT THAT PULLED A KNIFE ON A SUBJECT ON THE BIKE PATH"
	got = FinalizeIncidentNatureWithMap("", pulled, labels, terms, true, phrase, true)
	got = SanitizeCallNatureAssignment(pulled, got, labels)
	if got != "PERSON WITH KNIFE" {
		t.Fatalf("pulled knife got %q want PERSON WITH KNIFE", got)
	}
}

func TestCommercialAlarmUsesFireTagWhenTypeUnspoken(t *testing.T) {
	labels := []string{"BURGLARY", "FIRE ALARM DROP"}
	terms := []string{"COMMERCIAL ALARM", "BURGLARY", "FIRE ALARM", "FIRE ALARM DROP", "TAMPER"}
	phrase := map[string]string{
		"COMMERCIAL ALARM": "BURGLARY",
		"BURGLARY":         "BURGLARY",
		"TAMPER":           "BURGLARY",
		"FIRE ALARM":       "FIRE ALARM DROP",
		"FIRE ALARM DROP":  "FIRE ALARM DROP",
	}
	base := ProcessInput{NatureCodes: labels, NatureMatchTerms: terms, NaturePhraseToLabel: phrase}

	school := "BATTALION 5 ENGINE 6 LADDER 36 COMMERCIAL ALARM ACTIVATION 15111 MILES AVENUE JOHN F KENNEDY HIGH SCHOOL"
	base.ServiceTag = "Fire Dispatch"
	if got := ResolveIncidentNature(school, base); got != "FIRE ALARM DROP" {
		t.Fatalf("fire companies got %q", got)
	}

	bare := "COMMERCIAL ALARM ACTIVATION 15111 MILES AVENUE JOHN F KENNEDY HIGH SCHOOL"
	if got := ResolveIncidentNature(bare, base); got != "FIRE ALARM DROP" {
		t.Fatalf("fire tag got %q", got)
	}

	base.ServiceTag = "Law Dispatch"
	if got := ResolveIncidentNature(bare, base); got != "BURGLARY" {
		t.Fatalf("law tag got %q", got)
	}

	tamper := "ENGINE 5 RESPOND TO 500 MAIN STREET FOR A COMMERCIAL ALARM SHOWING TAMPER"
	base.ServiceTag = "Fire Dispatch"
	if got := ResolveIncidentNature(tamper, base); got != "BURGLARY" {
		t.Fatalf("tamper got %q", got)
	}
}

func TestSanitizeKeepsRealMedicalAlarm(t *testing.T) {
	tx := "MEDICAL ALARM AT 120 MAIN STREET PENDANT ACTIVATION"
	labels := []string{"ALARM DROP MEDICAL", "BURGLARY"}
	got := SanitizeCallNatureAssignment(tx, "ALARM DROP MEDICAL", labels)
	if got != "ALARM DROP MEDICAL" {
		t.Fatalf("got %q want ALARM DROP MEDICAL", got)
	}
}
