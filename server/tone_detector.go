// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT EVEN THE IMPLIED WARRANTY OF MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>
//
// Tone detection improvements inspired by techniques from icad_tone_detection
// (Apache 2.0 License, Copyright 2024 thegreatcodeholio)
// GitHub: https://github.com/thegreatcodeholio/icad_tone_detection
// Techniques include: dynamic noise floor estimation (20th percentile method),
// parabolic peak interpolation for sub-bin accuracy, force-split detection
// for frequency drift, and optimized bandpass filtering for analog channels.

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/cmplx"
	"os/exec"
	"sort"
	"strings"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Tone represents a detected tone with frequency and timing information
type Tone struct {
	Frequency float64 `json:"frequency"` // Hz
	StartTime float64 `json:"startTime"` // seconds from start of audio
	EndTime   float64 `json:"endTime"`   // seconds from start of audio
	Duration  float64 `json:"duration"`  // seconds
	ToneType  string  `json:"toneType"`  // Type of tone: "A", "B", "Long", or "" if matched multiple/none
	Magnitude float64 `json:"magnitude,omitempty"` // FFT peak magnitude (internal scoring; not persisted)
}

// ToneSet represents a configured set of tones for a talkgroup
type ToneSet struct {
	Id          string    `json:"id"`          // Unique identifier
	Label       string    `json:"label"`       // User-friendly name (e.g., "Fire Dept", "EMS")
	ATone       *ToneSpec `json:"aTone"`       // First tone specification (optional)
	BTone       *ToneSpec `json:"bTone"`       // Second tone specification (optional)
	LongTone    *ToneSpec `json:"longTone"`    // Long tone specification (optional)
	Tolerance   float64   `json:"tolerance"`   // Frequency tolerance in Hz (default: ±10Hz)
	MinDuration float64   `json:"minDuration"` // Minimum duration in seconds to be considered valid
	// TonesToActive downstream forwarding (per tone set)
	DownstreamEnabled bool   `json:"downstreamEnabled"` // Forward alerts for this tone set to an external endpoint
	DownstreamURL     string `json:"downstreamURL"`     // Destination URL (TonesToActive server)
	DownstreamAPIKey  string `json:"downstreamAPIKey"`  // API key sent in X-API-Key header
	// Incident mapping jurisdiction (takes priority over parent talkgroup geo when this tone set matches)
	GeoCity         string  `json:"geoCity"`
	GeoLat          float64 `json:"geoLat"`
	GeoLon          float64 `json:"geoLon"`
	GeoRadiusMiles  float64 `json:"geoRadiusMiles"`
	LocationContext string  `json:"locationContext"`
}

// ToneSpec defines the expected frequency and duration ranges for a tone
type ToneSpec struct {
	Frequency   float64 `json:"frequency"`   // Expected frequency in Hz
	MinDuration float64 `json:"minDuration"` // Minimum duration in seconds
	MaxDuration float64 `json:"maxDuration"` // Maximum duration in seconds (0 = unlimited)
}

// ToneSequence represents detected tones in a call
type ToneSequence struct {
	Tones           []Tone     `json:"tones"`           // Array of detected tones
	Duration        float64    `json:"duration"`        // Total sequence duration
	ATone           *Tone      `json:"aTone"`           // First tone (if present)
	BTone           *Tone      `json:"bTone"`           // Second tone (if present)
	LongTone        *Tone      `json:"longTone"`        // Extended tone (if present)
	HasTones        bool       `json:"hasTones"`        // Quick flag for filtering
	MatchedToneSet  *ToneSet   `json:"matchedToneSet"`  // Which configured tone set matched the full pattern (if any)
	MatchedToneSets []*ToneSet `json:"matchedToneSets"` // All configured tone sets that matched any detected tone
}

// PendingToneSequence represents tones detected on a call that are waiting to be attached to a subsequent voice call
type PendingToneSequence struct {
	ToneSequence *ToneSequence
	CallId       uint64
	Timestamp    int64 // Unix millisecond timestamp when tones were detected
	SystemId     uint64
	TalkgroupId  uint64
	Locked       bool // When true, prevents new tones from merging (claimed by transcribing call)

	// Cross-talkgroup fields (Scenario 2: tones on TGID A, voice on TGID B)
	// When non-zero, WindowSeconds overrides the global pendingToneTimeoutMinutes for this entry.
	WindowSeconds uint
	// MinVoiceDurationSeconds filters out mic-click false positives on the linked voice channel.
	// A voice call shorter than this many seconds will not claim these pending tones.
	MinVoiceDurationSeconds uint
	// CrossTalkgroupSourceKey is set on cross-talkgroup watch entries. When this entry is consumed
	// it is used to also clean up the source talkgroup's own pending-tones entry so a second alert
	// is not fired if a voice call later arrives on the original (tone) talkgroup.
	CrossTalkgroupSourceKey string
}

// ToneDetector handles tone detection in audio calls
type ToneDetector struct {
	// Configuration
	SampleRate      int     // Audio sample rate (Hz) - typically 8000 or 16000
	WindowSize      int     // FFT window size
	MinToneDuration float64 // Minimum duration to consider a tone valid (seconds)
	FrequencyRange  struct {
		Min float64 // Minimum frequency to detect (Hz)
		Max float64 // Maximum frequency to detect (Hz)
	}
}

// NewToneDetector creates a new tone detector with default settings
func NewToneDetector() *ToneDetector {
	return &ToneDetector{
		SampleRate:      16000, // 16kHz sample rate (can capture up to 8kHz via Nyquist, enough for 0-5000 Hz)
		WindowSize:      2048,  // FFT window size
		MinToneDuration: 0.4,   // Minimum 400ms (Lordstown A-tones on compressed dispatch MP3)
		FrequencyRange: struct {
			Min float64
			Max float64
		}{
			Min: 0.0,    // Can detect from 0 Hz
			Max: 5000.0, // Up to 5000 Hz
		},
	}
}

// Detect analyzes audio for tone patterns using FFT analysis
func (detector *ToneDetector) Detect(audio []byte, audioMime string, toneSets []ToneSet) (*ToneSequence, error) {
	if len(audio) < 1000 {
		return &ToneSequence{Tones: []Tone{}, HasTones: false}, nil
	}

	samples, sampleRate, err := detector.decodeAudioForDetect(audio)
	if err != nil {
		return nil, err
	}

	if len(samples) < 100 {
		return &ToneSequence{Tones: []Tone{}, HasTones: false}, nil
	}

	detectedTones := detector.analyzeFrequencies(samples, sampleRate, toneSets, false)

	// Log tone detection analysis
	fmt.Printf("tone detection: analyzed %d samples at %d Hz, found %d potential tone detections\n", len(samples), sampleRate, len(detectedTones))

	if len(detectedTones) == 0 {
		return &ToneSequence{Tones: []Tone{}, HasTones: false}, nil
	}

	// Build tone sequence
	sequence := &ToneSequence{
		Tones:    detectedTones,
		HasTones: true,
		Duration: float64(len(samples)) / float64(sampleRate),
	}

	// Identify ATone, BTone, LongTone from tone-set match or sequential order.
	sort.Slice(detectedTones, func(i, j int) bool {
		return detectedTones[i].StartTime < detectedTones[j].StartTime
	})
	for i := range detectedTones {
		tone := &detectedTones[i]
		switch tone.ToneType {
		case "A":
			if sequence.ATone == nil {
				sequence.ATone = tone
			}
		case "B":
			if sequence.BTone == nil {
				sequence.BTone = tone
			}
		case "Long":
			if sequence.LongTone == nil {
				sequence.LongTone = tone
			}
		}
	}
	if sequence.ATone == nil && len(detectedTones) > 0 {
		sequence.ATone = &detectedTones[0]
	}
	if sequence.BTone == nil && len(detectedTones) > 1 {
		sequence.BTone = &detectedTones[1]
	}

	return sequence, nil
}

// toneAnalysisMaxSeconds caps FFT analysis for auto-learn on long stacked-page clips.
const toneAnalysisMaxSeconds = 20.0

// tonePeakReferenceSeconds: silence gate uses early paging audio only so later dispatch
// voice on the same clip does not hide quiet lead-in tones.
const tonePeakReferenceSeconds = 3.0

// Shared detection gates for production Detect and auto-learn Discover.
// The STFT engine parameters (window, hop, force-split, tolerance) live in tone_stft.go.
const (
	toneDetectMinDurationSec     = 0.4
	toneDetectSilenceBelowGlobal = -42.0
	toneDetectSNRAboveNoise      = 3.0
	toneDetectMagnitudeThreshold = 0.008
	toneDetectPagingPadSec       = 0.2
	toneDetectPagingGateDB       = -38.0
	toneDetectHarmonicOnsetSec   = 0.2 // same-onset window for harmonic artifact rejection
)

// cropSamplesToPagingRegion trims leading/trailing silence using a short-hop energy envelope.
func cropSamplesToPagingRegion(samples []float64, sampleRate int) []float64 {
	const envWindow = 512
	const envHop = 128
	if len(samples) < envWindow*2 {
		return samples
	}

	type envFrame struct {
		start int
		rms   float64
	}
	var frames []envFrame
	global := 0.0
	for start := 0; start+envWindow <= len(samples); start += envHop {
		var sum float64
		for i := start; i < start+envWindow; i++ {
			v := samples[i]
			sum += v * v
		}
		rms := math.Sqrt(sum / float64(envWindow))
		if rms > global {
			global = rms
		}
		frames = append(frames, envFrame{start: start, rms: rms})
	}
	if global < 1e-20 || len(frames) == 0 {
		return samples
	}

	threshold := global * math.Pow(10, toneDetectPagingGateDB/20.0)
	first, last := -1, -1
	for i, f := range frames {
		if f.rms >= threshold {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return samples
	}

	startSample := frames[first].start
	endSample := frames[last].start + envWindow
	pad := int(toneDetectPagingPadSec * float64(sampleRate))
	startSample -= pad
	endSample += pad
	if startSample < 0 {
		startSample = 0
	}
	if endSample > len(samples) {
		endSample = len(samples)
	}
	if endSample <= startSample {
		return samples
	}
	return samples[startSample:endSample]
}

type toneFreqDetection struct {
	frequency float64
	startTime float64
	endTime   float64
	magnitude float64
}

type mergedDetection struct {
	frequency   float64
	startTime   float64
	endTime     float64
	magnitude   float64
	count       int
	freqHistory []float64
}

func pruneHarmonicMergedDetections(merged []mergedDetection) []mergedDetection {
	if len(merged) < 2 {
		return merged
	}
	drop := make([]bool, len(merged))
	for i := range merged {
		for j := range merged {
			if i == j || drop[j] {
				continue
			}
			loFreq, hiFreq := merged[i].frequency, merged[j].frequency
			lo, hi := merged[i], merged[j]
			idxHi := j
			if loFreq > hiFreq {
				lo, hi = merged[j], merged[i]
				idxHi = i
			}
			if hi.frequency <= lo.frequency+5 {
				continue
			}
			if math.Abs(hi.startTime-lo.startTime) > toneDetectHarmonicOnsetSec {
				continue
			}
			if !tonesTimeOverlap(
				Tone{StartTime: lo.startTime, EndTime: lo.endTime},
				Tone{StartTime: hi.startTime, EndTime: hi.endTime},
				0.1,
			) {
				continue
			}
			for _, n := range []float64{2, 3, 4} {
				if isIntegerHarmonicRatio(hi.frequency, lo.frequency, n) && hi.magnitude <= lo.magnitude*1.15 {
					drop[idxHi] = true
					break
				}
			}
		}
	}
	out := make([]mergedDetection, 0, len(merged))
	for i, md := range merged {
		if !drop[i] {
			out = append(out, md)
		}
	}
	return out
}

// decodeAudioForDetect decodes call audio to mono PCM for production Detect and auto-learn Discover.
func (detector *ToneDetector) decodeAudioForDetect(audio []byte) ([]float64, int, error) {
	ffArgs := []string{
		"-i", "pipe:0",
		"-ar", "16000",
		"-ac", "1",
		"-af", "highpass=f=200,lowpass=f=3000,dynaudnorm",
		"-f", "wav",
		"-loglevel", "error",
		"pipe:1",
	}

	ffCmd := exec.Command("ffmpeg", ffArgs...)
	stdin, err := ffCmd.StdinPipe()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create stdin pipe: %v", err)
	}

	var wavData bytes.Buffer
	var ffErr bytes.Buffer
	ffCmd.Stdout = &wavData
	ffCmd.Stderr = &ffErr

	if err := ffCmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	go func() {
		defer stdin.Close()
		stdin.Write(audio)
	}()

	if err := ffCmd.Wait(); err != nil {
		return nil, 0, fmt.Errorf("ffmpeg conversion failed: %v, stderr: %s", err, ffErr.String())
	}

	if wavData.Len() == 0 {
		return nil, 0, fmt.Errorf("ffmpeg produced no output")
	}

	return detector.parseWAV(wavData.Bytes())
}

// Discover analyzes audio and returns all sustained tones (matched or not) for auto-learn.
func (detector *ToneDetector) Discover(audio []byte, audioMime string) ([]Tone, error) {
	if len(audio) < 1000 {
		return []Tone{}, nil
	}

	samples, sampleRate, err := detector.decodeAudioForDetect(audio)
	if err != nil {
		return nil, err
	}
	if len(samples) < 100 {
		return []Tone{}, nil
	}

	// Same decode and FFT analysis as production Detect; includeUnmatched returns tones without a tone-set match.
	return detector.analyzeFrequencies(samples, sampleRate, nil, true), nil
}

// parseWAV parses WAV file and returns PCM samples and sample rate
func (detector *ToneDetector) parseWAV(wavData []byte) ([]float64, int, error) {
	if len(wavData) < 44 {
		return nil, 0, fmt.Errorf("WAV file too short")
	}

	// Check for WAV header
	if string(wavData[0:4]) != "RIFF" || string(wavData[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a valid WAV file")
	}

	// Read sample rate
	sampleRate := int(binary.LittleEndian.Uint32(wavData[24:28]))
	channels := int(binary.LittleEndian.Uint16(wavData[22:24]))
	bitsPerSample := int(binary.LittleEndian.Uint16(wavData[34:36]))

	// Find data chunk
	dataOffset := 44
	for i := 12; i < len(wavData)-8; i++ {
		if string(wavData[i:i+4]) == "data" {
			dataOffset = i + 8
			break
		}
	}

	audioData := wavData[dataOffset:]

	// Convert PCM to float samples
	var samples []float64
	if bitsPerSample == 16 {
		sampleCount := len(audioData) / 2
		samples = make([]float64, sampleCount)
		for i := 0; i < sampleCount; i++ {
			sample := int16(binary.LittleEndian.Uint16(audioData[i*2 : i*2+2]))
			samples[i] = float64(sample) / 32768.0
		}
	} else if bitsPerSample == 8 {
		samples = make([]float64, len(audioData))
		for i := 0; i < len(audioData); i++ {
			samples[i] = (float64(audioData[i]) - 128.0) / 128.0
		}
	} else {
		return nil, 0, fmt.Errorf("unsupported bits per sample: %d", bitsPerSample)
	}

	// Convert stereo to mono if needed
	if channels == 2 {
		monoSamples := make([]float64, len(samples)/2)
		for i := 0; i < len(monoSamples); i++ {
			monoSamples[i] = (samples[i*2] + samples[i*2+1]) / 2.0
		}
		samples = monoSamples
	}

	return samples, sampleRate, nil
}

// parabolicInterpolate performs parabolic interpolation around an FFT peak for sub-bin accuracy
// This technique improves frequency resolution from ±3.9 Hz (bin width) to ±0.5 Hz
// Inspired by icad_tone_detection (thegreatcodeholio)
func parabolicInterpolate(yMinus, y0, yPlus float64) float64 {
	denom := yMinus - 2.0*y0 + yPlus
	if denom == 0.0 {
		return 0.0
	}
	return 0.5 * (yMinus - yPlus) / denom
}

// analyzeFrequencies detects sustained paging tones via the single STFT engine
// (analyzeSTFTTones) and optionally matches them against configured tone sets.
func (detector *ToneDetector) analyzeFrequencies(samples []float64, sampleRate int, toneSets []ToneSet, includeUnmatched bool) []Tone {
	maxSamples := int(toneAnalysisMaxSeconds * float64(sampleRate))
	if maxSamples > 0 && len(samples) > maxSamples {
		samples = samples[:maxSamples]
	}

	minToneDuration := toneDetectMinDurationSec
	gates := detector.computeToneAnalysisGates(samples, sampleRate)
	if gates.globalPeak < 1e-20 {
		return []Tone{}
	}
	fmt.Printf("tone detection: global peak=%.4f, noise floor=%.1f dB, q20=%.1f dB\n", gates.globalPeak, gates.noiseFloorDB, gates.q20)

	work := cropSamplesToPagingRegion(samples, sampleRate)
	mergedDetections := detector.analyzeSTFTTones(work, sampleRate, gates)
	mergedDetections = pruneHarmonicMergedDetections(mergedDetections)

	var tones []Tone
	var allDetections []toneFreqDetection
	for _, md := range mergedDetections {
		if md.endTime-md.startTime >= minToneDuration {
			allDetections = append(allDetections, toneFreqDetection{
				frequency: md.frequency,
				startTime: md.startTime,
				endTime:   md.endTime,
				magnitude: md.magnitude,
			})
		}
	}

	// Process merged detections
	for _, md := range mergedDetections {
		duration := md.endTime - md.startTime

		// Check if frequency matches ANY configured tone set (check ALL, don't stop at first match)
		matchedToneSets := []string{}         // Track all matches for logging
		matchedTypes := make(map[string]bool) // Track which types this tone matched (A, B, Long)
		matched := false

		for _, toneSet := range toneSets {
			// Determine tolerance: if < 1.0, treat as multiplier for 5 Hz (0.01 = 5 Hz, 0.02 = 10 Hz, etc.); if >= 1.0, treat as absolute Hz
			baseTolerance := toneSet.Tolerance

			// Check ATone
			if toneSet.ATone != nil {
				// Calculate actual tolerance: if ratio (< 1.0), multiply by 500 Hz (0.01 = 5 Hz); if >= 1.0, use as absolute Hz
				actualTolerance := baseTolerance
				if baseTolerance < 1.0 {
					actualTolerance = baseTolerance * 500.0
				}
				freqDiff := math.Abs(md.frequency - toneSet.ATone.Frequency)
				if freqDiff <= actualTolerance && duration >= toneSet.ATone.MinDuration {
					// Check MaxDuration if specified (0 = unlimited)
					if toneSet.ATone.MaxDuration == 0 || duration <= toneSet.ATone.MaxDuration {
						matched = true
						matchedTypes["A"] = true
						matchInfo := fmt.Sprintf("%s A-tone (%.1f Hz, tol: ±%.1f Hz, diff: %.1f Hz)", toneSet.Label, toneSet.ATone.Frequency, actualTolerance, freqDiff)
						matchedToneSets = append(matchedToneSets, matchInfo)
						// Continue checking other tone sets - DON'T BREAK
					}
				}
			}

			// Check BTone
			if toneSet.BTone != nil {
				// Calculate actual tolerance: if ratio (< 1.0), multiply by 500 Hz (0.01 = 5 Hz); if >= 1.0, use as absolute Hz
				actualTolerance := baseTolerance
				if baseTolerance < 1.0 {
					actualTolerance = baseTolerance * 500.0
				}
				freqDiff := math.Abs(md.frequency - toneSet.BTone.Frequency)
				if freqDiff <= actualTolerance && duration >= toneSet.BTone.MinDuration {
					// Check MaxDuration if specified (0 = unlimited)
					if toneSet.BTone.MaxDuration == 0 || duration <= toneSet.BTone.MaxDuration {
						matched = true
						matchedTypes["B"] = true
						matchInfo := fmt.Sprintf("%s B-tone (%.1f Hz, tol: ±%.1f Hz, diff: %.1f Hz)", toneSet.Label, toneSet.BTone.Frequency, actualTolerance, freqDiff)
						matchedToneSets = append(matchedToneSets, matchInfo)
						// Continue checking other tone sets - DON'T BREAK
					}
				}
			}

			// Check LongTone
			if toneSet.LongTone != nil {
				// Calculate actual tolerance: if ratio (< 1.0), multiply by 500 Hz (0.01 = 5 Hz); if >= 1.0, use as absolute Hz
				actualTolerance := baseTolerance
				if baseTolerance < 1.0 {
					actualTolerance = baseTolerance * 500.0
				}
				freqDiff := math.Abs(md.frequency - toneSet.LongTone.Frequency)
				if freqDiff <= actualTolerance && duration >= toneSet.LongTone.MinDuration {
					// Check MaxDuration if specified (0 = unlimited)
					if toneSet.LongTone.MaxDuration == 0 || duration <= toneSet.LongTone.MaxDuration {
						matched = true
						matchedTypes["Long"] = true
						matchInfo := fmt.Sprintf("%s long-tone (%.1f Hz, tol: ±%.1f Hz, diff: %.1f Hz)", toneSet.Label, toneSet.LongTone.Frequency, actualTolerance, freqDiff)
						matchedToneSets = append(matchedToneSets, matchInfo)
						// Continue checking other tone sets - DON'T BREAK
					}
				}
			}
		}

		// Determine tone type based on what it matched
		// If it matches multiple types, leave empty (ambiguous)
		// If it matches only one type, use that
		toneType := ""
		if len(matchedTypes) == 1 {
			if matchedTypes["A"] {
				toneType = "A"
			} else if matchedTypes["B"] {
				toneType = "B"
			} else if matchedTypes["Long"] {
				toneType = "Long"
			}
		}

		// Log merged detection (showing merge info if multiple detections were merged)
		if matched {
			if md.count > 1 {
				fmt.Printf("tone matched - %.1f Hz (merged from %d detections) for %.2fs (matched: %s)\n", md.frequency, md.count, duration, strings.Join(matchedToneSets, ", "))
			} else {
				fmt.Printf("tone matched - %.1f Hz for %.2fs (matched: %s)\n", md.frequency, duration, strings.Join(matchedToneSets, ", "))
			}
			tones = append(tones, Tone{
				Frequency: md.frequency,
				StartTime: md.startTime,
				EndTime:   md.endTime,
				Duration:  duration,
				ToneType:  toneType,
				Magnitude: md.magnitude,
			})
		} else if includeUnmatched {
			seqType := ""
			switch len(tones) {
			case 0:
				seqType = "A"
			case 1:
				seqType = "B"
			}
			tones = append(tones, Tone{
				Frequency: md.frequency,
				StartTime: md.startTime,
				EndTime:   md.endTime,
				Duration:  duration,
				ToneType:  seqType,
				Magnitude: md.magnitude,
			})
		} else {
			// Log what we were looking for vs what was detected
			if md.count > 1 {
				fmt.Printf("tone detected but NO MATCH - %.1f Hz (merged from %d detections) for %.2fs (mag: %.4f)\n", md.frequency, md.count, duration, md.magnitude)
			} else {
				fmt.Printf("tone detected but NO MATCH - %.1f Hz for %.2fs (mag: %.4f)\n", md.frequency, duration, md.magnitude)
			}
			// Show closest configured tones for debugging
			if len(toneSets) > 0 {
				minDiff := 9999.0
				var closestTone string
				for _, ts := range toneSets {
					baseTol := ts.Tolerance
					if ts.ATone != nil {
						actualTol := baseTol
						if baseTol < 1.0 {
							actualTol = baseTol * 500.0
						}
						diff := math.Abs(md.frequency - ts.ATone.Frequency)
						if diff < minDiff {
							minDiff = diff
							closestTone = fmt.Sprintf("%s A-tone: %.1f Hz (tol: ±%.1f Hz, diff: %.1f Hz)", ts.Label, ts.ATone.Frequency, actualTol, diff)
						}
					}
					if ts.BTone != nil {
						actualTol := baseTol
						if baseTol < 1.0 {
							actualTol = baseTol * 500.0
						}
						diff := math.Abs(md.frequency - ts.BTone.Frequency)
						if diff < minDiff {
							minDiff = diff
							closestTone = fmt.Sprintf("%s B-tone: %.1f Hz (tol: ±%.1f Hz, diff: %.1f Hz)", ts.Label, ts.BTone.Frequency, actualTol, diff)
						}
					}
					if ts.LongTone != nil {
						actualTol := baseTol
						if baseTol < 1.0 {
							actualTol = baseTol * 500.0
						}
						diff := math.Abs(md.frequency - ts.LongTone.Frequency)
						if diff < minDiff {
							minDiff = diff
							closestTone = fmt.Sprintf("%s long-tone: %.1f Hz (tol: ±%.1f Hz, diff: %.1f Hz)", ts.Label, ts.LongTone.Frequency, actualTol, diff)
						}
					}
				}
				if closestTone != "" {
					fmt.Printf("closest configured tone: %s\n", closestTone)
				}
			}
		}
	}

	// Log summary
	if len(allDetections) > 0 {
		fmt.Printf("total detections meeting duration: %d, merged to: %d, matched: %d\n", len(allDetections), len(mergedDetections), len(tones))
		if len(allDetections) != len(mergedDetections) {
			fmt.Printf("DEBUG: merged %d detections into %d (removed %d duplicates)\n", len(allDetections), len(mergedDetections), len(allDetections)-len(mergedDetections))
		}
	} else {
		fmt.Printf("no tones detected meeting minimum duration (%.1fs)\n", minToneDuration)
	}

	return tones
}

// dft performs Fast Fourier Transform (FFT) on real-valued samples
// Returns magnitude spectrum up to Nyquist frequency
// This is O(N log N) complexity, much faster than the previous O(N²) DFT implementation
func (detector *ToneDetector) dft(samples []float64, sampleRate int) map[int]float64 {
	N := len(samples)
	nyquist := sampleRate / 2
	magnitudes := make(map[int]float64)

	// Only compute up to Nyquist frequency
	maxBin := (N * nyquist) / sampleRate

	// Create FFT transformer and compute FFT
	// gonum's NewFFT computes FFT on real input, returns complex coefficients
	fft := fourier.NewFFT(N)
	coeff := fft.Coefficients(nil, samples)

	// Convert complex FFT results to magnitudes
	// Store in map with bin index (matches old DFT interface)
	// Only need first half (up to Nyquist frequency)
	for k := 0; k < maxBin && k < N/2; k++ {
		// FFT gives us complex coefficients, compute magnitude
		// Normalize by N (same as DFT implementation)
		magnitude := cmplx.Abs(coeff[k]) / float64(N)
		magnitudes[k] = magnitude
	}

	return magnitudes
}

// MatchToneSet matches detected tones against configured tone sets and returns the first match
func (detector *ToneDetector) MatchToneSet(detected *ToneSequence, configured []ToneSet) *ToneSet {
	matched := detector.MatchToneSets(detected, configured)
	if len(matched) > 0 {
		return matched[0]
	}
	return nil
}

// MatchToneSets matches detected tones against configured tone sets and returns ALL matches
// This is used for stacked tones where multiple tone sequences may be detected across calls
func (detector *ToneDetector) MatchToneSets(detected *ToneSequence, configured []ToneSet) []*ToneSet {
	if detected == nil || !detected.HasTones || len(configured) == 0 {
		return nil
	}

	var matched []*ToneSet
	for i := range configured {
		toneSet := configured[i]
		if detector.matchesToneSet(detected, toneSet) {
			matched = append(matched, &toneSet)
		}
	}

	return matched
}

// matchesToneSet checks if detected tones match a configured tone set
// Requires that A-tone and B-tone come from the same sequence (A-tone before B-tone)
func (detector *ToneDetector) matchesToneSet(detected *ToneSequence, toneSet ToneSet) bool {
	baseTolerance := toneSet.Tolerance

	// If tone set only has a long tone (no A/B tones), only check for long tone
	if toneSet.LongTone != nil && toneSet.ATone == nil && toneSet.BTone == nil {
		actualTolerance := baseTolerance
		if baseTolerance < 1.0 {
			actualTolerance = baseTolerance * 500.0
		}

		for _, tone := range detected.Tones {
			if detector.frequencyMatches(tone.Frequency, toneSet.LongTone.Frequency, actualTolerance) {
				if tone.Duration >= toneSet.LongTone.MinDuration {
					if toneSet.LongTone.MaxDuration == 0 || tone.Duration <= toneSet.LongTone.MaxDuration {
						// Found matching long tone
						return true
					}
				}
			}
		}
		// No matching long tone found
		return false
	}

	// Find matching A-tone(s) and B-tone(s) with timing
	type matchingTone struct {
		tone      Tone
		matchedAs string // "A" or "B"
	}

	var aTones []matchingTone
	var bTones []matchingTone

	// Find all matching A-tones (only if tone set has A-tone configured)
	if toneSet.ATone != nil {
		actualTolerance := baseTolerance
		if baseTolerance < 1.0 {
			actualTolerance = baseTolerance * 500.0
		}

		for _, tone := range detected.Tones {
			if detector.frequencyMatches(tone.Frequency, toneSet.ATone.Frequency, actualTolerance) {
				if tone.Duration >= toneSet.ATone.MinDuration {
					if toneSet.ATone.MaxDuration == 0 || tone.Duration <= toneSet.ATone.MaxDuration {
						aTones = append(aTones, matchingTone{tone: tone, matchedAs: "A"})
					}
				}
			}
		}
	}

	// Find all matching B-tones (only if tone set has B-tone configured)
	if toneSet.BTone != nil {
		actualTolerance := baseTolerance
		if baseTolerance < 1.0 {
			actualTolerance = baseTolerance * 500.0
		}

		for _, tone := range detected.Tones {
			if detector.frequencyMatches(tone.Frequency, toneSet.BTone.Frequency, actualTolerance) {
				if tone.Duration >= toneSet.BTone.MinDuration {
					if toneSet.BTone.MaxDuration == 0 || tone.Duration <= toneSet.BTone.MaxDuration {
						bTones = append(bTones, matchingTone{tone: tone, matchedAs: "B"})
					}
				}
			}
		}
	}

	// Require A-tone if configured
	if toneSet.ATone != nil && len(aTones) == 0 {
		fmt.Printf("DEBUG: Tone set '%s' requires A-tone but none found\n", toneSet.Label)
		return false
	}

	// Require B-tone if configured
	if toneSet.BTone != nil && len(bTones) == 0 {
		fmt.Printf("DEBUG: Tone set '%s' requires B-tone but none found\n", toneSet.Label)
		return false
	}

	// Note: If tone set has A/B tones, we do NOT check for long tones
	// Long tones are only checked if the tone set has NO A/B tones (handled by early return above)

	// If both A-tone and B-tone are required, they must form a valid sequence
	// Each A-tone must be paired with its closest following B-tone (within 0.5s)
	// This prevents false matches where an A-tone pairs with a B-tone from a different tone sequence
	if toneSet.ATone != nil && toneSet.BTone != nil {
		fmt.Printf("DEBUG: Checking sequence for tone set '%s' - found %d A-tones and %d B-tones\n", toneSet.Label, len(aTones), len(bTones))

		// Sort A-tones by start time to process them in sequence
		aTonesSorted := make([]matchingTone, len(aTones))
		copy(aTonesSorted, aTones)
		sort.Slice(aTonesSorted, func(i, j int) bool {
			return aTonesSorted[i].tone.StartTime < aTonesSorted[j].tone.StartTime
		})

		// Check each A-tone against the tone set's B-tone
		// Each A-tone must find its closest following B-tone that matches this tone set
		for _, aMatch := range aTonesSorted {
			fmt.Printf("DEBUG: A-tone %.1f Hz: start=%.2fs, end=%.2fs, duration=%.2fs\n",
				aMatch.tone.Frequency, aMatch.tone.StartTime, aMatch.tone.EndTime, aMatch.tone.Duration)

			// Find the closest following B-tone within 0.5s gap
			// "Closest" means the smallest gap (either negative for overlap, or positive for sequential)
			var closestB *matchingTone
			var closestGap float64
			hasClosest := false

			for i := range bTones {
				bMatch := &bTones[i]

				fmt.Printf("DEBUG:   Checking B-tone %.1f Hz: start=%.2fs, end=%.2fs, duration=%.2fs\n",
					bMatch.tone.Frequency, bMatch.tone.StartTime, bMatch.tone.EndTime, bMatch.tone.Duration)

				// B-tone must START after A-tone starts (allows overlapping tones)
				// This supports both sequential (A then B) and overlapping (A+B simultaneously) two-tone paging
				if bMatch.tone.StartTime < aMatch.tone.StartTime {
					fmt.Printf("DEBUG:     REJECTED: B-tone starts (%.2fs) before A-tone starts (%.2fs)\n",
						bMatch.tone.StartTime, aMatch.tone.StartTime)
					continue // B starts before A starts, not a valid sequence
				}

				// Calculate gap (B start time - A end time)
				// Negative = overlapping (B starts before A ends) - this is OK now!
				// Positive = sequential (B starts after A ends)
				gap := bMatch.tone.StartTime - aMatch.tone.EndTime
				fmt.Printf("DEBUG:     Gap: %.2fs (B start %.2fs - A end %.2fs)\n",
					gap, bMatch.tone.StartTime, aMatch.tone.EndTime)

				// Allow overlap up to full duration of A-tone, or sequential up to 0.5s gap
				// This handles overlapping two-tone paging (gap will be negative)
				maxNegativeGap := -aMatch.tone.Duration // Allow B to start anytime after A starts
				if gap >= maxNegativeGap && gap <= 0.5 {
					// Check if this is closer than previous closest
					if !hasClosest {
						closestB = bMatch
						closestGap = gap
						hasClosest = true
						fmt.Printf("DEBUG:     ACCEPTED as closest (gap=%.2fs)\n", gap)
					} else {
						// Compare absolute gaps - want the one closest to 0
						if math.Abs(gap) < math.Abs(closestGap) {
							closestB = bMatch
							closestGap = gap
							fmt.Printf("DEBUG:     ACCEPTED as new closest (gap=%.2fs, prev=%.2fs)\n", gap, closestGap)
						} else {
							fmt.Printf("DEBUG:     Not closer than current closest (gap=%.2fs vs %.2fs)\n", gap, closestGap)
						}
					}
				} else {
					fmt.Printf("DEBUG:     REJECTED: Gap %.2fs outside of -0.5s to +0.5s range\n", gap)
				}
			}

			// If we found a closest B-tone, check if it matches this tone set's B-tone frequency
			if closestB != nil {
				fmt.Printf("DEBUG:   Found closest B-tone: %.1f Hz with gap=%.2fs\n", closestB.tone.Frequency, closestGap)
				// Check if the closest B-tone matches the tone set's B-tone frequency
				actualTolerance := baseTolerance
				if baseTolerance < 1.0 {
					actualTolerance = baseTolerance * 500.0
				}

				if detector.frequencyMatches(closestB.tone.Frequency, toneSet.BTone.Frequency, actualTolerance) {
					// Found a valid A-B pair where A-tone pairs with its closest B-tone
					// and that closest B-tone matches this tone set's B-tone
					fmt.Printf("DEBUG: MATCH! Tone set '%s' matched with A-B sequence\n", toneSet.Label)
					return true
				} else {
					fmt.Printf("DEBUG:   B-tone frequency %.1f Hz does NOT match expected %.1f Hz (tol: ±%.1f Hz)\n",
						closestB.tone.Frequency, toneSet.BTone.Frequency, actualTolerance)
				}
			} else {
				fmt.Printf("DEBUG:   No valid B-tone found within 0.5s of this A-tone\n")
			}
		}

		fmt.Printf("DEBUG: No valid A-B sequence found for tone set '%s'\n", toneSet.Label)
		// No valid A-B pair found where A pairs with closest B-tone that matches this tone set
		return false
	}

	return true
}

// frequencyMatches checks if a detected frequency matches an expected frequency within tolerance
func (detector *ToneDetector) frequencyMatches(detected, expected, tolerance float64) bool {
	diff := math.Abs(detected - expected)
	return diff <= tolerance
}

// ParseToneSets parses JSON tone sets from database
func ParseToneSets(jsonData string) ([]ToneSet, error) {
	if jsonData == "" || jsonData == "[]" {
		return []ToneSet{}, nil
	}

	var toneSets []ToneSet
	if err := json.Unmarshal([]byte(jsonData), &toneSets); err != nil {
		return nil, fmt.Errorf("failed to parse tone sets: %v", err)
	}

	return toneSets, nil
}

// SerializeToneSets serializes tone sets to JSON for database storage
func SerializeToneSets(toneSets []ToneSet) (string, error) {
	if len(toneSets) == 0 {
		return "[]", nil
	}

	data, err := json.Marshal(toneSets)
	if err != nil {
		return "", fmt.Errorf("failed to serialize tone sets: %v", err)
	}

	return string(data), nil
}

// SerializeToneSequence serializes a tone sequence to JSON for database storage
func SerializeToneSequence(toneSequence *ToneSequence) (string, error) {
	if toneSequence == nil {
		return "{}", nil
	}

	data, err := json.Marshal(toneSequence)
	if err != nil {
		return "", fmt.Errorf("failed to serialize tone sequence: %v", err)
	}

	return string(data), nil
}

// RemoveTonesFromAudio removes detected tone segments from audio file using ffmpeg
// Returns filtered audio (without tones) for transcription, or original audio if filtering fails
// This prevents tone hallucinations in transcripts while preserving original audio for playback
func (detector *ToneDetector) RemoveTonesFromAudio(audio []byte, audioMime string, tones []Tone) ([]byte, error) {
	if len(tones) == 0 {
		return audio, nil // No tones to remove
	}

	// STEP 1: Convert input audio to WAV format first (regardless of input format: MP3, M4A, etc.)
	// This ensures we have a consistent format with reliable duration metadata in the header
	// WAV duration is always in the header, unlike streaming formats (MP3) which may return "N/A"
	ffConvertArgs := []string{
		"-y", "-loglevel", "error",
		"-i", "pipe:0", // Read from stdin
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // Mono
		"-f", "wav", // WAV format
		"pipe:1", // Write to stdout
	}
	ffConvertCmd := exec.Command("ffmpeg", ffConvertArgs...)

	// Setup stdin for ffmpeg
	stdinConvert, err := ffConvertCmd.StdinPipe()
	if err != nil {
		return audio, fmt.Errorf("failed to create stdin pipe for WAV conversion: %v", err)
	}
	go func() {
		defer stdinConvert.Close()
		_, _ = stdinConvert.Write(audio)
	}()

	// Setup stdout for ffmpeg
	var wavData bytes.Buffer
	ffConvertCmd.Stdout = &wavData
	var ffConvertErr bytes.Buffer
	ffConvertCmd.Stderr = &ffConvertErr

	// Run ffmpeg to convert to WAV
	if err := ffConvertCmd.Start(); err != nil {
		return audio, fmt.Errorf("failed to start ffmpeg for WAV conversion: %v", err)
	}

	if err := ffConvertCmd.Wait(); err != nil {
		return audio, fmt.Errorf("ffmpeg WAV conversion failed: %v, stderr: %s", err, ffConvertErr.String())
	}

	wavBytes := wavData.Bytes()
	if len(wavBytes) < 1000 {
		return audio, fmt.Errorf("WAV conversion produced too small output (%d bytes)", len(wavBytes))
	}

	// STEP 2: Get duration by parsing WAV header directly (more reliable than ffprobe for piped data)
	// Parse WAV header to get sample rate and calculate duration from PCM data
	if len(wavBytes) < 44 {
		return audio, fmt.Errorf("WAV file too short to parse header")
	}

	// Verify WAV header
	if string(wavBytes[0:4]) != "RIFF" || string(wavBytes[8:12]) != "WAVE" {
		return audio, fmt.Errorf("invalid WAV header")
	}

	// Read audio parameters from WAV header
	sampleRate := int(binary.LittleEndian.Uint32(wavBytes[24:28]))
	channels := int(binary.LittleEndian.Uint16(wavBytes[22:24]))
	bitsPerSample := int(binary.LittleEndian.Uint16(wavBytes[34:36]))

	if sampleRate == 0 || channels == 0 || bitsPerSample == 0 {
		return audio, fmt.Errorf("invalid WAV parameters: sampleRate=%d, channels=%d, bitsPerSample=%d", sampleRate, channels, bitsPerSample)
	}

	// Find data chunk size
	var dataSize int
	for i := 12; i < len(wavBytes)-8; i++ {
		if string(wavBytes[i:i+4]) == "data" {
			dataSize = int(binary.LittleEndian.Uint32(wavBytes[i+4 : i+8]))
			break
		}
	}

	if dataSize == 0 {
		return audio, fmt.Errorf("WAV data chunk not found")
	}

	// Calculate duration from sample count
	bytesPerSample := bitsPerSample / 8
	sampleCount := dataSize / (bytesPerSample * channels)
	totalDuration := float64(sampleCount) / float64(sampleRate)

	if totalDuration <= 0 {
		return audio, fmt.Errorf("invalid calculated WAV duration: %.2fs (sampleRate=%d, channels=%d, sampleCount=%d)", totalDuration, sampleRate, channels, sampleCount)
	}

	// Build ffmpeg filter to remove tone segments
	// Strategy: Keep all audio EXCEPT the tone segments
	// Use select filter to keep only segments we want

	// Sort tones by start time
	sortedTones := make([]Tone, len(tones))
	copy(sortedTones, tones)
	sort.Slice(sortedTones, func(i, j int) bool {
		return sortedTones[i].StartTime < sortedTones[j].StartTime
	})

	// Build segments to KEEP (everything except tones)
	// Add small buffer (0.05s = 50ms) around tones to ensure complete removal without cutting too much voice
	const toneBuffer = 0.05

	type segment struct {
		start, end float64
	}
	var keepSegments []segment

	currentPos := 0.0
	for _, tone := range sortedTones {
		toneStart := math.Max(0, tone.StartTime-toneBuffer)
		toneEnd := math.Min(totalDuration, tone.EndTime+toneBuffer)

		// Detect if this tone is cutting into potential voice
		if currentPos < toneStart {
			segmentDuration := toneStart - currentPos
			// Keep segments that are at least 0.3s (300ms) - shorter segments are likely artifacts
			if segmentDuration >= 0.3 {
				keepSegments = append(keepSegments, segment{currentPos, toneStart})
				fmt.Printf("audio filtering: keeping voice segment %.3fs-%.3fs (%.2fs)\n", currentPos, toneStart, segmentDuration)
			} else {
				fmt.Printf("audio filtering: skipping short segment %.3fs-%.3fs (%.2fs, likely artifact)\n", currentPos, toneStart, segmentDuration)
			}
		}

		// Skip the tone itself
		fmt.Printf("audio filtering: removing tone %.3fs-%.3fs (%.2fs at %.1fHz)\n", toneStart, toneEnd, tone.Duration, tone.Frequency)
		currentPos = toneEnd
	}

	// Add final segment after last tone
	if currentPos < totalDuration {
		segmentDuration := totalDuration - currentPos
		// Always keep the final segment if it exists, as it's likely voice after tones
		if segmentDuration >= 0.1 {
			keepSegments = append(keepSegments, segment{currentPos, totalDuration})
			fmt.Printf("audio filtering: keeping final voice segment %.3fs-%.3fs (%.2fs)\n", currentPos, totalDuration, segmentDuration)
		}
	}

	// If no segments to keep, return empty (all tones)
	if len(keepSegments) == 0 {
		fmt.Printf("audio filtering: all audio is tones, returning original\n")
		return audio, nil
	}

	// Build ffmpeg filter complex
	// Use select filter to extract segments, then concat them
	var filterParts []string
	for i, seg := range keepSegments {
		// between(t,start,end) selects frames in time range
		filterParts = append(filterParts, fmt.Sprintf("[0:a]atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS[a%d]",
			seg.start, seg.end, i))
	}

	// Concat all segments
	concatInputs := ""
	for i := range keepSegments {
		concatInputs += fmt.Sprintf("[a%d]", i)
	}
	filterComplex := strings.Join(filterParts, ";") + fmt.Sprintf(";%sconcat=n=%d:v=0:a=1[out]",
		concatInputs, len(keepSegments))

	// STEP 3: Run ffmpeg with filter using stdin/stdout pipes on the WAV data
	// Output WAV for transcription
	ffArgs := []string{
		"-y", "-loglevel", "error",
		"-i", "pipe:0", // Read WAV from stdin
		"-filter_complex", filterComplex,
		"-map", "[out]",
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // Mono
		"-f", "wav", // Output WAV format for transcription
		"pipe:1", // Write to stdout
	}

	fmt.Printf("audio filtering: removing %d tone segments (%.2fs of tones from %.2fs total)\n",
		len(sortedTones), calculateTotalToneDuration(sortedTones), totalDuration)

	ffCmd := exec.Command("ffmpeg", ffArgs...)

	// Setup stdin for ffmpeg (use WAV data, not original audio)
	stdin, err := ffCmd.StdinPipe()
	if err != nil {
		return audio, fmt.Errorf("failed to create stdin pipe for ffmpeg: %v", err)
	}
	go func() {
		defer stdin.Close()
		_, _ = stdin.Write(wavBytes) // Use WAV data instead of original audio
	}()

	// Setup stdout for ffmpeg
	var filteredAudio bytes.Buffer
	ffCmd.Stdout = &filteredAudio
	var ffErr bytes.Buffer
	ffCmd.Stderr = &ffErr

	// Run ffmpeg
	if err := ffCmd.Start(); err != nil {
		return audio, fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// Wait for ffmpeg to complete
	if err := ffCmd.Wait(); err != nil {
		return audio, fmt.Errorf("ffmpeg filtering failed: %v, stderr: %s", err, ffErr.String())
	}

	// Get filtered audio from stdout
	filteredAudioBytes := filteredAudio.Bytes()

	// Verify we got something back
	if len(filteredAudioBytes) < 1000 {
		fmt.Printf("audio filtering: filtered audio too small (%d bytes), returning original\n", len(filteredAudioBytes))
		return audio, nil
	}

	fmt.Printf("audio filtering: success - original: %d bytes, filtered: %d bytes (removed %.1f%%)\n",
		len(audio), len(filteredAudioBytes), (1.0-float64(len(filteredAudioBytes))/float64(len(audio)))*100)

	return filteredAudioBytes, nil
}

const (
	// residualSpeechMinDB is the frame RMS (dBFS) a frame must reach to count as speech after tone removal.
	// On real Trumbull dispatch calls the leftover of a tone-only page peaks around -34..-37 dB while
	// speech sits around -3 dB, so -28 dB leaves ~25 dB of margin on both sides.
	residualSpeechMinDB = -28.0

	// MinResidualSpeechSeconds is how much non-tonal, speech-level audio must remain after tone
	// removal before the clip is worth sending to speech-to-text. Below this, STT only sees silence
	// and static and hallucinates filler ("you", "Thank you.") that then masquerades as the call.
	MinResidualSpeechSeconds = 0.4
)

// ResidualSpeechSeconds estimates how many seconds of speech-like audio are in a 16-bit PCM WAV
// (as produced by RemoveTonesFromAudio): frames that are loud enough AND not spectrally pure.
// Tone remnants that survived removal are loud but tonal, so they do not count as speech.
func (detector *ToneDetector) ResidualSpeechSeconds(wav []byte) (float64, error) {
	samples, sampleRate, err := detector.parseWAV(wav)
	if err != nil {
		return 0, err
	}

	const windowSize = 2048
	const hopSize = 512
	if len(samples) < windowSize {
		return 0, nil
	}

	speechFrames := 0
	for start := 0; start+windowSize <= len(samples); start += hopSize {
		frame := samples[start : start+windowSize]

		var sumSq float64
		for _, s := range frame {
			sumSq += s * s
		}
		rmsDB := 20.0 * math.Log10(math.Max(math.Sqrt(sumSq/float64(windowSize)), 1e-6))
		if rmsDB < residualSpeechMinDB {
			continue
		}

		windowed := make([]float64, windowSize)
		for i, s := range frame {
			windowed[i] = s * 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(windowSize-1)))
		}
		if spectralTonality(detector.dft(windowed, sampleRate), windowSize, sampleRate) >= minTranscriptionToneTonality {
			continue
		}
		speechFrames++
	}

	return float64(speechFrames*hopSize) / float64(sampleRate), nil
}

// calculateTotalToneDuration calculates total duration of all tones
func calculateTotalToneDuration(tones []Tone) float64 {
	total := 0.0
	for _, tone := range tones {
		total += tone.Duration
	}
	return total
}

// DetectAllTonesForTranscription detects ALL sustained tones in audio (200-5000Hz range)
// regardless of whether they match configured tone sets. This is used to remove dispatch tones
// before transcription to prevent Whisper hallucinations.
// Returns all detected tones that meet minimum duration requirements.
func (detector *ToneDetector) DetectAllTonesForTranscription(audio []byte, audioMime string) ([]Tone, error) {
	if len(audio) < 1000 {
		return []Tone{}, nil
	}

	// Convert audio to WAV PCM format using ffmpeg via stdin/stdout pipes
	ffArgs := []string{
		"-y", "-loglevel", "error",
		"-i", "pipe:0", // Read from stdin
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // Mono
		"-af", "highpass=f=200,lowpass=f=5000,dynaudnorm", // Detect tones in dispatch range
		"-f", "wav",
		"pipe:1", // Write to stdout
	}
	ffCmd := exec.Command("ffmpeg", ffArgs...)

	// Setup stdin for ffmpeg
	stdin, err := ffCmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe for ffmpeg: %v", err)
	}
	go func() {
		defer stdin.Close()
		_, _ = stdin.Write(audio)
	}()

	// Setup stdout for ffmpeg
	var wavData bytes.Buffer
	ffCmd.Stdout = &wavData
	var ffErr bytes.Buffer
	ffCmd.Stderr = &ffErr

	// Run ffmpeg
	if err := ffCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// Wait for ffmpeg to complete
	if err := ffCmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %v, stderr: %s", err, ffErr.String())
	}

	// Get the WAV data from stdout
	if wavData.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced no output")
	}

	// Parse WAV and extract PCM samples
	samples, sampleRate, err := detector.parseWAV(wavData.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to parse WAV: %v", err)
	}

	if len(samples) < 100 {
		return []Tone{}, nil
	}

	// Perform FFT analysis to detect ALL tones (no tone set matching)
	// Use aggressive detection parameters to catch all dispatch tones
	detectedTones := detector.detectAllSustainedTones(samples, sampleRate)

	fmt.Printf("transcription tone detection: found %d sustained tones to remove before transcription\n", len(detectedTones))

	return detectedTones, nil
}

// minTranscriptionToneTonality is the minimum spectral purity a frame must have to count as part of a
// dispatch tone when pre-filtering audio for transcription (see spectralTonality).
//
// Calibrated on real calls from the Trumbull County dispatch talkgroup: tone pages hold ~1.00 (pure
// sinusoids) for 3s+ runs, while the longest unbroken run of frames >= 0.95 in any voice call was
// 0.74s. Without this check, steady dispatcher vowels (200-650 Hz) were detected as "tones", the
// "voice remaining after the last tone" collapsed to <2s and the real dispatch was skipped as
// tone-only.
const minTranscriptionToneTonality = 0.95

// spectralTonality returns the fraction of in-band (200-5000 Hz) spectral energy that sits in the
// three strongest spectral peaks (+-2 bins each) of a windowed frame. A pure dispatch tone (or a
// tone plus its harmonics) scores ~1.0; speech spreads its energy across many harmonics and noise,
// and scores well below that over any sustained stretch.
func spectralTonality(magnitudes map[int]float64, windowSize, sampleRate int) float64 {
	lo := int(200.0 * float64(windowSize) / float64(sampleRate))
	hi := int(5000.0 * float64(windowSize) / float64(sampleRate))
	if lo < 1 {
		lo = 1
	}

	type peak struct {
		bin    int
		energy float64
	}
	var total float64
	var peaks []peak
	for k := lo; k <= hi; k++ {
		m := magnitudes[k]
		e := m * m
		total += e
		if m > magnitudes[k-1] && m >= magnitudes[k+1] {
			peaks = append(peaks, peak{k, e})
		}
	}
	if total <= 0 || len(peaks) == 0 {
		return 0
	}

	sort.Slice(peaks, func(i, j int) bool { return peaks[i].energy > peaks[j].energy })

	used := make(map[int]bool)
	var peakEnergy float64
	taken := 0
	for _, p := range peaks {
		if taken == 3 {
			break
		}
		if used[p.bin] {
			continue
		}
		for k := p.bin - 2; k <= p.bin+2; k++ {
			if !used[k] {
				used[k] = true
				peakEnergy += magnitudes[k] * magnitudes[k]
			}
		}
		taken++
	}
	return peakEnergy / total
}

// detectAllSustainedTones detects all sustained tones in audio without matching against tone sets
// This is specifically for transcription pre-processing to remove ALL dispatch tones
func (detector *ToneDetector) detectAllSustainedTones(samples []float64, sampleRate int) []Tone {
	windowSize := 2048
	hopSize := 512
	minToneDuration := 0.5 // Minimum 500ms (slightly less aggressive than 600ms for tone matching)

	// Track detected frequencies over time
	type freqDetection struct {
		frequency float64
		startTime float64
		endTime   float64
		magnitude float64
	}

	detections := make(map[int][]freqDetection)

	// Perform dynamic noise floor estimation (same as main detector)
	var framePeaks []float64
	numWindows := (len(samples) - windowSize) / hopSize

	// First pass: collect frame peaks
	for win := 0; win < numWindows; win++ {
		start := win * hopSize
		end := start + windowSize
		if end > len(samples) {
			break
		}

		window := samples[start:end]
		windowed := make([]float64, len(window))
		for i := range window {
			hann := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(len(window)-1)))
			windowed[i] = window[i] * hann
		}

		magnitudes := detector.dft(windowed, sampleRate)

		var framePeak float64
		for bin, mag := range magnitudes {
			freq := float64(bin) * float64(sampleRate) / float64(windowSize)
			if freq >= 200.0 && freq <= 5000.0 && mag > framePeak {
				framePeak = mag
			}
		}
		framePeaks = append(framePeaks, framePeak)
	}

	if len(framePeaks) == 0 {
		return []Tone{}
	}

	// Calculate noise floor
	globalPeak := 0.0
	for _, peak := range framePeaks {
		if peak > globalPeak {
			globalPeak = peak
		}
	}

	if globalPeak < 1e-20 {
		return []Tone{}
	}

	relativeDB := make([]float64, len(framePeaks))
	for i, peak := range framePeaks {
		relativeDB[i] = 20.0 * math.Log10(math.Max(peak, 1e-20)/globalPeak)
	}

	sortedDB := make([]float64, len(relativeDB))
	copy(sortedDB, relativeDB)
	sort.Float64s(sortedDB)
	q20Index := int(float64(len(sortedDB)) * 0.20)
	q20 := sortedDB[q20Index]

	var belowQ20 []float64
	for _, db := range relativeDB {
		if db <= q20 {
			belowQ20 = append(belowQ20, db)
		}
	}

	noiseFloorDB := -60.0
	if len(belowQ20) > 0 {
		sort.Float64s(belowQ20)
		noiseFloorDB = belowQ20[len(belowQ20)/2]
	}

	silenceBelowGlobalDB := -28.0
	snrAboveNoiseDB := 6.0

	// Second pass: detect tones
	for win := 0; win < numWindows; win++ {
		start := win * hopSize
		end := start + windowSize
		if end > len(samples) {
			break
		}

		window := samples[start:end]
		windowStartTime := float64(start) / float64(sampleRate)
		windowEndTime := float64(end) / float64(sampleRate)

		frameDB := relativeDB[win]
		isSilent := frameDB < silenceBelowGlobalDB || frameDB < (noiseFloorDB+snrAboveNoiseDB)
		if isSilent {
			continue
		}

		windowed := make([]float64, len(window))
		for i := range window {
			hann := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(len(window)-1)))
			windowed[i] = window[i] * hann
		}

		magnitudes := detector.dft(windowed, sampleRate)

		// Speech (steady vowel pitch, 200-650 Hz) also produces a stable spectral peak, so a loud
		// bin alone is not evidence of a dispatch tone. Only frames whose energy is concentrated in
		// a few narrow peaks count; otherwise voice gets cut out and the call is skipped as tone-only.
		if spectralTonality(magnitudes, windowSize, sampleRate) < minTranscriptionToneTonality {
			continue
		}

		for bin, mag := range magnitudes {
			freq := float64(bin) * float64(sampleRate) / float64(windowSize)

			// Detect tones in dispatch range (200-5000Hz)
			if freq >= 200.0 && freq <= 5000.0 && mag > 0.02 {
				// Parabolic interpolation
				binMinus := bin - 1
				binPlus := bin + 1
				if binMinus >= 0 && binPlus < len(magnitudes) {
					magMinus := magnitudes[binMinus]
					magPlus := magnitudes[binPlus]
					delta := parabolicInterpolate(magMinus, mag, magPlus)
					delta = math.Max(-0.5, math.Min(0.5, delta))
					binWidth := float64(sampleRate) / float64(windowSize)
					freq += delta * binWidth
				}

				// Check if this extends an existing detection
				found := false
				for freqBin, detectionList := range detections {
					binFreq := float64(freqBin * 10)
					if math.Abs(freq-binFreq) <= 20.0 {
						for i := range detectionList {
							if windowStartTime <= detectionList[i].endTime && windowEndTime >= detectionList[i].startTime {
								if windowEndTime > detectionList[i].endTime {
									detectionList[i].endTime = windowEndTime
								}
								if windowStartTime < detectionList[i].startTime {
									detectionList[i].startTime = windowStartTime
								}
								if mag > detectionList[i].magnitude {
									detectionList[i].magnitude = mag
									detectionList[i].frequency = freq
								}
								found = true
								break
							}
						}
						if found {
							break
						}
					}
				}

				if !found {
					freqBin := int(freq / 10.0)
					if detections[freqBin] == nil {
						detections[freqBin] = []freqDetection{}
					}
					detections[freqBin] = append(detections[freqBin], freqDetection{
						frequency: freq,
						startTime: windowStartTime,
						endTime:   windowEndTime,
						magnitude: mag,
					})
				}
			}
		}
	}

	// Merge nearby detections
	type mergedDetection struct {
		frequency float64
		startTime float64
		endTime   float64
		magnitude float64
	}

	var mergedDetections []mergedDetection

	for _, detectionList := range detections {
		for _, det := range detectionList {
			duration := det.endTime - det.startTime
			if duration >= minToneDuration {
				merged := false
				for i := range mergedDetections {
					md := &mergedDetections[i]
					freqDiff := math.Abs(det.frequency - md.frequency)
					timeOverlap := (det.startTime <= md.endTime+0.1 && det.endTime >= md.startTime-0.1)

					if freqDiff <= 25.0 && timeOverlap {
						md.frequency = (md.frequency + det.frequency) / 2.0
						if det.startTime < md.startTime {
							md.startTime = det.startTime
						}
						if det.endTime > md.endTime {
							md.endTime = det.endTime
						}
						if det.magnitude > md.magnitude {
							md.magnitude = det.magnitude
						}
						merged = true
						break
					}
				}

				if !merged {
					mergedDetections = append(mergedDetections, mergedDetection{
						frequency: det.frequency,
						startTime: det.startTime,
						endTime:   det.endTime,
						magnitude: det.magnitude,
					})
				}
			}
		}
	}

	// Convert to Tone objects
	var tones []Tone
	for _, md := range mergedDetections {
		duration := md.endTime - md.startTime
		if duration >= minToneDuration {
			tones = append(tones, Tone{
				Frequency: md.frequency,
				StartTime: md.startTime,
				EndTime:   md.endTime,
				Duration:  duration,
				ToneType:  "", // Not matched to any tone set
			})
			fmt.Printf("detected tone for removal: %.1f Hz for %.2fs (%.2f-%.2fs)\n",
				md.frequency, duration, md.startTime, md.endTime)
		}
	}

	return tones
}
