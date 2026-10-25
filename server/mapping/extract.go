// Copyright (C) 2025 Thinline Dynamic Solutions
//
// extract.go — station-coverage markers and OpenAI HTTP helpers (call-nature).

package mapping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// StationCoverageMarkers are phrasings dispatchers use for pure off-duty tones.
var StationCoverageMarkers = []string{
	"STATION COVERAGE",
	"STATION TRANSFER",
	"TRANSFER STATION",
	"OFF DUTY",
	"OFF-DUTY",
	"OFF TV STATION",
	"OFF DC STATION",
}

// RealIncidentMarkers identify phrasings that prove a real incident was toned out.
var RealIncidentMarkers = []string{
	"SQUAD CALL", "SQUAW CALL", "SWAB CALL", "SQUARE CALL",
	"SQUAD PERSONNEL", "RESCUE RESPOND", "RESCUE NEEDED", "RESCUES NEEDED",
	"RESCUE REQUEST", "SQUAD IS NEEDED", "SQUAD REQUESTED", "SQUADS REQUESTED",
	"ALPHA RESPONSE", "BRAVO RESPONSE", "CHARLIE RESPONSE", "DELTA RESPONSE",
	"ECHO RESPONSE",
	"FIRE ALARM", "ALARM DROP", "ALARM DRAW",
	"STRUCTURE FIRE", "VEHICLE FIRE", "BRUSH FIRE", "DEBRIS PILE",
	"GAS LEAK", "GAS ODOR", "ODOR INVESTIGATION", "BURNING COMPLAINT",
	"WIRE DOWN", "WIRES DOWN", "TREE DOWN", "TREES DOWN", "TREEDOWN",
	"TORE DOWN A WIRE", "TORE DOWN THE WIRE",
	"POWER LINES ARE ON", "POWER LINES ON THE GROUND", "LINES ON THE GROUND",
	"CRASH WITH", " MVA ", "MOTOR VEHICLE", " AUTO ACCIDENT",
	"VERBAL ALTERCATION", " ALTERCATION ", " DOMESTIC ", " ASSAULT ",
	// Gun / shooting broadcasts — must not be wiped by location-narrative guards
	// when the address is suffixless ("4908 CENTRAL … DRIVE-BY SHOOTING … PARKING LOT").
	" DRIVE-BY ", " DRIVE BY ", " SHOOTING ", " SHOTS FIRED ", " SHOT SPOTTER ",
	" SHOTSPOTTER ", " SOMEONE SHOT ", " SOMEONE IS SHOT ",
	"CARDIAC", "UNCONSCIOUS", "UNRESPONSIVE", "SEIZURE", "STROKE", "DIABETIC", "OVERDOSE",
	"CHEST PAIN", "ABDOMINAL", "SHORT OF BREATH", "SHORTNESS OF BREATH",
	" FELL", "FAINTED", "PASSED OUT",
	"LACERATION", " LACERATION", "BLEEDING", " HEMORRHAGE",
	"LIFT ASSIST", "MUTUAL AID", "MUTUALLY",
	"BACKWARDS", "STILL DOWN",
	"2ND SQUAD", "SECOND SQUAD", "2ND CALL", "SECOND CALL",
	"LIGHTING OFF FIREWORKS", "SHOOTING OFF FIREWORKS", "SETTING OFF FIREWORKS",
	"FIREWORKS COMPLAINT", "FIREWORKS", "FIREWORK",
	"ILL PERSON", "ILPERSON", " MEDIC RUN", "CHEST PAIN", "CHEST PAINS",
	"ABDOMINAL PAIN", "DIFFICULTY BREATHING", "BREATHING PROBLEMS",
	" UNCONSCIOUS ", " STROKE ", " SEIZURE ", " OVERDOSE ",
	" TRESPASS",
}

// suicideIncidentMarkers prove a suicide/self-harm incident without becoming the
// map nature label (see inferSuicideNature).
var suicideIncidentMarkers = []string{
	"ATTEMPTED SUICIDE", "ATTEMPTED TO KILL", " INTENDS TO STRANGLE", " INTENDS TO KILL",
	" SUICIDAL", " SUICIDE ATTEMPT",
}

// TranscriptIsPureStationCoverage reports whether the transcript is a pure
// off-duty / station-coverage tone with no embedded real incident.
func TranscriptIsPureStationCoverage(transcript string) bool {
	upper := " " + strings.ToUpper(transcript) + " "
	hasCoverage := false
	for _, m := range StationCoverageMarkers {
		if strings.Contains(upper, m) {
			hasCoverage = true
			break
		}
	}
	if !hasCoverage {
		return false
	}
	// Off-duty openers often accompany a restated real call ("OFF-DUTY. THE CO
	// … 7584 ELMLAND AVENUE"). A clean house-number street means this is not
	// pure station coverage.
	if transcriptHasCleanNumberedDispatchAddress(transcript) {
		return false
	}
	for _, m := range RealIncidentMarkers {
		if strings.Contains(upper, m) {
			return false
		}
	}
	return true
}

// RawFallback builds a minimal CuratedAlert when OpenAI is unavailable.
func RawFallback(transcript, toneSetLabel string) *CuratedAlert {
	return &CuratedAlert{
		UnitLocation: strings.ToUpper(toneSetLabel),
		Notes:        strings.ToUpper(transcript),
	}
}

// DefaultOpenAIModel is the model used when no override is supplied.
var DefaultOpenAIModel = "gpt-5.4-mini"

// OpenAIChatCompletionsURL is the OpenAI endpoint for extractions.
var OpenAIChatCompletionsURL = "https://api.openai.com/v1/chat/completions"

// OpenAIResponse is the decoded OpenAI chat-completions payload.
type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

const openaiPerAttemptTimeout = 5 * time.Second
const openaiMaxAttempts = 3
const openaiMaxResponseBytes = 1 << 20

// PostOpenAIWithRetry performs the POST to OpenAI with per-attempt timeout and retries.
func PostOpenAIWithRetry(url, apiKey string, bodyBytes []byte) (OpenAIResponse, error) {
	return PostOpenAIWithRetryTimeout(url, apiKey, bodyBytes, openaiPerAttemptTimeout)
}

// PostOpenAIWithRetryTimeout is PostOpenAIWithRetry with a custom per-attempt timeout.
func PostOpenAIWithRetryTimeout(url, apiKey string, bodyBytes []byte, perAttempt time.Duration) (OpenAIResponse, error) {
	if perAttempt <= 0 {
		perAttempt = openaiPerAttemptTimeout
	}
	var (
		oaiResp OpenAIResponse
		lastErr error
	)
	for attempt := 1; attempt <= openaiMaxAttempts; attempt++ {
		req, rerr := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
		if rerr != nil {
			return OpenAIResponse{}, rerr
		}
		req.Header.Set("Content-Type", "application/json")
		if strings.TrimSpace(apiKey) != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := &http.Client{Timeout: perAttempt}
		resp, derr := client.Do(req)
		if derr != nil {
			lastErr = fmt.Errorf("openai request (attempt %d/%d): %w", attempt, openaiMaxAttempts, derr)
			if !isOpenAIRetryableError(derr) {
				return OpenAIResponse{}, lastErr
			}
			log.Printf("[WARN] openai: %v — retrying", lastErr)
			continue
		}
		bodyData, rerr := readAndCloseLimited(resp.Body, openaiMaxResponseBytes)
		if rerr != nil {
			lastErr = fmt.Errorf("openai read body (attempt %d/%d): %w", attempt, openaiMaxAttempts, rerr)
			if !isOpenAIRetryableError(rerr) {
				return OpenAIResponse{}, lastErr
			}
			log.Printf("[WARN] openai: %v — retrying", lastErr)
			continue
		}
		oaiResp = OpenAIResponse{}
		if jerr := json.Unmarshal(bodyData, &oaiResp); jerr != nil {
			return OpenAIResponse{}, fmt.Errorf("decode response (http %d): %w", resp.StatusCode, jerr)
		}
		if resp.StatusCode >= 400 {
			msg := strings.TrimSpace(string(bodyData))
			if oaiResp.Error != nil && strings.TrimSpace(oaiResp.Error.Message) != "" {
				msg = oaiResp.Error.Message
			}
			lastErr = fmt.Errorf("openai http %d: %s", resp.StatusCode, msg)
			if !isOpenAIRetryableHTTPStatus(resp.StatusCode) {
				return OpenAIResponse{}, lastErr
			}
			log.Printf("[WARN] openai: %v — retrying", lastErr)
			continue
		}
		if attempt > 1 {
			log.Printf("[INFO] openai: request succeeded on attempt %d/%d", attempt, openaiMaxAttempts)
		}
		return oaiResp, nil
	}
	if lastErr != nil {
		log.Printf("[WARN] openai: all %d attempts failed: %v", openaiMaxAttempts, lastErr)
	}
	return OpenAIResponse{}, lastErr
}

func isOpenAIRetryableHTTPStatus(code int) bool {
	// Client errors (400/404) won't succeed on retry; 429/502/503 might.
	switch code {
	case 400, 401, 403, 404, 413, 422:
		return false
	default:
		return code >= 500 || code == 429
	}
}

func isOpenAIRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "Client.Timeout exceeded") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "i/o timeout")
}

func readAndCloseLimited(rc io.ReadCloser, max int64) ([]byte, error) {
	if rc == nil {
		return nil, nil
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}
