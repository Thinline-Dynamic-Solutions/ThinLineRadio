// Copyright (C) 2024 Thinline Dynamic Solutions
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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// CentralManagementService handles communication with the centralized management system
type CentralManagementService struct {
	controller *Controller
	stopChan   chan struct{}
	registered bool
}

// NewCentralManagementService creates a new central management service
func NewCentralManagementService(controller *Controller) *CentralManagementService {
	return &CentralManagementService{
		controller: controller,
		stopChan:   make(chan struct{}),
	}
}

// Start begins the central management service
func (cms *CentralManagementService) Start() {
	if !cms.controller.Options.CentralManagementEnabled {
		return
	}

	log.Println("Central Management: Service enabled, attempting registration...")

	// Attempt initial registration
	if err := cms.register(); err != nil {
		log.Printf("Central Management: Initial registration failed: %v", err)
	} else {
		cms.registered = true
		log.Println("Central Management: Successfully registered")
	}

	// Start heartbeat loop (first heartbeat fires immediately, then every minute)
	go cms.heartbeatLoop()
}

// Stop stops the central management service
func (cms *CentralManagementService) Stop() {
	close(cms.stopChan)
}

// register sends registration information to the central system
func (cms *CentralManagementService) register() error {
	if cms.controller.Options.CentralManagementURL == "" ||
		cms.controller.Options.CentralManagementAPIKey == "" {
		return fmt.Errorf("central management URL or API key not configured")
	}

	// Gather server information
	serverName := cms.controller.Options.CentralManagementServerName
	if serverName == "" {
		serverName = "TLR Server"
	}

	// Get public URL from BaseUrl option or construct from listen address
	serverURL := cms.controller.Options.BaseUrl
	if serverURL == "" {
		serverURL = "http://localhost:3000"
	}

	// Get systems information from database
	systems := []map[string]interface{}{}
	for _, system := range cms.controller.Systems.List {
		systems = append(systems, map[string]interface{}{
			"id":    system.Id,
			"label": system.Label,
			"kind":  system.Kind,
		})
	}

	// Prepare registration payload
	payload := map[string]interface{}{
		"name":    serverName,
		"url":     serverURL,
		"systems": systems,
		"version": Version,
	}

	// Add Radio Reference system ID if available (from first system)
	if len(cms.controller.Systems.List) > 0 {
		firstSystem := cms.controller.Systems.List[0]
		if firstSystem.SystemRef > 0 {
			payload["radio_reference_system_id"] = firstSystem.SystemRef
		}
	}

	// Send registration request
	return cms.sendRequest("POST", "/api/tlr/register", payload)
}

// heartbeatLoop sends periodic heartbeats to the central system
func (cms *CentralManagementService) heartbeatLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := cms.sendHeartbeat(); err != nil {
				log.Printf("Central Management: Heartbeat failed: %v", err)
				// If heartbeat fails, try to re-register
				if !cms.registered {
					if err := cms.register(); err == nil {
						cms.registered = true
						log.Println("Central Management: Re-registration successful")
					}
				}
			} else {
				cms.registered = true
			}
		case <-cms.stopChan:
			return
		}
	}
}

// sendHeartbeat sends a heartbeat to the central system
func (cms *CentralManagementService) sendHeartbeat() error {
	return cms.sendRequest("POST", "/api/tlr/heartbeat", nil)
}

// sendRequest sends an HTTP request to the central management system
func (cms *CentralManagementService) sendRequest(method, path string, payload interface{}) error {
	url := cms.controller.Options.CentralManagementURL + path

	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %w", err)
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cms.controller.Options.CentralManagementAPIKey)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// TestConnection tests the connection to the central management system with provided credentials.
// It returns the exact upstream HTTP status and response body for easier troubleshooting in the UI.
func (cms *CentralManagementService) TestConnection(centralURL, apiKey, serverName, serverURL string) (int, []byte, error) {
	baseURL := strings.TrimRight(centralURL, "/")
	testURL := fmt.Sprintf("%s/api/tlr/register", baseURL)

	// Build a lightweight payload so upstream logs clearly show this is a test request.
	payload := map[string]interface{}{
		"name":    serverName,
		"url":     serverURL,
		"systems": []interface{}{},
		"version": Version,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal test payload: %w", err)
	}

	req, err := http.NewRequest("POST", testURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create test request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to reach central management: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read central response body: %w", readErr)
	}

	return resp.StatusCode, body, nil
}
