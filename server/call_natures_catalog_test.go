package main

import (
	"strings"
	"testing"
)

func TestParseCallNatureCatalog(t *testing.T) {
	if _, err := parseCallNatureCatalog([]byte(`{"natures":[{"label":"FALL","phrases":["FELL"]}]}`)); err == nil {
		t.Fatal("short catalog should be rejected")
	}
	var b strings.Builder
	b.WriteString(`{"name":"ThinLine call classifications","version":"abc","natures":[`)
	for i := 0; i < callNatureCatalogMinRows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"label":"N","phrases":["N"],"enabled":true,"order":0,"expireMinutes":0}`)
	}
	b.WriteString(`]}`)
	doc, err := parseCallNatureCatalog([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != "abc" || len(doc.Natures) != callNatureCatalogMinRows {
		t.Fatalf("doc %+v", doc)
	}
}
