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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Tone represents a detected tone with frequency and timing information
type Tone struct {
	Frequency float64 `json:"frequency"` // Hz
	StartTime float64 `json:"startTime"` // seconds from start of audio
	EndTime   float64 `json:"endTime"`   // seconds from start of audio
	Duration  float64 `json:"duration"`  // seconds
	ToneType  string  `json:"toneType"`  // Type of tone: "A", "B", "Long", or "" if matched multiple/none
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
		MinToneDuration: 0.6,   // Minimum 600ms to be considered a tone
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

	// Convert audio to WAV PCM format using ffmpeg
	tempDir := os.TempDir()
	srcFile := filepath.Join(tempDir, fmt.Sprintf("tone_src_%d.m4a", time.Now().UnixNano()))
	wavFile := filepath.Join(tempDir, fmt.Sprintf("tone_wav_%d.wav", time.Now().UnixNano()))

	// Write source audio to temp file
	if err := os.WriteFile(srcFile, audio, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temp audio file: %v", err)
	}
	defer os.Remove(srcFile)
	defer os.Remove(wavFile)

	// Convert to WAV 16kHz mono with bandpass filter for tone detection
	// Bandpass 200-3000 Hz isolates typical paging tones and reduces noise/DC offset
	// This significantly improves detection on analog conventional channels
	ffArgs := []string{
		"-y", "-loglevel", "error",
		"-i", srcFile,
		"-ar", "16000", // 16kHz sample rate (can capture up to 8kHz via Nyquist, sufficient for 0-5000 Hz)
		"-ac", "1", // Mono
		"-af", "highpass=f=200,lowpass=f=3000,dynaudnorm", // Bandpass filter + dynamic normalization
		"-f", "wav",
		wavFile,
	}
	ffCmd := exec.Command("ffmpeg", ffArgs...)
	var ffErr bytes.Buffer
	ffCmd.Stderr = &ffErr
	if err := ffCmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %v, stderr: %s", err, ffErr.String())
	}

	// Read WAV file
	wavData, err := os.ReadFile(wavFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read WAV file: %v", err)
	}

	// Parse WAV and extract PCM samples
	samples, sampleRate, err := detector.parseWAV(wavData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse WAV: %v", err)
	}

	if len(samples) < 100 {
		return &ToneSequence{Tones: []Tone{}, HasTones: false}, nil
	}

	// Perform FFT analysis to detect tones
	detectedTones := detector.analyzeFrequencies(samples, sampleRate, toneSets)

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

	// Identify ATone, BTone, LongTone based on what they matched in the tone sets
	// Use the ToneType field that was set during matching
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

	return sequence, nil
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

// analyzeFrequencies performs FFT analysis to detect sustained tones
// Enhanced with dynamic noise floor estimation, parabolic interpolation, and force-split detection
// Techniques inspired by icad_tone_detection (thegreatcodeholio) for improved analog channel detection
func (detector *ToneDetector) analyzeFrequencies(samples []float64, sampleRate int, toneSets []ToneSet) []Tone {
	windowSize := 2048     // FFT window size
	hopSize := 512         // Slide window by this much
	minToneDuration := 0.6 // Minimum 600ms to be considered a tone
	toneRange := detector.FrequencyRange

	if toneRange.Min == 0 {
		toneRange.Min = 0.0 // Can detect from 0 Hz
	}
	if toneRange.Max == 0 {
		toneRange.Max = 5000.0 // Up to 5000 Hz
	}

	// Track detected frequencies over time
	type freqDetection struct {
		frequency float64
		startTime float64
		endTime   float64
		magnitude float64
	}

	detections := make(map[int][]freqDetection) // frequency bin -> detections

	// For dynamic noise floor estimation
	var framePeaks []float64

	// First pass: collect frame peaks for noise floor estimation
	numWindows := (len(samples) - windowSize) / hopSize
	for win := 0; win < numWindows; win++ {
		start := win * hopSize
		end := start + windowSize
		if end > len(samples) {
			break
		}

		window := samples[start:end]

		// Apply window function (Hann window) to reduce spectral leakage
		windowed := make([]float64, len(window))
		for i := range window {
			hann := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(len(window)-1)))
			windowed[i] = window[i] * hann
		}

		// Perform DFT (Discrete Fourier Transform)
		magnitudes := detector.dft(windowed, sampleRate)

		// Find peak magnitude in tone range for this frame
		var framePeak float64
		for bin, mag := range magnitudes {
			freq := float64(bin) * float64(sampleRate) / float64(windowSize)
			if freq >= toneRange.Min && freq <= toneRange.Max && mag > framePeak {
				framePeak = mag
			}
		}
		framePeaks = append(framePeaks, framePeak)
	}

	// Calculate dynamic noise floor (20th percentile method from icad_tone_detection)
	if len(framePeaks) == 0 {
		return []Tone{}
	}

	// Find global peak
	globalPeak := 0.0
	for _, peak := range framePeaks {
		if peak > globalPeak {
			globalPeak = peak
		}
	}

	if globalPeak < 1e-20 {
		return []Tone{}
	}

	// Calculate relative dB for each frame
	relativeDB := make([]float64, len(framePeaks))
	for i, peak := range framePeaks {
		relativeDB[i] = 20.0 * math.Log10(math.Max(peak, 1e-20)/globalPeak)
	}

	// Sort to find 20th percentile
	sortedDB := make([]float64, len(relativeDB))
	copy(sortedDB, relativeDB)
	sort.Float64s(sortedDB)
	q20Index := int(float64(len(sortedDB)) * 0.20)
	q20 := sortedDB[q20Index]

	// Calculate noise floor as median of values below q20
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

	// Silence gating thresholds (from icad_tone_detection defaults)
	silenceBelowGlobalDB := -28.0 // Frame must be within 28 dB of global peak
	snrAboveNoiseDB := 6.0        // Frame must be 6 dB above noise floor

	fmt.Printf("tone detection: global peak=%.4f, noise floor=%.1f dB, q20=%.1f dB\n", globalPeak, noiseFloorDB, q20)

	// Second pass: analyze in sliding windows with noise gating
	for win := 0; win < numWindows; win++ {
		start := win * hopSize
		end := start + windowSize
		if end > len(samples) {
			break
		}

		window := samples[start:end]
		windowStartTime := float64(start) / float64(sampleRate)
		windowEndTime := float64(end) / float64(sampleRate) // Actual end time of window

		// Check if this frame passes noise gate
		frameDB := relativeDB[win]
		isSilent := frameDB < silenceBelowGlobalDB || frameDB < (noiseFloorDB+snrAboveNoiseDB)
		if isSilent {
			continue // Skip silent frames
		}

		// Apply window function (Hann window) to reduce spectral leakage
		windowed := make([]float64, len(window))
		for i := range window {
			hann := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(len(window)-1)))
			windowed[i] = window[i] * hann
		}

		// Perform DFT (Discrete Fourier Transform)
		magnitudes := detector.dft(windowed, sampleRate)

		// Find peaks in tone range with parabolic interpolation
		for bin, mag := range magnitudes {
			freq := float64(bin) * float64(sampleRate) / float64(windowSize)

			// Basic magnitude check (much lower threshold now that we have noise gating)
			if freq >= toneRange.Min && freq <= toneRange.Max && mag > 0.02 {
				// Parabolic interpolation for sub-bin accuracy
				binMinus := bin - 1
				binPlus := bin + 1
				if binMinus >= 0 && binPlus < len(magnitudes) {
					magMinus := magnitudes[binMinus]
					magPlus := magnitudes[binPlus]
					delta := parabolicInterpolate(magMinus, mag, magPlus)
					delta = math.Max(-0.5, math.Min(0.5, delta)) // Clamp to [-0.5, 0.5]
					// Apply sub-bin correction
					binWidth := float64(sampleRate) / float64(windowSize)
					freq += delta * binWidth
				}
				// Check if this frequency is close to any existing detection (within ±15 Hz) and overlaps in time
				// This prevents creating separate detections for the same tone detected at slightly different frequencies
				found := false
				for freqBin, detectionList := range detections {
					binFreq := float64(freqBin * 10) // Approximate frequency for this bin
					if math.Abs(freq-binFreq) <= 15.0 {
						// Check if any detection in this bin overlaps with current window
						for i := range detectionList {
							// Check if windows overlap (current window overlaps with detection time range)
							if windowStartTime <= detectionList[i].endTime && windowEndTime >= detectionList[i].startTime {
								// Same tone detected - extend the detection
								if windowEndTime > detectionList[i].endTime {
									detectionList[i].endTime = windowEndTime
								}
								if windowStartTime < detectionList[i].startTime {
									detectionList[i].startTime = windowStartTime
								}
								if mag > detectionList[i].magnitude {
									detectionList[i].magnitude = mag
									detectionList[i].frequency = freq // Update to closer frequency
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
					// Create new detection - use frequency bin but track actual frequency
					freqBin := int(freq / 10.0)
					if detections[freqBin] == nil {
						detections[freqBin] = []freqDetection{}
					}

					detections[freqBin] = append(detections[freqBin], freqDetection{
						frequency: freq,
						startTime: windowStartTime,
						endTime:   windowEndTime, // Use actual window end time
						magnitude: mag,
					})
				}
			}
		}
	}

	// Merge nearby frequency detections to avoid duplicate detections of the same tone
	// Group detections by similar frequency and time overlap
	type mergedDetection struct {
		frequency   float64   // Average frequency
		startTime   float64   // Earliest start
		endTime     float64   // Latest end
		magnitude   float64   // Highest magnitude
		count       int       // Number of detections merged
		freqHistory []float64 // Track frequency progression for force-split detection
	}

	mergedDetections := []mergedDetection{}

	// Force-split parameters (from icad_tone_detection)
	forceSplitStepHz := 18.0 // Force split if frequency jumps > 18 Hz between consecutive detections
	splitLookahead := 2      // Number of frames to look ahead to confirm split

	for _, detectionList := range detections {
		for _, det := range detectionList {
			duration := det.endTime - det.startTime

			if duration >= minToneDuration {
				// Try to merge with existing merged detection
				merged := false
				for i := range mergedDetections {
					md := &mergedDetections[i]
					freqDiff := math.Abs(det.frequency - md.frequency)

					// Check for force-split condition: large frequency jump indicates different tone
					forceSplit := false
					if len(md.freqHistory) >= splitLookahead {
						// Calculate recent median frequency
						recentFreqs := md.freqHistory[len(md.freqHistory)-splitLookahead:]
						sort.Float64s(recentFreqs)
						recentMedian := recentFreqs[len(recentFreqs)/2]

						// If frequency jumps too much from recent median, force split
						if math.Abs(det.frequency-recentMedian) > forceSplitStepHz {
							forceSplit = true
						}
					}

					// Only merge if frequencies are within ±20 Hz (increased for analog drift) AND times overlap AND no force-split
					// Increased from ±15 Hz to ±20 Hz to handle analog channel frequency drift
					// For A-tones: typically 300-600 Hz range, ±20 Hz covers drift + Doppler
					// For B-tones: typically 1000-1200 Hz range, ±20 Hz covers drift + Doppler
					// We use a small tolerance (0.1s) to handle cases where one tone ends exactly when another starts
					// (could be the same tone with a tiny gap), but we don't merge tones that are clearly separate
					timeOverlap := (det.startTime <= md.endTime+0.1 && det.endTime >= md.startTime-0.1)

					// Only merge if frequencies are close AND times overlap AND no force-split
					// This prevents merging separate tone sets in stacked tone scenarios
					if freqDiff <= 20.0 && timeOverlap && !forceSplit {
						// Merge: use weighted average frequency, extend time range, use max magnitude
						oldFreq := md.frequency
						totalCount := md.count + 1
						md.frequency = (md.frequency*float64(md.count) + det.frequency) / float64(totalCount)
						if det.startTime < md.startTime {
							md.startTime = det.startTime
						}
						if det.endTime > md.endTime {
							md.endTime = det.endTime
						}
						if det.magnitude > md.magnitude {
							md.magnitude = det.magnitude
						}
						md.count = totalCount
						md.freqHistory = append(md.freqHistory, det.frequency)
						fmt.Printf("merged tone %.1f Hz (%.2fs) with existing %.1f Hz -> %.1f Hz (merged %d detections, time: %.2f-%.2fs)\n",
							det.frequency, det.endTime-det.startTime, oldFreq, md.frequency, totalCount, md.startTime, md.endTime)
						merged = true
						break
					}
				}

				if !merged {
					// Create new merged detection
					mergedDetections = append(mergedDetections, mergedDetection{
						frequency:   det.frequency,
						startTime:   det.startTime,
						endTime:     det.endTime,
						magnitude:   det.magnitude,
						count:       1,
						freqHistory: []float64{det.frequency},
					})
				}
			}
		}
	}

	// Convert merged detections to tones (filter by duration and match against tone sets)
	var tones []Tone
	var allDetections []freqDetection // For logging all detected frequencies (before merging)

	// Log all raw detections for debugging
	for _, detectionList := range detections {
		for _, det := range detectionList {
			if det.endTime-det.startTime >= minToneDuration {
				allDetections = append(allDetections, det)
			}
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
							actualTol = ts.ATone.Frequency * baseTol
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
							actualTol = ts.BTone.Frequency * baseTol
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
							actualTol = ts.LongTone.Frequency * baseTol
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
		return false
	}

	// Require B-tone if configured
	if toneSet.BTone != nil && len(bTones) == 0 {
		return false
	}

	// Note: If tone set has A/B tones, we do NOT check for long tones
	// Long tones are only checked if the tone set has NO A/B tones (handled by early return above)

	// If both A-tone and B-tone are required, they must form a valid sequence
	// Each A-tone must be paired with its closest following B-tone (within 0.5s)
	// This prevents false matches where an A-tone pairs with a B-tone from a different tone sequence
	if toneSet.ATone != nil && toneSet.BTone != nil {
		// Sort A-tones by start time to process them in sequence
		aTonesSorted := make([]matchingTone, len(aTones))
		copy(aTonesSorted, aTones)
		sort.Slice(aTonesSorted, func(i, j int) bool {
			return aTonesSorted[i].tone.StartTime < aTonesSorted[j].tone.StartTime
		})

		// Check each A-tone against the tone set's B-tone
		// Each A-tone must find its closest following B-tone that matches this tone set
		for _, aMatch := range aTonesSorted {
			// Find the closest following B-tone within 0.5s gap
			// "Closest" means the smallest gap (either negative for overlap, or positive for sequential)
			var closestB *matchingTone
			var closestGap float64
			hasClosest := false

			for i := range bTones {
				bMatch := &bTones[i]

				// B-tone must end after or when A ends (ensures A comes first in sequence)
				if bMatch.tone.EndTime < aMatch.tone.EndTime {
					continue // B ends before A ends, not a valid sequence
				}

				// Calculate gap (B start time - A end time)
				// Negative = overlapping (B starts before A ends)
				// Positive = sequential (B starts after A ends)
				gap := bMatch.tone.StartTime - aMatch.tone.EndTime

				// Must be within 0.5s gap (either overlap up to 0.5s, or sequential up to 0.5s)
				if gap >= -0.5 && gap <= 0.5 {
					// Check if this is closer than previous closest
					if !hasClosest {
						closestB = bMatch
						closestGap = gap
						hasClosest = true
					} else {
						// Compare absolute gaps - want the one closest to 0
						if math.Abs(gap) < math.Abs(closestGap) {
							closestB = bMatch
							closestGap = gap
						}
					}
				}
			}

			// If we found a closest B-tone, check if it matches this tone set's B-tone frequency
			if closestB != nil {
				// Check if the closest B-tone matches the tone set's B-tone frequency
				actualTolerance := baseTolerance
				if baseTolerance < 1.0 {
					actualTolerance = baseTolerance * 500.0
				}

				if detector.frequencyMatches(closestB.tone.Frequency, toneSet.BTone.Frequency, actualTolerance) {
					// Found a valid A-B pair where A-tone pairs with its closest B-tone
					// and that closest B-tone matches this tone set's B-tone
					return true
				}
			}
		}

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

	// Create temp files
	tempDir := os.TempDir()
	srcFile := filepath.Join(tempDir, fmt.Sprintf("tone_filter_src_%d.m4a", time.Now().UnixNano()))
	outFile := filepath.Join(tempDir, fmt.Sprintf("tone_filter_out_%d.m4a", time.Now().UnixNano()))

	// Write source audio
	if err := os.WriteFile(srcFile, audio, 0644); err != nil {
		return audio, fmt.Errorf("failed to write temp audio: %v", err)
	}
	defer os.Remove(srcFile)
	defer os.Remove(outFile)

	// Get total audio duration
	durationCmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		srcFile,
	)
	durationOutput, err := durationCmd.Output()
	if err != nil {
		return audio, fmt.Errorf("failed to get audio duration: %v", err)
	}

	var totalDuration float64
	if _, err := fmt.Sscanf(string(durationOutput), "%f", &totalDuration); err != nil {
		return audio, fmt.Errorf("failed to parse duration: %v", err)
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
	// Add small buffer (0.1s) around tones to ensure complete removal
	const toneBuffer = 0.1

	type segment struct {
		start, end float64
	}
	var keepSegments []segment

	currentPos := 0.0
	for _, tone := range sortedTones {
		toneStart := math.Max(0, tone.StartTime-toneBuffer)
		toneEnd := math.Min(totalDuration, tone.EndTime+toneBuffer)

		// Add segment before this tone
		if currentPos < toneStart {
			keepSegments = append(keepSegments, segment{currentPos, toneStart})
		}

		// Skip the tone itself
		currentPos = toneEnd
	}

	// Add final segment after last tone
	if currentPos < totalDuration {
		keepSegments = append(keepSegments, segment{currentPos, totalDuration})
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

	// Run ffmpeg with filter
	ffArgs := []string{
		"-y", "-loglevel", "error",
		"-i", srcFile,
		"-filter_complex", filterComplex,
		"-map", "[out]",
		"-ar", "16000",          // 16kHz sample rate
		"-ac", "1",              // Mono
		"-c:a", "libopus",       // Encode to Opus (was aac)
		"-b:a", "16k",           // 16 kbps (was 64k - voice optimized)
		"-application", "voip",  // Voice optimization
		"-f", "opus",            // Opus format
		outFile,
	}

	fmt.Printf("audio filtering: removing %d tone segments (%.2fs of tones from %.2fs total)\n",
		len(sortedTones), calculateTotalToneDuration(sortedTones), totalDuration)

	ffCmd := exec.Command("ffmpeg", ffArgs...)
	var ffErr bytes.Buffer
	ffCmd.Stderr = &ffErr
	if err := ffCmd.Run(); err != nil {
		return audio, fmt.Errorf("ffmpeg filtering failed: %v, stderr: %s", err, ffErr.String())
	}

	// Read filtered audio
	filteredAudio, err := os.ReadFile(outFile)
	if err != nil {
		return audio, fmt.Errorf("failed to read filtered audio: %v", err)
	}

	// Verify we got something back
	if len(filteredAudio) < 1000 {
		fmt.Printf("audio filtering: filtered audio too small (%d bytes), returning original\n", len(filteredAudio))
		return audio, nil
	}

	fmt.Printf("audio filtering: success - original: %d bytes, filtered: %d bytes (removed %.1f%%)\n",
		len(audio), len(filteredAudio), (1.0-float64(len(filteredAudio))/float64(len(audio)))*100)

	return filteredAudio, nil
}

// calculateTotalToneDuration calculates total duration of all tones
func calculateTotalToneDuration(tones []Tone) float64 {
	total := 0.0
	for _, tone := range tones {
		total += tone.Duration
	}
	return total
}
