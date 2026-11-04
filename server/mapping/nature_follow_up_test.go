// Copyright (C) 2026 Thinline Dynamic Solutions

package mapping

import "testing"

func TestFollowUpRequiresFollowUpWording(t *testing.T) {
	labels := []string{"FOLLOW UP", "TRAFFIC STOP", "SERVICE CALL"}
	cleared := []string{
		"FOR THE LIGHT TEAM I VE GOT A 2002 TAN CHEVY SILVERADO COMING BACK TO HER NAME",
		"THAT VIN COMES BACK A 2007 DODGE TO MAXWELL MOORE THE 28 ON THAT SHOULD BE 637830 ALPHA 637830 ALPHA",
		"YOU CAN SHOW AS CLEARED FROM SCENE AND EN ROUTE TO METAVIEW",
		"SP 211 CONTROL I VE GOT A 27 THROUGH WEST VIRGINIA WHEN YOU RE READY AND ALSO COULD YOU RUN THAT 28 MAKE SURE IT S NOT STOLEN",
		"GO AHEAD TROY YOU HAVE TO GO SIR FOLLOW ME TO 1819 FOLLOW ME DETAIL 104",
	}
	for _, tx := range cleared {
		if got := SanitizeCallNatureAssignment(tx, "FOLLOW UP", labels); got != "" {
			t.Errorf("got %q want empty for %q", got, tx)
		}
	}
	kept := []string{
		"88 TO DISPATCH GO AHEAD CAN YOU SHOW ME A ROUTE TO 56 COUNTY ROAD 464 FOR A FOLLOW UP TEN FOUR",
		"8 P GO AHEAD SHOW ME EN ROUTE TO 501 PARK TRYING TO LOCATE SOME FAMILY MEMBERS JUST FOLLOWING UP ON AN INCIDENT",
		"Need a unit for a follow-up at 12 Main Street.",
	}
	for _, tx := range kept {
		if got := SanitizeCallNatureAssignment(tx, "FOLLOW UP", labels); got != "FOLLOW UP" {
			t.Errorf("got %q want FOLLOW UP for %q", got, tx)
		}
	}
}
