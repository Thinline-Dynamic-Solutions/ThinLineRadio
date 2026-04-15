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
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type ToneImportFormat string

const (
	ToneImportFormatTwoTone ToneImportFormat = "twotone"
	ToneImportFormatCSV     ToneImportFormat = "csv"
)

type ToneImportRequest struct {
	Format  string `json:"format"`
	Content string `json:"content"`
}

type ToneImportResponse struct {
	Format   string    `json:"format"`
	Count    int       `json:"count"`
	ToneSets []ToneSet `json:"toneSets"`
	Warnings []string  `json:"warnings,omitempty"`
}

type toneImportResult struct {
	toneSets []ToneSet
	warnings []string
}

func ParseToneImport(format string, content string) (*toneImportResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("no content provided")
	}

	switch ToneImportFormat(strings.ToLower(strings.TrimSpace(format))) {
	case ToneImportFormatTwoTone:
		return parseTwoToneDetectConfig(content)
	case ToneImportFormatCSV:
		return parseToneCSV(content)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

func parseTwoToneDetectConfig(content string) (*toneImportResult, error) {
	result := &toneImportResult{
		toneSets: []ToneSet{},
		warnings: []string{},
	}

	type section struct {
		name string
		data map[string]string
	}
	var sections []section

	scanner := bufio.NewScanner(strings.NewReader(content))
	current := section{
		name: "",
		data: map[string]string{},
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current.name != "" {
				sections = append(sections, current)
			}
			current = section{
				name: strings.Trim(line, "[]"),
				data: map[string]string{},
			}
			continue
		}

		split := strings.SplitN(line, "=", 2)
		if len(split) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(split[0]))
		value := strings.TrimSpace(split[1])
		current.data[key] = value
	}

	if current.name != "" {
		sections = append(sections, current)
	}

	for _, sec := range sections {
		if toneSet, warning := toneSetFromTwoToneSection(sec); toneSet != nil {
			result.toneSets = append(result.toneSets, *toneSet)
			if warning != "" {
				result.warnings = append(result.warnings, warning)
			}
		} else if warning != "" {
			result.warnings = append(result.warnings, warning)
		}
	}

	return result, nil
}

func toneSetFromTwoToneSection(sec struct {
	name string
	data map[string]string
}) (*ToneSet, string) {
	getString := func(keys ...string) string {
		for _, key := range keys {
			if val, ok := sec.data[strings.ToLower(key)]; ok && strings.TrimSpace(val) != "" {
				return strings.TrimSpace(val)
			}
		}
		return ""
	}

	getFloat := func(key string) (float64, bool) {
		value, ok := sec.data[strings.ToLower(key)]
		if !ok {
			return 0, false
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}

	label := getString("description", "__name__", "name")
	if label == "" {
		label = sec.name
	}

	aFreq, hasA := getFloat("atone")
	bFreq, hasB := getFloat("btone")
	longFreq, hasLong := getFloat("longtone")

	if !hasA && !hasB && !hasLong {
		return nil, fmt.Sprintf("section %s has no tone definitions", sec.name)
	}

	toneSet := &ToneSet{
		Id:    uuid.NewString(),
		Label: label,
	}

	if hasA {
		min := getDurationFallback(sec.data, "atonelength")
		toneSet.ATone = &ToneSpec{
			Frequency:   aFreq,
			MinDuration: min,
		}
	}

	if hasB {
		min := getDurationFallback(sec.data, "btonelength")
		toneSet.BTone = &ToneSpec{
			Frequency:   bFreq,
			MinDuration: min,
		}
	}

	if hasLong {
		min := getDurationFallback(sec.data, "longtonelength", "longtone_length")
		if min == 0 {
			min = getDurationFallback(sec.data, "tone_length")
		}
		if min == 0 {
			min = 5.0
		}
		toneSet.LongTone = &ToneSpec{
			Frequency:   longFreq,
			MinDuration: min,
		}
	}

	tolerance, hasTolerance := getFloat("tone_tolerance")
	if hasTolerance {
		toneSet.Tolerance = tolerance
	} else {
		toneSet.Tolerance = 10
	}

	// Determine overall minimum duration if available
	toneSet.MinDuration = minDurationFromToneSpecs(toneSet)

	var warning string
	if label == "" {
		warning = fmt.Sprintf("section %s is missing a description; generated label %s", sec.name, toneSet.Id)
	}

	return toneSet, warning
}

func parseToneCSV(content string) (*toneImportResult, error) {
	result := &toneImportResult{
		toneSets: []ToneSet{},
		warnings: []string{},
	}

	content = strings.TrimLeft(content, "\ufeff")
	reader := csv.NewReader(strings.NewReader(content))
	reader.TrimLeadingSpace = true

	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read csv header: %w", err)
	}

	headerIndex := map[string]int{}
	for idx, header := range headers {
		normalized := normalizeHeader(header)
		if normalized != "" {
			headerIndex[normalized] = idx
		}
	}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read csv: %w", err)
		}

		if toneSet, warning := toneSetFromCSVRecord(record, headerIndex); toneSet != nil {
			result.toneSets = append(result.toneSets, *toneSet)
			if warning != "" {
				result.warnings = append(result.warnings, warning)
			}
		} else if warning != "" {
			result.warnings = append(result.warnings, warning)
		}
	}

	return result, nil
}

func toneSetFromCSVRecord(record []string, headerIndex map[string]int) (*ToneSet, string) {
	get := func(keys ...string) string {
		for _, key := range keys {
			if idx, ok := headerIndex[key]; ok {
				if idx >= 0 && idx < len(record) {
					val := strings.TrimSpace(record[idx])
					if val != "" {
						return val
					}
				}
			}
		}
		return ""
	}

	getFloat := func(keys ...string) (float64, bool) {
		value := get(keys...)
		if value == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}

	label := get("description", "label", "name")
	if label == "" {
		return nil, "csv row missing description/label"
	}

	aFreq, hasA := getFloat("atone", "a", "afreq", "a_frequency")
	bFreq, hasB := getFloat("btone", "b", "bfreq", "b_frequency")
	longFreq, hasLong := getFloat("longtone", "long", "longfreq", "long_frequency")

	if !hasA && !hasB && !hasLong {
		return nil, fmt.Sprintf("csv row %s missing tone frequencies", label)
	}

	toneSet := &ToneSet{
		Id:    uuid.NewString(),
		Label: label,
	}

	if hasA {
		min := fallbackDuration(getFloat, 0.6, "atonelength", "a_length", "a_duration")
		toneSet.ATone = &ToneSpec{
			Frequency:   aFreq,
			MinDuration: min,
		}
		if max, ok := getFloat("atonemaxduration", "amaxduration", "atonemax", "a_max_duration", "amax"); ok && max > 0 {
			toneSet.ATone.MaxDuration = max
		}
	}

	if hasB {
		min := fallbackDuration(getFloat, 0.6, "btonelength", "b_length", "b_duration")
		toneSet.BTone = &ToneSpec{
			Frequency:   bFreq,
			MinDuration: min,
		}
		if max, ok := getFloat("btonemaxduration", "bmaxduration", "btonemax", "b_max_duration", "bmax"); ok && max > 0 {
			toneSet.BTone.MaxDuration = max
		}
	}

	if hasLong {
		min := fallbackDuration(getFloat, 5.0, "longtonelength", "long_length", "long_duration")
		toneSet.LongTone = &ToneSpec{
			Frequency:   longFreq,
			MinDuration: min,
		}
		if max, ok := getFloat("longtonemaxduration", "longmaxduration", "longtonemax", "long_max_duration", "longmax"); ok && max > 0 {
			toneSet.LongTone.MaxDuration = max
		}
	}

	tolerance, hasTolerance := getFloat("tone_tolerance", "tolerance")
	if hasTolerance {
		toneSet.Tolerance = tolerance
	} else {
		toneSet.Tolerance = 10
	}

	if seqMin, ok := getFloat("sequenceminduration", "sequencesminduration", "tonepatternminduration", "tonesetminduration"); ok && seqMin > 0 {
		toneSet.MinDuration = seqMin
	} else {
		toneSet.MinDuration = minDurationFromToneSpecs(toneSet)
	}

	return toneSet, ""
}

func getDurationFallback(data map[string]string, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := data[strings.ToLower(key)]; ok {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				return f
			}
		}
	}
	return 0
}

func fallbackDuration(getFloat func(keys ...string) (float64, bool), fallback float64, keys ...string) float64 {
	if val, ok := getFloat(keys...); ok {
		return val
	}
	return fallback
}

func minDurationFromToneSpecs(toneSet *ToneSet) float64 {
	minDuration := math.MaxFloat64
	hasDuration := false

	if toneSet.ATone != nil && toneSet.ATone.MinDuration > 0 {
		minDuration = math.Min(minDuration, toneSet.ATone.MinDuration)
		hasDuration = true
	}
	if toneSet.BTone != nil && toneSet.BTone.MinDuration > 0 {
		minDuration = math.Min(minDuration, toneSet.BTone.MinDuration)
		hasDuration = true
	}
	if toneSet.LongTone != nil && toneSet.LongTone.MinDuration > 0 {
		minDuration = math.Min(minDuration, toneSet.LongTone.MinDuration)
		hasDuration = true
	}

	if !hasDuration || minDuration == math.MaxFloat64 {
		return 0
	}

	return minDuration
}

func normalizeHeader(header string) string {
	header = strings.ToLower(strings.TrimSpace(header))
	header = strings.ReplaceAll(header, "-", "")
	header = strings.ReplaceAll(header, "_", "")
	header = strings.ReplaceAll(header, " ", "")
	return header
}
