package main

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // embed the zone database so IANA names work on minimal hosts/containers
)

// Per-instance timezone and prune time.
//
// A host can run many instances (one per scanner system), each recording a different
// area. timeZone makes that instance show and announce call times in its own zone;
// pruneTime lets each instance clean its database at a chosen time of day instead of
// every instance doing it at startup and on the hour together.

// validTimeZone reports whether s is empty (server local time) or a known IANA zone.
func validTimeZone(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	// "Local" is accepted by time.LoadLocation but is ambiguous in a config shared across hosts.
	if strings.EqualFold(s, "local") {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// normalizePruneTime accepts "" (legacy behavior) or "H:MM"/"HH:MM" (24-hour) and
// returns the canonical "HH:MM".
func normalizePruneTime(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	var h, m int
	var extra string
	n, _ := fmt.Sscanf(s, "%d:%d%s", &h, &m, &extra)
	if n != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", h, m), true
}

// Location returns the instance's configured zone, or the server's local zone when
// none is set or the stored name cannot be loaded.
func (options *Options) Location() *time.Location {
	options.mutex.Lock()
	name := options.TimeZone
	options.mutex.Unlock()
	return locationFor(name)
}

func locationFor(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.Local
}

// pruneSlot returns the configured prune time of day in loc, as of now's date.
func pruneSlot(now time.Time, hhmm string) (time.Time, bool) {
	var h, m int
	if n, _ := fmt.Sscanf(hhmm, "%d:%d", &h, &m); n != 2 {
		return time.Time{}, false
	}
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location()), true
}
