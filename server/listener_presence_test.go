package main

import (
	"database/sql"
	"testing"
)

func TestListenerClientKind(t *testing.T) {
	cases := map[string]string{
		"":                                "unknown",
		"Mozilla/5.0 (Windows NT 10.0)":   "web",
		"Dart/3.4 (dart:io)":              "mobile",
		"ThinLineRadio/1.2":               "mobile",
		"okhttp/4.12.0":                   "mobile",
		"CountyPulse/2 CFNetwork Darwin":  "mobile",
	}
	for ua, want := range cases {
		if got := listenerClientKind(ua); got != want {
			t.Errorf("listenerClientKind(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestClipListenerText(t *testing.T) {
	if got := clipListenerText("  abc  ", 10); got != "abc" {
		t.Fatalf("trim: %q", got)
	}
	if got := clipListenerText("abcdef", 3); got != "abc" {
		t.Fatalf("clip: %q", got)
	}
}

func TestListenerRecordDBRequiresOptIn(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 dbname=unused sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	controller := &Controller{Options: &Options{}, Database: &Database{Sql: db}}

	if controller.listenerRecordDB() != nil {
		t.Fatal("visits recorded while Record listener visits is off")
	}
	if controller.listenerDB() == nil {
		t.Fatal("existing visits should stay readable while recording is off")
	}
	controller.Options.RecordListenerVisits = true
	if controller.listenerRecordDB() != db {
		t.Fatal("visits not recorded after opting in")
	}
}
