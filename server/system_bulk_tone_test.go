package main

import "testing"

func TestApplyBulkToneDetectionDoesNotWipeWhenBulkOff(t *testing.T) {
	tagID := uint64(7)
	tgOn := &Talkgroup{Id: 1, TagId: tagID, ToneDetectionEnabled: true}
	tgOff := &Talkgroup{Id: 2, TagId: tagID, ToneDetectionEnabled: false}
	tgOther := &Talkgroup{Id: 3, TagId: 99, ToneDetectionEnabled: true}

	system := &System{
		BulkToneDetectionEnabled: false,
		BulkToneDetectionTagIds:  []uint64{tagID},
		Talkgroups: &Talkgroups{
			List: []*Talkgroup{tgOn, tgOff, tgOther},
		},
	}

	system.applyBulkToneDetection()

	if !tgOn.ToneDetectionEnabled {
		t.Fatal("bulk off must not clear a talkgroup that already has tone detection enabled")
	}
	if tgOff.ToneDetectionEnabled {
		t.Fatal("bulk off must not enable talkgroups")
	}
	if !tgOther.ToneDetectionEnabled {
		t.Fatal("unrelated talkgroup must stay unchanged")
	}
}

func TestApplyBulkToneDetectionEnablesMatchingTagsWhenBulkOn(t *testing.T) {
	tagID := uint64(7)
	tgMatch := &Talkgroup{Id: 1, TagId: tagID, ToneDetectionEnabled: false}
	tgOther := &Talkgroup{Id: 2, TagId: 99, ToneDetectionEnabled: false}

	system := &System{
		BulkToneDetectionEnabled: true,
		BulkToneDetectionTagIds:  []uint64{tagID},
		Talkgroups: &Talkgroups{
			List: []*Talkgroup{tgMatch, tgOther},
		},
	}

	system.applyBulkToneDetection()

	if !tgMatch.ToneDetectionEnabled {
		t.Fatal("bulk on should enable talkgroups with matching tags")
	}
	if tgOther.ToneDetectionEnabled {
		t.Fatal("bulk on should not enable talkgroups outside selected tags")
	}
}
