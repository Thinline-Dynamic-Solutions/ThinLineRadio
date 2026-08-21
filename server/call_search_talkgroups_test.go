package main

import (
	"testing"
)

func TestCallsSearchOptionsResolvedTalkgroups(t *testing.T) {
	o := NewCallSearchOptions()
	if refs := o.resolvedTalkgroupRefs(); len(refs) != 0 {
		t.Fatalf("empty options refs=%v", refs)
	}

	o.Talkgroup = uint(12)
	if refs := o.resolvedTalkgroupRefs(); len(refs) != 1 || refs[0] != 12 {
		t.Fatalf("scalar talkgroup refs=%v", refs)
	}

	o.Talkgroups = []uint{7, 9, 7, 0}
	if refs := o.resolvedTalkgroupRefs(); len(refs) != 2 || refs[0] != 7 || refs[1] != 9 {
		t.Fatalf("talkgroups array prefers list and dedupes, got %v", refs)
	}

	from := NewCallSearchOptions().fromMap(map[string]any{
		"system":     float64(100),
		"talkgroups": []any{float64(3), float64(5), float64(3)},
	})
	if _, ok := from.System.(uint); !ok {
		t.Fatalf("system not parsed")
	}
	refs := from.resolvedTalkgroupRefs()
	if len(refs) != 2 || refs[0] != 3 || refs[1] != 5 {
		t.Fatalf("fromMap talkgroups=%v", refs)
	}
}
