// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"math/cmplx"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	// fpSampleRate is the audio sample rate used for fingerprinting.
	// 11025 Hz is sufficient for voice (Nyquist = 5512 Hz) and faster to process than 16kHz.
	fpSampleRate = 11025

	// fpFrameSize is the FFT window size in samples (~0.37s at 11025 Hz).
	fpFrameSize = 4096

	// fpHopSize is the hop between frames in samples (50% overlap).
	fpHopSize = 2048

	// fpNumBands is the number of logarithmic frequency bands per frame.
	fpNumBands = 8

	// fpFreqMin/Max defines the voice frequency range for fingerprinting (Hz).
	fpFreqMin = 200.0
	fpFreqMax = 3500.0
)

// fingerprintCacheEntry holds a recently-seen fingerprint in memory.
type fingerprintCacheEntry struct {
	fingerprint   []int32
	timestamp     time.Time   // when this entry was added (server receive time)
	callTimestamp int64       // call.Timestamp.UnixMilli() — the radio transmission time
}

// FingerprintCache is an in-memory store of recently generated fingerprints.
// It is checked alongside the DB to catch simultaneous duplicate uploads that
// haven't been committed to the database yet (race condition where two recorders
// upload the same transmission at the exact same moment).
type FingerprintCache struct {
	mu      sync.Mutex
	entries map[string][]fingerprintCacheEntry // key: "systemId:talkgroupId"
	ttl     time.Duration
}

// NewFingerprintCache creates a cache with the given TTL for entries.
func NewFingerprintCache(ttl time.Duration) *FingerprintCache {
	c := &FingerprintCache{
		entries: make(map[string][]fingerprintCacheEntry),
		ttl:     ttl,
	}
	go c.runCleanup()
	return c
}

// cacheKey builds the map key for a system+talkgroup pair.
func (c *FingerprintCache) cacheKey(systemId, talkgroupId uint64) string {
	return strconv.FormatUint(systemId, 10) + ":" + strconv.FormatUint(talkgroupId, 10)
}

// CheckAndAdd checks the cache for a duplicate and, if none found, registers
// the new call atomically. Returns true if a duplicate was found.
//
// Two checks are performed:
//  1. Timestamp match: if the incoming call's radio timestamp is within
//     metadataWindowMs of any cached call's timestamp → duplicate.
//     This replicates the legacy metadata check in-memory, eliminating the
//     DB race condition for simultaneous uploads.
//  2. Fingerprint match: if Hamming distance is within threshold → duplicate.
//     This catches calls with slightly different timestamps but same content.
func (c *FingerprintCache) CheckAndAdd(
	systemId, talkgroupId uint64,
	fp []int32,
	callTimestampMs int64,
	fingerprinter *AudioFingerprinter,
	threshold float64,
	metadataWindowMs int64,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := c.cacheKey(systemId, talkgroupId)
	now := time.Now()
	cutoff := now.Add(-c.ttl)

	var live []fingerprintCacheEntry
	isDuplicate := false

	for _, entry := range c.entries[key] {
		if entry.timestamp.Before(cutoff) {
			continue // expired
		}
		live = append(live, entry)
		if isDuplicate {
			continue
		}

		// Check 1: timestamp window (same logic as legacy metadata check, but in-memory)
		diff := callTimestampMs - entry.callTimestamp
		if diff < 0 {
			diff = -diff
		}
		if diff <= metadataWindowMs {
			isDuplicate = true
			continue
		}

		// Check 2: fingerprint similarity (adaptive threshold for short clips).
		// Skip if either fingerprint is too short to compare reliably (< 3 integers = < 96 bits).
		if len(fp) >= 3 && len(entry.fingerprint) >= 3 {
			minLen := len(fp)
			if len(entry.fingerprint) < minLen {
				minLen = len(entry.fingerprint)
			}
			dist := fingerprinter.Compare(fp, entry.fingerprint)
			if dist <= adaptiveThreshold(threshold, minLen) {
				isDuplicate = true
			}
		}
	}

	// Always register so subsequent simultaneous calls see this one.
	live = append(live, fingerprintCacheEntry{
		fingerprint:   fp,
		timestamp:     now,
		callTimestamp: callTimestampMs,
	})
	c.entries[key] = live

	return isDuplicate
}

// runCleanup periodically removes expired entries to prevent unbounded growth.
func (c *FingerprintCache) runCleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		cutoff := time.Now().Add(-c.ttl)
		for key, entries := range c.entries {
			var live []fingerprintCacheEntry
			for _, e := range entries {
				if !e.timestamp.Before(cutoff) {
					live = append(live, e)
				}
			}
			if len(live) == 0 {
				delete(c.entries, key)
			} else {
				c.entries[key] = live
			}
		}
		c.mu.Unlock()
	}
}

// emitCacheEntry holds a recently emitted call's fingerprint for cross-talkgroup dedup.
type emitCacheEntry struct {
	fingerprint   []int32
	callTimestamp int64  // radio transmission time (ms)
	talkgroupId   uint64 // which talkgroup this was emitted on
	receivedAt    time.Time
}

// EmitFingerprintCache suppresses cross-talkgroup duplicate streaming for patched
// talkgroups. When the same transmission arrives on FINDLAY and POST41 simultaneously,
// both calls are saved to the DB (history preserved) but only the first is streamed
// to clients. The cache is keyed by systemId so fingerprints are compared across ALL
// talkgroups in the same system.
type EmitFingerprintCache struct {
	mu      sync.Mutex
	entries map[uint64][]emitCacheEntry // key: systemId
	ttl     time.Duration
}

// NewEmitFingerprintCache creates a cross-talkgroup emit dedup cache.
func NewEmitFingerprintCache(ttl time.Duration) *EmitFingerprintCache {
	c := &EmitFingerprintCache{
		entries: make(map[uint64][]emitCacheEntry),
		ttl:     ttl,
	}
	go c.runCleanup()
	return c
}

// CheckAndRegister checks whether the same audio was recently emitted on a
// different talkgroup in the same system (patch scenario). If not, it registers
// this call so future patch duplicates are suppressed. Returns true if emit
// should be suppressed.
func (c *EmitFingerprintCache) CheckAndRegister(
	systemId, talkgroupId uint64,
	fp []int32,
	callTimestampMs int64,
	fingerprinter *AudioFingerprinter,
	threshold float64,
	metadataWindowMs int64,
) bool {
	if len(fp) == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-c.ttl)
	var live []emitCacheEntry
	isDuplicate := false

	for _, entry := range c.entries[systemId] {
		if entry.receivedAt.Before(cutoff) {
			continue
		}
		live = append(live, entry)
		if isDuplicate {
			continue
		}
		// Only suppress if it's a DIFFERENT talkgroup (same talkgroup = ingest dedup handles it)
		if entry.talkgroupId == talkgroupId {
			continue
		}
		// Timestamp must be within the window
		diff := callTimestampMs - entry.callTimestamp
		if diff < 0 {
			diff = -diff
		}
		if diff > metadataWindowMs {
			continue
		}
		// Fingerprint similarity check with adaptive threshold.
		// Skip if either fingerprint is too short to compare reliably (< 3 integers = < 96 bits).
		if len(fp) < 3 || len(entry.fingerprint) < 3 {
			continue
		}
		minLen := len(fp)
		if len(entry.fingerprint) < minLen {
			minLen = len(entry.fingerprint)
		}
		dist := fingerprinter.Compare(fp, entry.fingerprint)
		if dist <= adaptiveThreshold(threshold, minLen) {
			isDuplicate = true
		}
	}

	// Always register this call so subsequent patch duplicates are caught.
	live = append(live, emitCacheEntry{
		fingerprint:   fp,
		callTimestamp: callTimestampMs,
		talkgroupId:   talkgroupId,
		receivedAt:    now,
	})
	c.entries[systemId] = live

	return isDuplicate
}

// runCleanup periodically removes expired entries.
func (c *EmitFingerprintCache) runCleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		cutoff := time.Now().Add(-c.ttl)
		for key, entries := range c.entries {
			var live []emitCacheEntry
			for _, e := range entries {
				if !e.receivedAt.Before(cutoff) {
					live = append(live, e)
				}
			}
			if len(live) == 0 {
				delete(c.entries, key)
			} else {
				c.entries[key] = live
			}
		}
		c.mu.Unlock()
	}
}

// AudioFingerprinter generates compact spectral fingerprints from radio call audio.
//
// The algorithm works by:
//  1. Normalizing audio to 11025 Hz mono via FFmpeg (removes absolute volume differences)
//  2. Dividing into overlapping frames (~0.37s, 50% overlap)
//  3. For each frame: Hann window → FFT → 8 log-spaced frequency band energies
//  4. Comparing adjacent-band energy deltas between consecutive frames → 1 bit per band
//  5. Packing bits into a []int32 fingerprint array
//
// Two recordings of the same transmission (from different sites, different noise floors)
// produce fingerprints with low Hamming distance. Completely different audio produces
// fingerprints with ~50% bit difference.
type AudioFingerprinter struct {
	available bool
}

// NewAudioFingerprinter creates a new fingerprinter. Checks for FFmpeg availability.
func NewAudioFingerprinter() *AudioFingerprinter {
	fp := &AudioFingerprinter{}
	if err := exec.Command("ffmpeg", "-version").Run(); err == nil {
		fp.available = true
	}
	return fp
}

// Available reports whether fingerprinting is available (requires FFmpeg).
func (fp *AudioFingerprinter) Available() bool {
	return fp.available
}

// Generate computes a spectral fingerprint from raw audio bytes.
// Returns a []int32 fingerprint or an error if decoding fails.
func (fp *AudioFingerprinter) Generate(audio []byte, audioMime string) ([]int32, error) {
	if !fp.available {
		return nil, fmt.Errorf("fingerprint: ffmpeg not available")
	}
	if len(audio) < 1000 {
		return nil, fmt.Errorf("fingerprint: audio too short (%d bytes)", len(audio))
	}

	// Normalize to 11025 Hz mono WAV via FFmpeg stdin→stdout (no temp files).
	// dynaudnorm ensures loudness differences between sites don't skew band energies.
	ffArgs := []string{
		"-loglevel", "error",
		"-i", "pipe:0",
		"-ar", strconv.Itoa(fpSampleRate),
		"-ac", "1",
		"-af", "dynaudnorm",
		"-f", "wav",
		"pipe:1",
	}
	cmd := exec.Command("ffmpeg", ffArgs...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("fingerprint: stdin pipe: %w", err)
	}

	var wavBuf, errBuf bytes.Buffer
	cmd.Stdout = &wavBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("fingerprint: ffmpeg start: %w", err)
	}
	go func() {
		defer stdin.Close()
		stdin.Write(audio) //nolint:errcheck
	}()
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("fingerprint: ffmpeg: %w (stderr: %s)", err, errBuf.String())
	}
	if wavBuf.Len() == 0 {
		return nil, fmt.Errorf("fingerprint: ffmpeg produced no output")
	}

	samples, err := fp.parseWAV(wavBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("fingerprint: WAV parse: %w", err)
	}
	if len(samples) < fpFrameSize {
		return nil, fmt.Errorf("fingerprint: decoded audio too short (%d samples)", len(samples))
	}

	return fp.computeFingerprint(samples), nil
}

// Compare returns the normalized Hamming distance between two fingerprints [0.0–1.0].
//
//   - 0.00 – 0.15 → likely same audio content (duplicate)
//   - 0.15 – 0.30 → possibly related (borderline, depends on threshold)
//   - 0.40 – 0.50 → unrelated audio
//
// Uses a bidirectional sliding window so it handles:
//   - Different length fingerprints (one call shorter than the other)
//   - Same length but time-shifted fingerprints (one recorder started a few
//     seconds earlier/later, producing same-length arrays that are offset in time)
//
// fpMaxShift controls how many integer positions to try in each direction.
// Each integer represents ~0.85s of audio, so 5 positions ≈ ±4 seconds of offset.
const fpMaxShift = 5

func (fp *AudioFingerprinter) Compare(a, b []int32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 1.0
	}

	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	// Require at least 2 integers of overlap for a meaningful comparison.
	minOverlap := 2

	bestDist := 1.0

	// Try all shifts from -fpMaxShift to +fpMaxShift.
	// shift > 0: b starts later (compare a[shift:] against b)
	// shift = 0: aligned
	// shift < 0: a starts later (compare a against b[-shift:])
	for shift := -fpMaxShift; shift <= fpMaxShift; shift++ {
		var subA, subB []int32

		if shift >= 0 {
			if shift >= len(a) {
				continue
			}
			subA = a[shift:]
			subB = b
		} else {
			absShift := -shift
			if absShift >= len(b) {
				continue
			}
			subA = a
			subB = b[absShift:]
		}

		// Take the shorter of the two overlapping tails.
		l := len(subA)
		if len(subB) < l {
			l = len(subB)
		}
		if l < minOverlap {
			continue
		}

		dist := hammingDist(subA[:l], subB[:l])
		if dist < bestDist {
			bestDist = dist
		}
		if bestDist == 0.0 {
			break
		}
	}

	return bestDist
}

// hammingDist computes normalized Hamming distance between two equal-length int32 slices.
func hammingDist(a, b []int32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 1.0
	}
	var diffBits int
	for i := range a {
		diffBits += bits.OnesCount32(uint32(a[i]) ^ uint32(b[i]))
	}
	return float64(diffBits) / float64(len(a)*32)
}

// adaptiveThreshold returns a more permissive threshold for short fingerprints.
// Short clips (1-2s) produce only 3-5 integers. With so few bits, even minor
// encoding differences between receivers push the raw Hamming distance above 0.25,
// causing same-transmission calls to be missed. Longer fingerprints are more
// statistically stable so the configured threshold applies as-is.
//
//   < 3 integers  → 0.0 (skip: not enough data for a reliable comparison)
//   3–4 integers  → threshold + 0.05  (minimal boost, short clip)
//   5–9 integers  → threshold + 0.03  (slight boost, moderately short)
//   ≥ 10 integers → threshold as-is   (enough data to be reliable)
func adaptiveThreshold(baseThreshold float64, minLen int) float64 {
	switch {
	case minLen < 3:
		return 0.0 // not enough bits — caller must skip when dist > 0
	case minLen < 5:
		return baseThreshold + 0.05
	case minLen < 10:
		return baseThreshold + 0.03
	default:
		return baseThreshold
	}
}

// computeFingerprint converts normalized PCM samples into a []int32 fingerprint.
func (fp *AudioFingerprinter) computeFingerprint(samples []float64) []int32 {
	// Logarithmically-spaced band boundaries covering the voice range.
	bandBoundaries := logspacedBands(fpFreqMin, fpFreqMax, fpNumBands+1)

	numFrames := (len(samples) - fpFrameSize) / fpHopSize
	if numFrames < 2 {
		return []int32{}
	}

	// Pre-compute Hann window coefficients.
	hannWindow := make([]float64, fpFrameSize)
	for i := 0; i < fpFrameSize; i++ {
		hannWindow[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(fpFrameSize-1)))
	}

	fftTransformer := fourier.NewFFT(fpFrameSize)

	// Compute band energies for every frame.
	frameEnergies := make([][]float64, numFrames)
	for fi := 0; fi < numFrames; fi++ {
		start := fi * fpHopSize

		// Apply Hann window.
		windowed := make([]float64, fpFrameSize)
		for i := 0; i < fpFrameSize; i++ {
			windowed[i] = samples[start+i] * hannWindow[i]
		}

		// FFT.
		coeff := fftTransformer.Coefficients(nil, windowed)

		// Sum energy per frequency band.
		energies := make([]float64, fpNumBands)
		for band := 0; band < fpNumBands; band++ {
			loHz := bandBoundaries[band]
			hiHz := bandBoundaries[band+1]
			loBin := int(loHz * float64(fpFrameSize) / float64(fpSampleRate))
			hiBin := int(hiHz * float64(fpFrameSize) / float64(fpSampleRate))
			if loBin < 0 {
				loBin = 0
			}
			if hiBin >= len(coeff) {
				hiBin = len(coeff) - 1
			}
			var energy float64
			for k := loBin; k <= hiBin; k++ {
				mag := cmplx.Abs(coeff[k])
				energy += mag * mag
			}
			energies[band] = energy
		}
		frameEnergies[fi] = energies
	}

	// Generate fingerprint bits.
	//
	// For each consecutive frame pair and each band pair (b, b+1):
	//   diff_curr = energies[frame][b] - energies[frame][b+1]
	//   diff_prev = energies[frame-1][b] - energies[frame-1][b+1]
	//   bit = 1 if diff_curr > diff_prev, else 0
	//
	// This encodes how the spectral shape changes over time — same audio → same pattern
	// regardless of absolute volume or noise floor.
	var fingerprint []int32
	var current int32
	var bitPos uint

	for fi := 1; fi < numFrames; fi++ {
		for band := 0; band < fpNumBands-1; band++ {
			curr := frameEnergies[fi][band] - frameEnergies[fi][band+1]
			prev := frameEnergies[fi-1][band] - frameEnergies[fi-1][band+1]
			if curr > prev {
				current |= (1 << bitPos)
			}
			bitPos++
			if bitPos == 32 {
				fingerprint = append(fingerprint, current)
				current = 0
				bitPos = 0
			}
		}
	}
	if bitPos > 0 {
		fingerprint = append(fingerprint, current)
	}

	return fingerprint
}

// logspacedBands returns n logarithmically-spaced values between lo and hi.
func logspacedBands(lo, hi float64, n int) []float64 {
	bands := make([]float64, n)
	logLo := math.Log(lo)
	logHi := math.Log(hi)
	step := (logHi - logLo) / float64(n-1)
	for i := 0; i < n; i++ {
		bands[i] = math.Exp(logLo + float64(i)*step)
	}
	return bands
}

// parseWAV extracts normalized float64 PCM samples from a 16-bit mono WAV byte slice.
func (fp *AudioFingerprinter) parseWAV(data []byte) ([]float64, error) {
	if len(data) < 44 {
		return nil, fmt.Errorf("WAV data too short (%d bytes)", len(data))
	}

	// Find the "data" chunk by scanning past RIFF/fmt chunks.
	dataOffset := -1
	dataLen := 0
	for i := 12; i < len(data)-8; i++ {
		if data[i] == 'd' && data[i+1] == 'a' && data[i+2] == 't' && data[i+3] == 'a' {
			dataLen = int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
			dataOffset = i + 8
			break
		}
	}
	if dataOffset < 0 || dataLen == 0 {
		return nil, fmt.Errorf("no data chunk in WAV")
	}

	end := dataOffset + dataLen
	if end > len(data) {
		end = len(data)
	}
	pcm := data[dataOffset:end]
	numSamples := len(pcm) / 2
	if numSamples == 0 {
		return nil, fmt.Errorf("no PCM samples in WAV data chunk")
	}

	samples := make([]float64, numSamples)
	for i := 0; i < numSamples; i++ {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		samples[i] = float64(s) / 32768.0
	}
	return samples, nil
}

// SerializeFingerprint converts a fingerprint to a comma-separated string for DB storage.
func SerializeFingerprint(fp []int32) string {
	if len(fp) == 0 {
		return ""
	}
	parts := make([]string, len(fp))
	for i, v := range fp {
		parts[i] = strconv.FormatInt(int64(v), 10)
	}
	return strings.Join(parts, ",")
}

// DeserializeFingerprint parses a comma-separated DB string back into a []int32 fingerprint.
func DeserializeFingerprint(s string) []int32 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]int32, 0, len(parts))
	for _, p := range parts {
		if v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
			result = append(result, int32(v))
		}
	}
	return result
}

// CheckDuplicateByFingerprint queries recent calls in the DB and compares their stored
// fingerprints against the incoming call's fingerprint. Returns (isDuplicate, error).
//
// A wider time window than metadata detection is used (default 30s) so this catches
// delayed uploads and calls with slightly mismatched timestamps that slipped through.
func (calls *Calls) CheckDuplicateByFingerprint(
	call *Call,
	msTimeFrame uint,
	threshold float64,
	fingerprinter *AudioFingerprinter,
	db *Database,
) (bool, error) {
	if call.System == nil || call.Talkgroup == nil {
		return false, nil
	}
	if len(call.AudioFingerprint) == 0 {
		return false, nil // No fingerprint to compare
	}
	if !fingerprinter.Available() {
		return false, nil
	}

	d := time.Duration(msTimeFrame) * time.Millisecond
	from := call.Timestamp.Add(-d)
	to := call.Timestamp.Add(d)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT "audioFingerprint" FROM "calls" WHERE ("timestamp" BETWEEN %d AND %d) AND "systemId" = %d AND "talkgroupId" = %d AND "audioFingerprint" != ''`,
		from.UnixMilli(), to.UnixMilli(), call.System.Id, call.Talkgroup.Id,
	)

	rows, err := db.Sql.QueryContext(ctx, query)
	if err != nil {
		return false, fmt.Errorf("fingerprint duplicate check query failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var fpStr sql.NullString
		if err := rows.Scan(&fpStr); err != nil {
			continue
		}
		if !fpStr.Valid || fpStr.String == "" {
			continue
		}
		existing := DeserializeFingerprint(fpStr.String)
		// Skip if either fingerprint is too short to compare reliably (< 3 integers = < 96 bits).
		if len(existing) < 3 || len(call.AudioFingerprint) < 3 {
			continue
		}
		minLen := len(call.AudioFingerprint)
		if len(existing) < minLen {
			minLen = len(existing)
		}
		dist := fingerprinter.Compare(call.AudioFingerprint, existing)
		if dist <= adaptiveThreshold(threshold, minLen) {
			return true, nil
		}
	}

	return false, rows.Err()
}
