package main

import (
	"log"
	"sort"
)

// Sites stamp each recording with their own clock, and Core matches copies from
// different sites by those times. A site whose clock is a second off makes a
// short call look like the next key-up, so both copies are stored. Each site's
// offset from the server is measured from its call.started reports and its
// times are moved onto the server's clock before matching.
const (
	coreClockSamples    = 64
	coreClockMinSamples = 10
	coreClockMaxSkewMs  = int64(10 * 60 * 1000)
	coreClockLogStepMs  = int64(100)
)

type coreSiteClock struct {
	samples  []int64
	next     int
	offsetMs int64
	loggedMs int64
	logged   bool
}

// add records how far a recording's start, on the site's clock, is ahead of the
// moment the server heard about it. call.started is sent as a recording begins,
// so that is the site's clock offset less the network and processing delay.
func (clock *coreSiteClock) add(sampleMs int64) {
	if sampleMs > coreClockMaxSkewMs || sampleMs < -coreClockMaxSkewMs {
		return
	}
	if len(clock.samples) < coreClockSamples {
		clock.samples = append(clock.samples, sampleMs)
	} else {
		clock.samples[clock.next] = sampleMs
		clock.next = (clock.next + 1) % coreClockSamples
	}
	if len(clock.samples) < coreClockMinSamples {
		return
	}
	// Delays only make a sample smaller: a report re-sent on a later pass or held
	// through a reconnect is late. A high percentile tracks the prompt reports
	// while ignoring the odd sample that is off the other way.
	sorted := append([]int64(nil), clock.samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	clock.offsetMs = sorted[len(sorted)*9/10]
}

func (clock *coreSiteClock) ready() bool {
	return len(clock.samples) >= coreClockMinSamples
}

// serverStartLocked converts a site's start time to the server's clock, and
// learns the site's offset from the first report of each recording.
func (core *coreCoordinator) serverStartLocked(keyId uint64, siteStartMs int64, receivedMs int64, firstReport bool) int64 {
	if core.clocks == nil {
		core.clocks = map[uint64]*coreSiteClock{}
	}
	clock := core.clocks[keyId]
	if clock == nil {
		clock = &coreSiteClock{}
		core.clocks[keyId] = clock
	}
	if firstReport {
		clock.add(siteStartMs - receivedMs)
		if clock.ready() && (!clock.logged || abs64(clock.offsetMs-clock.loggedMs) >= coreClockLogStepMs) {
			clock.logged, clock.loggedMs = true, clock.offsetMs
			log.Printf("core: %s clock is %dms %s the server, adjusting its call times", core.siteName(keyId),
				abs64(clock.offsetMs), map[bool]string{true: "ahead of", false: "behind"}[clock.offsetMs > 0])
		}
	}
	if !clock.ready() {
		return siteStartMs
	}
	return siteStartMs - clock.offsetMs
}
