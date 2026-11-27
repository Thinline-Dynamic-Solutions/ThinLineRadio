// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Alert tones: the short beeps and alternating hi-lo (warble) tones many
// agencies use instead of two-tone paging. The main detector only keeps steady
// tones of at least 0.4 s and splits on frequency jumps, so these get their own
// pass over short frames.

package main

import (
	"fmt"
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	alertToneWindow     = 512  // 32 ms @ 16 kHz
	alertToneHop        = 128  // 8 ms
	alertToneMinRun     = 0.06 // seconds a frequency must hold to count as a beep or half-cycle
	alertToneMaxBeep    = 1.5  // longer steady tones are long tones, not beeps
	alertToneMaxHalf    = 0.8  // longest single half of a warble cycle
	alertToneMaxGap     = 0.25 // silence allowed inside a warble
	alertToneBeepGap    = 1.0  // beeps further apart than this start a new group
	alertTonePurity     = 0.6  // share of band energy around the peak; speech stays well below
	alertToneLevelDB    = -30  // frames this far below the loudest frame are ignored
	alertToneMinTolHz   = 15   // short frames estimate frequency less precisely than the main detector
	alertToneMaxSeconds = 20
)

// AlertToneSpec describes a beep or warble alert tone.
type AlertToneSpec struct {
	Kind       string  `json:"kind"`                 // "beeps" or "warble"
	Frequency  float64 `json:"frequency"`            // beep frequency, or one warble frequency
	Frequency2 float64 `json:"frequency2,omitempty"` // the other warble frequency
	MinCount   int     `json:"minCount,omitempty"`   // beeps in a row, or warble frequency changes
}

func (s *AlertToneSpec) minCount() int {
	if s.MinCount > 0 {
		return s.MinCount
	}
	if s.Kind == "warble" {
		return 4
	}
	return 1
}

func (s *AlertToneSpec) valid() bool {
	if s == nil || s.Frequency <= 0 {
		return false
	}
	return s.Kind == "beeps" || (s.Kind == "warble" && s.Frequency2 > 0)
}

// signature ties a detected alert tone back to the pattern that found it.
func (s *AlertToneSpec) signature() string {
	return fmt.Sprintf("%s:%.1f:%.1f:%d", s.Kind, s.Frequency, s.Frequency2, s.minCount())
}

func alertToneTolerance(toneSetTolerance float64) float64 {
	tol := toneSetTolerance
	if tol < 1.0 {
		tol *= 500.0
	}
	return math.Max(tol, alertToneMinTolHz)
}

// alertToneFrames returns the dominant frequency of each frame, or 0 when the
// frame is quiet or not a clean single tone.
func alertToneFrames(samples []float64, sampleRate int) []float64 {
	if max := alertToneMaxSeconds * sampleRate; len(samples) > max {
		samples = samples[:max]
	}
	if len(samples) < alertToneWindow {
		return nil
	}
	hann := make([]float64, alertToneWindow)
	for i := range hann {
		hann[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(alertToneWindow-1))
	}
	fft := fourier.NewFFT(alertToneWindow)
	lo := int(250.0 * alertToneWindow / float64(sampleRate))
	hi := int(3000.0 * alertToneWindow / float64(sampleRate))
	if lo < 2 {
		lo = 2
	}

	n := (len(samples)-alertToneWindow)/alertToneHop + 1
	freqs := make([]float64, n)
	levels := make([]float64, n)
	buf := make([]float64, alertToneWindow)
	power := make([]float64, alertToneWindow/2+1)
	loudest := 0.0
	for f := 0; f < n; f++ {
		start := f * alertToneHop
		for i := range buf {
			buf[i] = samples[start+i] * hann[i]
		}
		coeff := fft.Coefficients(nil, buf)
		total := 0.0
		peak := lo
		for k := lo - 2; k <= hi+2 && k < len(coeff); k++ {
			power[k] = math.Pow(cmplx.Abs(coeff[k]), 2)
			if k >= lo && k <= hi {
				total += power[k]
				if power[k] > power[peak] {
					peak = k
				}
			}
		}
		if total <= 0 {
			continue
		}
		around := power[peak-2] + power[peak-1] + power[peak] + power[peak+1] + power[peak+2]
		levels[f] = total
		if total > loudest {
			loudest = total
		}
		if around/total < alertTonePurity {
			continue
		}
		offset := parabolicInterpolate(math.Log(power[peak-1]+1e-12), math.Log(power[peak]+1e-12), math.Log(power[peak+1]+1e-12))
		freqs[f] = (float64(peak) + offset) * float64(sampleRate) / alertToneWindow
	}
	floor := loudest * math.Pow(10, alertToneLevelDB/10.0)
	for f := range freqs {
		if levels[f] < floor {
			freqs[f] = 0
		}
	}
	return freqs
}

type alertToneRun struct {
	label      int // 1 = Frequency, 2 = Frequency2
	start, end float64
}

// alertToneRuns labels frames by which spec frequency they hold and returns
// runs long enough to count. Single-frame dropouts inside a run are bridged.
func alertToneRuns(freqs []float64, sampleRate int, spec *AlertToneSpec, tol float64) []alertToneRun {
	labels := make([]int, len(freqs))
	for i, f := range freqs {
		switch {
		case f <= 0:
		case math.Abs(f-spec.Frequency) <= tol:
			labels[i] = 1
		case spec.Kind == "warble" && math.Abs(f-spec.Frequency2) <= tol:
			labels[i] = 2
		}
	}
	for i := 1; i+1 < len(labels); i++ {
		if labels[i] == 0 && labels[i-1] != 0 && labels[i-1] == labels[i+1] {
			labels[i] = labels[i-1]
		}
	}
	hop := float64(alertToneHop) / float64(sampleRate)
	runs := []alertToneRun{}
	for i := 0; i < len(labels); {
		if labels[i] == 0 {
			i++
			continue
		}
		j := i
		for j < len(labels) && labels[j] == labels[i] {
			j++
		}
		run := alertToneRun{label: labels[i], start: float64(i) * hop, end: float64(j) * hop}
		if run.end-run.start >= alertToneMinRun {
			runs = append(runs, run)
		}
		i = j
	}
	return runs
}

// matchAlertTone finds the first span of runs that satisfies spec.
func matchAlertTone(runs []alertToneRun, spec *AlertToneSpec) (start, end float64, ok bool) {
	need := spec.minCount()
	if spec.Kind == "beeps" {
		count := 0
		for i, r := range runs {
			if r.end-r.start > alertToneMaxBeep {
				count = 0
				continue
			}
			if count == 0 || r.start-runs[i-1].end > alertToneBeepGap {
				count = 0
				start = r.start
			}
			count++
			if count >= need {
				return start, r.end, true
			}
		}
		return 0, 0, false
	}

	changes := 0
	for i, r := range runs {
		if r.end-r.start > alertToneMaxHalf {
			changes = -1
			continue
		}
		if changes < 0 || i == 0 || r.start-runs[i-1].end > alertToneMaxGap {
			changes = 0
			start = r.start
			continue
		}
		if r.label != runs[i-1].label {
			changes++
		}
		if changes >= need {
			return start, r.end, true
		}
	}
	return 0, 0, false
}

// detectAlertTones runs every alert tone pattern in toneSets over the audio and
// returns one tone per pattern found, tagged with the pattern's signature.
func (detector *ToneDetector) detectAlertTones(samples []float64, sampleRate int, toneSets []ToneSet) []Tone {
	var freqs []float64
	seen := map[string]bool{}
	tones := []Tone{}
	for _, ts := range expandTonePatterns(toneSets) {
		spec := ts.AlertTone
		if !spec.valid() || seen[spec.signature()] {
			continue
		}
		seen[spec.signature()] = true
		if freqs == nil {
			if freqs = alertToneFrames(samples, sampleRate); freqs == nil {
				return tones
			}
		}
		runs := alertToneRuns(freqs, sampleRate, spec, alertToneTolerance(ts.Tolerance))
		if start, end, ok := matchAlertTone(runs, spec); ok {
			fmt.Printf("alert tone matched - %s %s %.0f Hz from %.2fs to %.2fs\n", ts.Label, spec.Kind, spec.Frequency, start, end)
			tones = append(tones, Tone{
				Frequency: spec.Frequency,
				StartTime: start,
				EndTime:   end,
				Duration:  end - start,
				ToneType:  "Alert",
				Pattern:   spec.signature(),
			})
		}
	}
	return tones
}

func hasAlertTone(detected *ToneSequence, spec *AlertToneSpec) bool {
	if detected == nil || !spec.valid() {
		return false
	}
	sig := spec.signature()
	for _, t := range detected.Tones {
		if t.Pattern == sig {
			return true
		}
	}
	return false
}
