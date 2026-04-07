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
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// CallAudioDownloadHandler serves raw audio bytes for a call.
//
// GET /api/calls/{callId}/audio?pin=<user_pin>
//
// Authentication: the same user PIN the mobile app already stores when a
// user adds a scanner (validated via getClient, which checks against the
// bcrypt-hashed PIN in the users table).
func (api *Api) CallAudioDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Extract call ID from URL: /api/calls/{id}/audio
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "calls" || parts[3] != "audio" {
		api.exitWithError(w, http.StatusBadRequest, "Invalid path — expected /api/calls/{id}/audio")
		return
	}

	callId, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid call ID")
		return
	}

	// Authenticate using the same mechanism as every other user-facing endpoint:
	// PIN from ?pin= or Authorization: Bearer <pin>, matched against the users table.
	client := api.getClient(r)
	if client == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="TLR pager audio"`)
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	call, err := api.Controller.Calls.GetCall(callId)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to retrieve call")
		return
	}
	if call == nil || len(call.Audio) == 0 {
		api.exitWithError(w, http.StatusNotFound, "Call audio not found")
		return
	}

	mimeType := call.AudioMime
	if mimeType == "" {
		mimeType = "audio/aac"
	}

	filename := call.AudioFilename
	if filename == "" {
		filename = fmt.Sprintf("call_%d.m4a", callId)
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(call.Audio)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(call.Audio) //nolint:errcheck
}
