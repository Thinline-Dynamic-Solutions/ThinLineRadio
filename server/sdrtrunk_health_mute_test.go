// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"testing"
	"time"
)

func TestSdrtrunkAlertsMuted(t *testing.T) {
	now := time.Now()
	cases := []struct {
		until int64
		muted bool
	}{
		{0, false},
		{sdrtrunkAlertsOff, true},
		{now.Add(time.Hour).UnixMilli(), true},
		{now.Add(-time.Minute).UnixMilli(), false},
	}
	for _, c := range cases {
		record := &SdrtrunkHealthRecord{AlertsMutedUntil: c.until}
		if got := record.alertsMuted(now); got != c.muted {
			t.Fatalf("until %d: muted %t, want %t", c.until, got, c.muted)
		}
	}
}

func TestSdrtrunkReportKeepsMute(t *testing.T) {
	controller := &Controller{Logs: NewLogs()}
	controller.storeSdrtrunkHealth(&SdrtrunkHealthRecord{ApiKeyId: 7, Reporting: true})
	if err := controller.setSdrtrunkAlertsMuted(7, sdrtrunkAlertsOff); err != nil {
		t.Fatal(err)
	}
	controller.storeSdrtrunkHealth(&SdrtrunkHealthRecord{ApiKeyId: 7, Reporting: false})
	if !controller.sdrtrunkAlertsMuted(7) {
		t.Fatal("a new report must not turn a site's alerts back on")
	}
	if err := controller.setSdrtrunkAlertsMuted(8, sdrtrunkAlertsOff); err == nil {
		t.Fatal("muting a site with no reports must fail")
	}
}

func TestSdrtrunkDownFor(t *testing.T) {
	for age, want := range map[time.Duration]string{
		95 * time.Second:            "95 seconds",
		31 * time.Minute:            "31 minutes",
		3*time.Hour + 5*time.Minute: "3 hours 5 minutes",
		72 * time.Hour:              "3 days",
	} {
		if got := sdrtrunkDownFor(age); got != want {
			t.Fatalf("%s: got %q, want %q", age, got, want)
		}
	}
}
