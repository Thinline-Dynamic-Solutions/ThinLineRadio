package mapping

import "testing"

func TestNatureTokenWordMatchRejectsGoingToBeAsBees(t *testing.T) {
	if natureTokenWordMatch("BE", "BEES") {
		t.Fatal("BE must not match BEES (going to be ≠ allergic)")
	}
	if !natureTokenWordMatch("BEE", "BEES") {
		t.Fatal("BEE must still match BEES")
	}
	if !natureTokenWordMatch("BEES", "BEE") {
		t.Fatal("BEES must still match BEE")
	}
}

func TestNatureStemRejectsPersonnelAsPersonal(t *testing.T) {
	if natureWordStemMatch("PERSONNEL", "PERSONAL") {
		t.Fatal("PERSONNEL must not stem-match PERSONAL")
	}
	if natureWordStemMatch("PERSON", "PERSONAL") {
		t.Fatal("PERSON must not stem-match PERSONAL")
	}
	if natureTokenWordMatch("PERSONNEL", "PERSONAL") {
		t.Fatal("PERSONNEL must not token-match PERSONAL")
	}
	if !natureWordStemMatch("DEHYDRATED", "DEHYDRATION") {
		t.Fatal("DEHYDRATED must still match DEHYDRATION")
	}
	if !natureWordStemMatch("INJURED", "INJURY") {
		t.Fatal("INJURED must still match INJURY")
	}
}

func TestClassifyGoingToBeIsNotAllergic(t *testing.T) {
	labels := []string{"ALLERGIC REACTION", "FALL", "BREAK", "COURT"}
	terms := []string{"ALLERGIC", "ALLERGIC REACTION", "ALLERGY", "BEES", "BEE STING", "STUNG"}
	p2l := map[string]string{
		"ALLERGIC": "ALLERGIC REACTION", "ALLERGIC REACTION": "ALLERGIC REACTION",
		"ALLERGY": "ALLERGIC REACTION", "BEES": "ALLERGIC REACTION",
		"BEE STING": "ALLERGIC REACTION", "STUNG": "ALLERGIC REACTION",
	}
	tx := "LET S SEE THAT ADDRESS FOR YOU IT S GOING TO BE 6643 ANNABELLE OKAY"
	if got := ClassifyCallNature(tx, labels, terms, p2l); got == "ALLERGIC REACTION" {
		t.Fatalf("going-to-be must not classify allergic, got %q", got)
	}
	if got := ClassifyCallNature("MEDICS ARE RESPONDING ON AN ALLERGIC REACTION", labels, terms, p2l); got != "ALLERGIC REACTION" {
		t.Fatalf("real allergic must still classify, got %q", got)
	}
}

func TestClassifySquadPersonnelIsNotPersonal(t *testing.T) {
	labels := []string{"PERSONAL", "GENERAL ILLNESS", "FALL"}
	terms := []string{"PERSONAL", "GENERAL ILLNESS", "SICK PERSON"}
	p2l := map[string]string{"PERSONAL": "PERSONAL", "GENERAL ILLNESS": "GENERAL ILLNESS", "SICK PERSON": "GENERAL ILLNESS"}
	tx := "8500 APPLE CREEK SQUAD PERSONNEL 59 NORTH APPLE CREEK ROAD"
	if got := ClassifyCallNature(tx, labels, terms, p2l); got == "PERSONAL" {
		t.Fatalf("squad personnel must not classify PERSONAL, got %q", got)
	}
}

func TestClassifyRadioBreakIsNotBreakNature(t *testing.T) {
	labels := []string{"BREAK", "PARKING VIOLATION/COMPLAINT"}
	terms := []string{"BREAK", "BREAK TIME", "COFFEE BREAK"}
	p2l := map[string]string{"BREAK": "BREAK", "BREAK TIME": "BREAK", "COFFEE BREAK": "BREAK"}
	if got := ClassifyCallNature("844 CLEVELAND BREAK FOR THE OTHER UNIT INVOLVED IN THIS", labels, terms, p2l); got == "BREAK" {
		t.Fatalf("radio BREAK FOR must not classify BREAK, got %q", got)
	}
}

func TestClassifyDepartmentRescueIsNotEntrapment(t *testing.T) {
	labels := []string{"ENTRAPMENT/RESCUE", "SEIZURE"}
	terms := []string{"RESCUE", "ENTRAPMENT", "ENTRAPMENT/RESCUE", "SEIZURE"}
	p2l := map[string]string{
		"RESCUE": "ENTRAPMENT/RESCUE", "ENTRAPMENT": "ENTRAPMENT/RESCUE",
		"ENTRAPMENT/RESCUE": "ENTRAPMENT/RESCUE", "SEIZURE": "SEIZURE",
	}
	tx := "ATTENTION WESTFIELD RESCUE REQUEST FOR YOUR SQUAD 9005 HIGHLAND FOR A FEMALE WITH A STAPLE INTO HER HAND"
	if got := ClassifyCallNature(tx, labels, terms, p2l); got == "ENTRAPMENT/RESCUE" {
		t.Fatalf("department rescue request must not classify ENTRAPMENT, got %q", got)
	}
}

func TestClassifyCheckTheAreaIsNotWelfare(t *testing.T) {
	labels := []string{"CHECK WELFARE", "SHOTS FIRED"}
	terms := []string{"CHECK WELFARE", "CHECK THE AREA", "CHECK ON", "WELFARE CHECK", "SHOTS FIRED"}
	p2l := map[string]string{
		"CHECK WELFARE": "CHECK WELFARE", "CHECK THE AREA": "CHECK WELFARE",
		"CHECK ON": "CHECK WELFARE", "WELFARE CHECK": "CHECK WELFARE",
		"SHOTS FIRED": "SHOTS FIRED",
	}
	tx := "YOU CAN CHECK THE AREA OF 24250 LORRAINE ROAD WE HAD A CALLER REPORT TRASH IN THE ROADWAY"
	if got := ClassifyCallNature(tx, labels, terms, p2l); got == "CHECK WELFARE" {
		t.Fatalf("check the area must not classify welfare, got %q", got)
	}
	if got := ClassifyCallNature("WELFARE CHECK AT 3071 KENDALL ROAD CHECKING ON A MALE", labels, terms, p2l); got != "CHECK WELFARE" {
		t.Fatalf("real welfare check must still classify, got %q", got)
	}
}

func TestClassifyGunCheckIsNotPersonWithGun(t *testing.T) {
	labels := []string{"PERSON WITH GUN", "THEFT"}
	terms := []string{"GUN", "PERSON WITH GUN", "THEFT"}
	p2l := map[string]string{"GUN": "PERSON WITH GUN", "PERSON WITH GUN": "PERSON WITH GUN", "THEFT": "THEFT"}
	if got := ClassifyCallNature("ITEM 37 THAT GUN S GOING TO CHECK OK CHECK S CLEAR TERMINAL 292", labels, terms, p2l); got == "PERSON WITH GUN" {
		t.Fatalf("gun check radio traffic must not classify person with gun, got %q", got)
	}
	if got := ClassifyCallNature("3229 E 48 CALLERS SAY SOMEONE PULLED A GUN ON THEM", labels, terms, p2l); got != "PERSON WITH GUN" {
		t.Fatalf("pulled a gun must still classify, got %q", got)
	}
}

func TestClassifyChaseUnitIsNotPursuit(t *testing.T) {
	labels := []string{"PURSUIT", "FALL"}
	terms := []string{"CHASE", "PURSUIT", "FALL", "FELL"}
	p2l := map[string]string{"CHASE": "PURSUIT", "PURSUIT": "PURSUIT", "FALL": "FALL", "FELL": "FALL"}
	if got := ClassifyCallNature("GREEN DISTRICT 2 EMS GREEN CHASE 2 GREEN MEDIC 2 708 MOORE ROAD FALLS", labels, terms, p2l); got == "PURSUIT" {
		t.Fatalf("GREEN CHASE unit must not classify pursuit, got %q", got)
	}
	if got := ClassifyCallNature("KIRTLAND POLICE WAS INVOLVED IN A PURSUIT THE VEHICLE HAS STOPPED", labels, terms, p2l); got != "PURSUIT" {
		t.Fatalf("real pursuit must still classify, got %q", got)
	}
}

func TestClassifyOfficerLunchIsNotLunchNature(t *testing.T) {
	labels := []string{"LUNCH", "PARKING VIOLATION/COMPLAINT"}
	terms := []string{"LUNCH"}
	p2l := map[string]string{"LUNCH": "LUNCH"}
	if got := ClassifyCallNature("I AM COMPLETE WITH LUNCH AT 101 HARRY S EYE", labels, terms, p2l); got == "LUNCH" {
		t.Fatalf("unit lunch status must not classify LUNCH, got %q", got)
	}
}

func TestClassifyOfficerCellIsNotCellPhoneCalls(t *testing.T) {
	labels := []string{"CELL PHONE CALLS", "THEFT", "ABANDONED 911 CALL"}
	terms := []string{"CELL PHONE", "CELL PHONE CALLS", "THEFT", "ABANDONED 911"}
	p2l := map[string]string{
		"CELL PHONE": "CELL PHONE CALLS", "CELL PHONE CALLS": "CELL PHONE CALLS",
		"THEFT": "THEFT", "ABANDONED 911": "ABANDONED 911 CALL",
	}
	if got := ClassifyCallNature("MY CELL PHONE 413 WILL BE OUT AT 581 HALL STREET MAKING CONTACT", labels, terms, p2l); got == "CELL PHONE CALLS" {
		t.Fatalf("officer cell status must not classify cell phone calls, got %q", got)
	}
	if got := ClassifyCallNature("INCOMPLETE 911 CELL PHONE IN THE AREA 4268 NEWCOMER ROAD NOTHING HEARD", labels, terms, p2l); got != "CELL PHONE CALLS" {
		t.Fatalf("incomplete 911 cell must still classify, got %q", got)
	}
}

func TestClassifyHangingFireIsNotHanging(t *testing.T) {
	labels := []string{"HANGING", "WIRES/TREES DOWN"}
	terms := []string{"HANGING", "HANGED", "WIRES"}
	p2l := map[string]string{"HANGING": "HANGING", "HANGED": "HANGING", "WIRES": "WIRES/TREES DOWN"}
	if got := ClassifyCallNature("STAFFED IN THE BACKYARD AND IS NOW HANGING FIRE WIRE CLOSE TO THE RESIDENCE", labels, terms, p2l); got == "HANGING" {
		t.Fatalf("hanging fire must not classify hanging, got %q", got)
	}
	if got := ClassifyCallNature("TEENAGE MALE ATTEMPTED HANGING AT THE DETENTION CENTER", labels, terms, p2l); got != "HANGING" {
		t.Fatalf("attempted hanging must still classify, got %q", got)
	}
	suicideLabels := []string{"HANGING", "SUICIDE"}
	suicideTerms := []string{"HANGING", "SUICIDE", "SUICIDAL"}
	suicideMap := map[string]string{"HANGING": "HANGING", "SUICIDE": "SUICIDE", "SUICIDAL": "SUICIDE"}
	if got := ClassifyCallNature("THIRD PARTY FOR A MALE ATTEMPTING TO HANG HIMSELF WE HAVE PD EN ROUTE", suicideLabels, suicideTerms, suicideMap); got != "SUICIDE" && got != "HANGING" {
		t.Fatalf("hang himself must classify suicide/hanging, got %q", got)
	}
}

func TestClassifyAerialLiftIsNotLiftAssist(t *testing.T) {
	labels := []string{"LIFT ASSIST", "ENTRAPMENT/RESCUE"}
	terms := []string{"LIFT", "LIFT ASSIST", "TRAPPED"}
	p2l := map[string]string{"LIFT": "LIFT ASSIST", "LIFT ASSIST": "LIFT ASSIST", "TRAPPED": "ENTRAPMENT/RESCUE"}
	if got := ClassifyCallNature("A MALE IN A LIFT BASKET THAT S STUCK ABOUT 40 FEET IN THE AIR", labels, terms, p2l); got == "LIFT ASSIST" {
		t.Fatalf("aerial lift basket must not classify lift assist, got %q", got)
	}
	if got := ClassifyCallNature("TWINSBURG MEDIC IS NEEDED FOR A LIFT ASSIST AT 1954 WESTWOOD", labels, terms, p2l); got != "LIFT ASSIST" {
		t.Fatalf("real lift assist must still classify, got %q", got)
	}
}

func TestClassifyFallsAvenueIsNotFall(t *testing.T) {
	labels := []string{"FALL", "GENERAL ILLNESS"}
	terms := []string{"FALL", "FALLS", "FELL", "FALLEN", "FALL VICTIM"}
	p2l := map[string]string{"FALL": "FALL", "FALLS": "FALL", "FELL": "FALL", "FALLEN": "FALL", "FALL VICTIM": "FALL"}
	if got := ClassifyCallNature("FALLS EMS DISTRICT 1 MED 1 213 FALLS AVENUE BETWEEN 2ND STREET AND 3RD STREET", labels, terms, p2l); got == "FALL" {
		t.Fatalf("FALLS AVENUE must not classify FALL, got %q", got)
	}
	if got := ClassifyCallNature("59 YEAR OLD FEMALE FALL WITH A SHOULDER INJURY AT 5849 CUTTER ROAD", labels, terms, p2l); got != "FALL" {
		t.Fatalf("real fall must still classify, got %q", got)
	}
	if got := ClassifyCallNature("FALLS EMS DISTRICT 4 NET 4 1931 NORTHAMPTON ROAD FALL BETWEEN WEST PORTAGE TRAIL AND MEREDITH LANE", labels, terms, p2l); got != "FALL" {
		t.Fatalf("CAD FALL BETWEEN must still classify, got %q", got)
	}
}

func TestClassifyLongShotIsNotShotsFired(t *testing.T) {
	labels := []string{"SHOTS FIRED"}
	terms := []string{"SHOTS", "SHOTS FIRED", "SHOOTING"}
	p2l := map[string]string{"SHOTS": "SHOTS FIRED", "SHOTS FIRED": "SHOTS FIRED", "SHOOTING": "SHOTS FIRED"}
	if got := ClassifyCallNature("IT S A LONG SHOT BUT WE LL BE HEADING TO 12019 LAKE AVENUE ROOM 216", labels, terms, p2l); got == "SHOTS FIRED" {
		t.Fatalf("long shot must not classify shots fired, got %q", got)
	}
}

func TestClassifyCardinalVillageIsNotCardiac(t *testing.T) {
	labels := []string{"CARDIAC ARREST", "FALL"}
	terms := []string{"CARDIAC", "CARDIAC ARREST", "FALL"}
	p2l := map[string]string{"CARDIAC": "CARDIAC ARREST", "CARDIAC ARREST": "CARDIAC ARREST", "FALL": "FALL"}
	tx := "FALLS EMS DISTRICT 3 MED 3 171 GRAHAM ROAD CARDINAL RETIREMENT VILLAGE APARTMENT 79"
	if got := ClassifyCallNature(tx, labels, terms, p2l); got == "CARDIAC ARREST" {
		t.Fatalf("CARDINAL place name must not classify cardiac, got %q", got)
	}
	if got := ClassifyCallNature("CARDIAC ARREST STATION 331 MEDIC 10 AT 4296 BOWTOWN ROAD", labels, terms, p2l); got != "CARDIAC ARREST" {
		t.Fatalf("real cardiac arrest must still classify, got %q", got)
	}
}

func TestClassifyLakeRescueDepartmentIsNotLakeEmergency(t *testing.T) {
	labels := []string{"LAKE EMERGENCY", "DIZZINESS"}
	terms := []string{"LAKE RESCUE", "LAKE EMERGENCY", "LIGHTHEADED", "DIZZINESS"}
	p2l := map[string]string{
		"LAKE RESCUE": "LAKE EMERGENCY", "LAKE EMERGENCY": "LAKE EMERGENCY",
		"LIGHTHEADED": "DIZZINESS", "DIZZINESS": "DIZZINESS",
	}
	tx := "ATTENTION WILLOW LAKE RESCUE IS NEEDED 32901 LAKESHORE FOR A 47 YEAR OLD FEMALE FEELING LIGHTHEADED"
	if got := ClassifyCallNature(tx, labels, terms, p2l); got == "LAKE EMERGENCY" {
		t.Fatalf("lake-named department must not classify lake emergency, got %q", got)
	}
}

func TestClassifyGasPumpIsNotPumpsNature(t *testing.T) {
	labels := []string{"PUMPS", "CRASH WITH REPORTED INJURIES"}
	terms := []string{"PUMPS", "PUMP", "CRASH"}
	p2l := map[string]string{"PUMPS": "PUMPS", "PUMP": "PUMPS", "CRASH": "CRASH WITH REPORTED INJURIES"}
	if got := ClassifyCallNature("VEHICLE INTO A GAS PUMP AT THE SHELL STATION", labels, terms, p2l); got == "PUMPS" {
		t.Fatalf("gas pump must not classify PUMPS, got %q", got)
	}
}

func TestClassifyBreakIntoIsBreakingAndEntering(t *testing.T) {
	labels := []string{"BREAK", "BREAKING AND ENTERING"}
	terms := []string{"BREAK", "BREAK IN", "BREAK INTO", "BREAKING AND ENTERING"}
	p2l := map[string]string{
		"BREAK": "BREAK", "BREAK IN": "BREAKING AND ENTERING",
		"BREAK INTO": "BREAKING AND ENTERING", "BREAKING AND ENTERING": "BREAKING AND ENTERING",
	}
	if got := ClassifyCallNature("SOMEONE S TRYING TO BREAK INTO THE HOME AT 7411 ELTON", labels, terms, p2l); got != "BREAKING AND ENTERING" {
		t.Fatalf("break into must classify B&E, got %q", got)
	}
}

func TestClassifyBreedingDifficultyIsNotAssumedBreathing(t *testing.T) {
	labels := []string{"SHORTNESS OF BREATH", "FALL", "UNKNOWN PROBLEM"}
	terms := []string{
		"SHORTNESS OF BREATH", "BREATHING DIFFICULTY", "DIFFICULTY BREATHING",
		"BREATHING PROBLEMS", "FALL", "FALLEN",
	}
	p2l := map[string]string{
		"SHORTNESS OF BREATH":  "SHORTNESS OF BREATH",
		"BREATHING DIFFICULTY": "SHORTNESS OF BREATH",
		"DIFFICULTY BREATHING": "SHORTNESS OF BREATH",
		"BREATHING PROBLEMS":   "SHORTNESS OF BREATH",
		"FALL":                 "FALL",
		"FALLEN":               "FALL",
	}
	for _, tx := range []string{
		"SQUAD PERSONNEL 837 EAST PUZZLE HOME ROAD FOR A FEMALE 160 IS BREEDING DIFFICULTY",
		"FALLS EMS DISTRICT 2 MED 2 1200 BUCKINGHAM GATE BOULEVARD BREEDING PROBLEMS",
		"RESCUE 81 4999 SCIOTO DARBY ROAD DIFFICULTY BREEDING",
		"62 YEAR OLD FEMALE BREED DIFFICULTY",
		"STATION 14 FOR A BREEDING DIFFICULTY 125 MAGNOLIA ROAD SOUTHWEST CROSS STREET TO FARGO ROAD SOUTHWEST AND FALLEN ROAD SOUTHWEST",
	} {
		if got := ClassifyCallNature(tx, labels, terms, p2l); got == "SHORTNESS OF BREATH" {
			t.Fatalf("breeding/breed must not be assumed as breathing (%q), got %q", tx, got)
		}
		if got := ClassifyCallNature(tx, labels, terms, p2l); got == "FALL" {
			t.Fatalf("FALLEN ROAD must not classify FALL when the complaint is breeding (%q), got %q", tx, got)
		}
	}
	if got := ClassifyCallNature("MED 7 1434 BREEDING ROAD BREATHING PROBLEMS", labels, terms, p2l); got != "SHORTNESS OF BREATH" {
		t.Fatalf("spoken breathing problems must still classify, got %q", got)
	}
}

func TestClassifyRadioStandByIsNotStandbyNature(t *testing.T) {
	labels := []string{"STANDBY", "SHORTNESS OF BREATH"}
	terms := []string{"STANDBY", "STAND BY", "STANDING BY", "BREATHING DIFFICULTY"}
	p2l := map[string]string{
		"STANDBY":              "STANDBY",
		"STAND BY":             "STANDBY",
		"STANDING BY":          "STANDBY",
		"BREATHING DIFFICULTY": "SHORTNESS OF BREATH",
	}
	if got := ClassifyCallNature("DO WE HAVE A PHONE NUMBER FOR THIS LOCATION STAND BY 9466346994", labels, terms, p2l); got == "STANDBY" {
		t.Fatalf("radio stand by must not classify STANDBY, got %q", got)
	}
	if got := ClassifyCallNature("33 IS STANDING BY WITH ONE REQUESTING RE TONE FOR A 51 YEAR OLD MALE BREATHING DIFFICULTY", labels, terms, p2l); got == "STANDBY" {
		t.Fatalf("unit standing by with one must not classify STANDBY, got %q", got)
	}
	if got := ClassifyCallNature("CAN YOU RESPOND FOR A STANDBY AT 865 MCKINLEY AVENUE SOUTHWEST", labels, terms, p2l); got != "STANDBY" {
		t.Fatalf("dispatch standby at an address must still classify, got %q", got)
	}
	if got := ClassifyCallNature("THEY HAVE A TECHNICIAN WORKING ON THE ATM THEY RE ASKING FOR A STAND BY", labels, terms, p2l); got != "STANDBY" {
		t.Fatalf("property stand-by request must still classify, got %q", got)
	}
}

func TestClassifySpokenUnknownRecoveries(t *testing.T) {
	labels := []string{
		"TRAUMATIC INJURY", "SHORTNESS OF BREATH", "CARDIAC ARREST", "HEART PROBLEM",
		"CRASH WITH REPORTED INJURIES", "GENERAL ILLNESS", "FIRE ALARM DROP",
		"STRUCTURE FIRE", "CHECK WELFARE", "BLEEDING", "LIFT ASSIST",
		"ALTERED LEVEL OF CONSCIOUSNESS", "MENTAL", "ELECTRICAL FIRE",
		"UNKNOWN PROBLEM",
	}
	terms := []string{
		"INJURED PERSON", "HEAD INJURY", "HIP INJURY", "TRAUMATIC INJURY",
		"BREATHING DIFFICULTIES", "BREATHING DIFFICULTY", "OUT OF BREATH",
		"NON BREATHER", "CARDIAC ARREST", "HIGH HEART RATE", "POSSIBLE HEART",
		"HEART PROBLEM", "VEHICLE ACCIDENT", "TRAFFIC COLLISION",
		"SICK PARTY", "ILL MALE", "AN ILLNESS", "WHO S ILL", "ALARM FIRE", "FIRE ALARM",
		"BOX ALARM", "KITCHEN FIRE", "STRUCTURE FIRE", "WELLNESS CHECK",
		"CHECK WELFARE", "NOSEBLEED", "BLEEDING", "INVALID ASSIST",
		"CANNOT GET UP", "LIFT ASSIST", "CHANGE IN MENTAL STATUS",
		"ALTERED LEVEL OF CONSCIOUSNESS", "MENTAL", "MENTAL HEALTH",
		"ELECTRICAL", "ELECTRICAL FIRE", "CAR ALARM",
	}
	p2l := map[string]string{
		"INJURED PERSON": "TRAUMATIC INJURY", "HEAD INJURY": "TRAUMATIC INJURY",
		"HIP INJURY": "TRAUMATIC INJURY", "TRAUMATIC INJURY": "TRAUMATIC INJURY",
		"BREATHING DIFFICULTIES": "SHORTNESS OF BREATH", "BREATHING DIFFICULTY": "SHORTNESS OF BREATH",
		"OUT OF BREATH": "SHORTNESS OF BREATH", "NON BREATHER": "CARDIAC ARREST",
		"CARDIAC ARREST": "CARDIAC ARREST", "HIGH HEART RATE": "HEART PROBLEM",
		"POSSIBLE HEART": "HEART PROBLEM", "HEART PROBLEM": "HEART PROBLEM",
		"VEHICLE ACCIDENT": "CRASH WITH REPORTED INJURIES",
		"TRAFFIC COLLISION": "CRASH WITH REPORTED INJURIES",
		"SICK PARTY": "GENERAL ILLNESS", "ILL MALE": "GENERAL ILLNESS",
		"AN ILLNESS": "GENERAL ILLNESS", "WHO S ILL": "GENERAL ILLNESS",
		"ALARM FIRE": "FIRE ALARM DROP",
		"FIRE ALARM": "FIRE ALARM DROP", "BOX ALARM": "STRUCTURE FIRE",
		"KITCHEN FIRE": "STRUCTURE FIRE", "STRUCTURE FIRE": "STRUCTURE FIRE",
		"WELLNESS CHECK": "CHECK WELFARE", "CHECK WELFARE": "CHECK WELFARE",
		"NOSEBLEED": "BLEEDING", "BLEEDING": "BLEEDING",
		"INVALID ASSIST": "LIFT ASSIST", "CANNOT GET UP": "LIFT ASSIST",
		"LIFT ASSIST": "LIFT ASSIST", "CHANGE IN MENTAL STATUS": "ALTERED LEVEL OF CONSCIOUSNESS",
		"ALTERED LEVEL OF CONSCIOUSNESS": "ALTERED LEVEL OF CONSCIOUSNESS",
		"MENTAL": "MENTAL", "MENTAL HEALTH": "MENTAL",
		"ELECTRICAL": "ELECTRICAL FIRE", "ELECTRICAL FIRE": "ELECTRICAL FIRE",
		"CAR ALARM": "FIRE ALARM DROP",
	}
	cases := []struct {
		tx, want string
	}{
		{"MEDIC 95 QUINT 93 7175 AVERY ROAD INJURED PERSON", "TRAUMATIC INJURY"},
		{"8500 MOUNT EATON SQUAD PERSONNEL 9458 TENTROP ROAD 80 YEAR OLD MALE WITH A HIP INJURY", "TRAUMATIC INJURY"},
		{"8500 DALTON SQUAD PERSONNEL 15028 EAST OLD LINCOLN WAY 51 YEAR OLD MALE BREATHING DIFFICULTIES SORE THROAT", "SHORTNESS OF BREATH"},
		{"MEDIC 134 126 MIDHURST ROAD NON BREATHER", "CARDIAC ARREST"},
		{"TOWNSHIP DUTY CREW RESCUE NEEDED 333 MORRELL FOR MALE WITH A HIGH HEART RATE", "HEART PROBLEM"},
		{"RESCUE 592 BLACKLICK EASTERN ROAD NW VEHICLE ACCIDENT", "CRASH WITH REPORTED INJURIES"},
		{"ENGINE 7 TRAFFIC COLLISION TRANSPORTATION INCIDENT I 90 EAST", "CRASH WITH REPORTED INJURIES"},
		{"MED 3 3690 CHESTNUT CHASE SICK PARTY", "GENERAL ILLNESS"},
		{"COUNTY DISPATCH JEFFERSON RESCUE RESPOND 112 SOUTH ELM STREET 78 YEAR OLD MALE WHO S ILL", "GENERAL ILLNESS"},
		{"RESCUE RESPOND 55 LINDEN FOR AN ILLNESS", "GENERAL ILLNESS"},
		{"ENGINE 3 LADDER 1 1277 PATRIOT PLACE SOUTHWEST ALARM FIRE ZONE 1", "FIRE ALARM DROP"},
		{"BOX ALARM FOR THE CLEVELAND DIVISION OF FIRE 4315 WEST 140", "STRUCTURE FIRE"},
		{"REPORT OF A POSSIBLE KITCHEN FIRE AT 1532 CEDAR LANE", "STRUCTURE FIRE"},
		{"WE HAVE A CALLER REQUESTING A WELLNESS CHECK ON A STAFF MEMBER AT 11508 WOODSTOCK AVENUE", "CHECK WELFARE"},
		{"CITY RESCUE RESPOND 815 NORTH STATE FOR NOSEBLEED", "BLEEDING"},
		{"CHESTER FIRE DEPARTMENT RESCUE SQUAD IS NEEDED FOR AN INVALID ASSIST", "LIFT ASSIST"},
		{"70 YEAR OLD FEMALE WHO SEEMS DISORIENTED AND CANNOT GET UP", "LIFT ASSIST"},
		{"ATTENTION COMMUNITY SQUAD REQUESTED 7763 STATE ROUTE 303 FOR A 74 YEAR OLD MALE CHANGE IN MENTAL STATUS", "ALTERED LEVEL OF CONSCIOUSNESS"},
		{"MENTAL HEALTH CRISIS AT 1930 TUCKER TRAIL", "MENTAL"},
	}
	for _, c := range cases {
		if got := ClassifyCallNature(c.tx, labels, terms, p2l); got != c.want {
			t.Fatalf("%q: got %q want %q", c.tx, got, c.want)
		}
	}
	if got := ClassifyCallNature("HE IS CONSCIOUS AND BREATHING", labels, terms, p2l); got == "SHORTNESS OF BREATH" {
		t.Fatalf("conscious and breathing must not classify SOB, got %q", got)
	}
	if got := ClassifyCallNature("CHECK THE AREA FOR A CAR ALARM THAT WAS GOING OFF", labels, terms, p2l); got == "FIRE ALARM DROP" {
		t.Fatalf("car alarm must not classify fire alarm, got %q", got)
	}
	if got := ClassifyCallNature("LINCOLN ELECTRIC FOR A FEMALE SHE FELL DOWN THE STEPS", labels, terms, p2l); got == "ELECTRICAL FIRE" {
		t.Fatalf("Lincoln Electric place must not classify electrical fire, got %q", got)
	}
	if got := ClassifyCallNature("69 YEAR OLD FEMALE DISABILITY BREATHING HISTORY OF COPD", labels, terms, p2l); got == "SHORTNESS OF BREATH" {
		t.Fatalf("disability breathing must not be assumed as breathing difficulty, got %q", got)
	}
}

func TestClassifyBatchTwoSpokenRecoveries(t *testing.T) {
	labels := []string{
		"NAUSEA/VOMITING", "CRASH WITH REPORTED INJURIES", "FRACTURE",
		"OVERDOSE", "MENTAL", "GENERAL ILLNESS", "LIFT ASSIST",
		"MISDIALED 911 CALL", "UNKNOWN PROBLEM",
	}
	terms := []string{
		"VOMITING", "NAUSEOUS", "ACCIDENT WITH INJURY", "TWO CAR ACCIDENT",
		"CRASH INVOLVING", "BROKEN LEG", "BROKEN LEGS", "BROKEN ARM",
		"BROKEN HIP", "BROKEN ANKLE", "BROKE HIS LEG", "FRACTURE",
		"TOOK OVER", "BUNCH OF PILLS", "OVERDOSE", "PANIC ATTACK",
		"HALLUCINATING", "HALLUCINATIONS", "SCHIZOPHRENIC", "MENTAL",
		"INFECTION", "SEPSIS", "SEPTIC", "LETHARGIC", "HEADACHE",
		"GENERAL ILLNESS", "UNABLE TO WALK", "CANNOT WALK", "LIFT ASSIST",
		"911 ACCIDENTAL", "ACCIDENTALLY DIALED", "MISDIALED 911 CALL",
	}
	p2l := map[string]string{
		"VOMITING": "NAUSEA/VOMITING", "NAUSEOUS": "NAUSEA/VOMITING",
		"ACCIDENT WITH INJURY": "CRASH WITH REPORTED INJURIES",
		"TWO CAR ACCIDENT":     "CRASH WITH REPORTED INJURIES",
		"CRASH INVOLVING":      "CRASH WITH REPORTED INJURIES",
		"BROKEN LEG":           "FRACTURE", "BROKEN LEGS": "FRACTURE",
		"BROKEN ARM": "FRACTURE", "BROKEN HIP": "FRACTURE",
		"BROKEN ANKLE": "FRACTURE", "BROKE HIS LEG": "FRACTURE",
		"FRACTURE": "FRACTURE", "TOOK OVER": "OVERDOSE",
		"BUNCH OF PILLS": "OVERDOSE", "OVERDOSE": "OVERDOSE",
		"PANIC ATTACK": "MENTAL", "HALLUCINATING": "MENTAL",
		"HALLUCINATIONS": "MENTAL", "SCHIZOPHRENIC": "MENTAL",
		"MENTAL": "MENTAL", "INFECTION": "GENERAL ILLNESS",
		"SEPSIS": "GENERAL ILLNESS", "SEPTIC": "GENERAL ILLNESS",
		"LETHARGIC": "GENERAL ILLNESS", "HEADACHE": "GENERAL ILLNESS",
		"GENERAL ILLNESS": "GENERAL ILLNESS", "UNABLE TO WALK": "LIFT ASSIST",
		"CANNOT WALK": "LIFT ASSIST", "LIFT ASSIST": "LIFT ASSIST",
		"911 ACCIDENTAL": "MISDIALED 911 CALL", "ACCIDENTALLY DIALED": "MISDIALED 911 CALL",
		"MISDIALED 911 CALL": "MISDIALED 911 CALL",
	}
	for _, c := range []struct{ tx, want string }{
		{"8500 CANNON TOWNSHIP SQUAD PERSONNEL 8744 RIDGE ROAD FOR 57 YEAR OLD FEMALE VOMITING", "NAUSEA/VOMITING"},
		{"GREEN DISTRICT 3 EMS GREEN MEDIC 3 1651 EAST TURKEY FOOT LAKE ROAD SHEETZ ACCIDENT WITH INJURY", "CRASH WITH REPORTED INJURIES"},
		{"STATION 3 RESCUE NEEDED 923 PONTIAC FOR POSSIBLE BROKEN LEGS", "FRACTURE"},
		{"STATION 8 425 SENECA APARTMENT 301 POSSIBLY BROKEN ANKLE", "FRACTURE"},
		{"33 YEAR OLD FEMALE TOOK A BUNCH OF PILLS AND ALCOHOL AT 111 EAST WASHINGTON", "OVERDOSE"},
		{"8500 APPLE CREEK SQUAD PERSONNEL 124 CLAY STREET FOR A 73 YEAR OLD MALE POSSIBLE PANIC ATTACK", "MENTAL"},
		{"8500 CHIPPEWA TOWNSHIP SQUAD PERSONNEL 95 BLACK DRIVE 92 YEAR OLD FEMALE POSSIBLE SEPSIS", "GENERAL ILLNESS"},
		{"CONTRACT DUTY CREW RESCUE NEEDED 507 WESTBURY FOR A MALE WITH A HEADACHE", "GENERAL ILLNESS"},
		{"MANIFIER STATION 5 RESCUE IS NEEDED MALE UNABLE TO WALK 8116 MUNSON ROAD", "LIFT ASSIST"},
		{"911 ACCIDENTAL 776 WOODMERE DRIVE LINE DISCONNECTED", "MISDIALED 911 CALL"},
	} {
		if got := ClassifyCallNature(c.tx, labels, terms, p2l); got != c.want {
			t.Fatalf("%q: got %q want %q", c.tx, got, c.want)
		}
	}
	if got := ClassifyCallNature("CONTROL LET S JUST CRASH ONE STAND BY FOR FUEL PORT 16 GALLONS", labels, terms, p2l); got == "CRASH WITH REPORTED INJURIES" {
		t.Fatalf("Crash 1 fuel port must not classify crash, got %q", got)
	}
	if got := ClassifyCallNature("THE PASSENGER WINDOW IS BROKEN AT 5492 BLACK HAWK FOREST DRIVE", labels, terms, p2l); got == "FRACTURE" {
		t.Fatalf("broken window must not classify fracture, got %q", got)
	}
	burnLabels := []string{"BURNS", "BURNING COMPLAINT", "GENERAL ILLNESS"}
	burnTerms := []string{"BURNS", "OPEN BURN", "BURNING COMPLAINT", "ILL"}
	burnMap := map[string]string{
		"BURNS": "BURNS", "OPEN BURN": "BURNING COMPLAINT",
		"BURNING COMPLAINT": "BURNING COMPLAINT", "ILL": "GENERAL ILLNESS",
	}
	if got := ClassifyCallNature("GREEN ENGINE 2 ON ROUTE 216 SAMOA DRIVE FOR AN OPEN BURN", burnLabels, burnTerms, burnMap); got != "BURNING COMPLAINT" {
		t.Fatalf("open burn must classify BURNING COMPLAINT, got %q", got)
	}
	if got := ClassifyCallNature("BURN RESCUE 95 CLARENDON TROY ROAD 61 YEAR OLD FEMALE ILL", burnLabels, burnTerms, burnMap); got == "BURNS" {
		t.Fatalf("Burn Rescue department must not classify BURNS, got %q", got)
	}
}
