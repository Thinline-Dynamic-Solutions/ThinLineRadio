/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 * ****************************************************************************
 */

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

const fsBrowseMaxEntries = 500

type fsBrowseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

type fsBrowseResponse struct {
	Path    string          `json:"path"`
	Parent  string          `json:"parent,omitempty"`
	BaseDir string          `json:"baseDir,omitempty"`
	OS      string          `json:"os"`
	Entries []fsBrowseEntry `json:"entries"`
	Error   string          `json:"error,omitempty"`
}

// FsBrowseHandler lists directories on the ThinLine Radio server filesystem
// (not the admin operator's local machine). GET /api/admin/fs/browse?path=
func (admin *Admin) FsBrowseHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	baseDir := ""
	if admin.Controller != nil && admin.Controller.Config != nil {
		baseDir = admin.Controller.Config.BaseDir
	}

	reqPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if strings.Contains(reqPath, "\x00") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(fsBrowseResponse{OS: runtime.GOOS, Error: "invalid path"})
		return
	}

	// Windows-style paths are meaningless on macOS/Linux — don't invent a "C:" drive.
	if runtime.GOOS != "windows" && looksLikeWindowsPath(reqPath) {
		json.NewEncoder(w).Encode(fsBrowseResponse{
			Path:    "/",
			Parent:  "",
			BaseDir: baseDir,
			OS:      runtime.GOOS,
			Entries: []fsBrowseEntry{},
			Error:   "That looks like a Windows path. This server is " + runtime.GOOS + " — browse from / or use Base dir.",
		})
		return
	}

	target, parent, errMsg := resolveFsBrowsePath(reqPath, baseDir)
	resp := fsBrowseResponse{
		Path:    target,
		Parent:  parent,
		BaseDir: baseDir,
		OS:      runtime.GOOS,
		Entries: []fsBrowseEntry{},
	}
	if errMsg != "" {
		resp.Error = errMsg
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Windows only: empty path lists drive letters.
	if runtime.GOOS == "windows" && (target == "" || target == `\` || target == `/`) {
		resp.Path = ""
		resp.Parent = ""
		resp.Entries = listWindowsDrives()
		json.NewEncoder(w).Encode(resp)
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		resp.Error = err.Error()
		json.NewEncoder(w).Encode(resp)
		return
	}
	if !info.IsDir() {
		target = filepath.Dir(target)
		resp.Path = target
		resp.Parent = fsBrowseParent(target)
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		resp.Error = err.Error()
		json.NewEncoder(w).Encode(resp)
		return
	}

	dirs := make([]fsBrowseEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(target, name)
		dirs = append(dirs, fsBrowseEntry{
			Name:  name,
			Path:  full,
			IsDir: true,
		})
		if len(dirs) >= fsBrowseMaxEntries {
			break
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	resp.Entries = dirs
	json.NewEncoder(w).Encode(resp)
}

func looksLikeWindowsPath(p string) bool {
	if p == "" {
		return false
	}
	// C: or C:\ or C:/...
	if len(p) >= 2 && unicode.IsLetter(rune(p[0])) && p[1] == ':' {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}

func resolveFsBrowsePath(reqPath, baseDir string) (target, parent, errMsg string) {
	reqPath = strings.TrimSpace(reqPath)

	// Empty path: filesystem root on Unix, drive list on Windows (handled by caller).
	// Do NOT snap back to baseDir — that made "Up" appear broken near the top.
	if reqPath == "" {
		if runtime.GOOS == "windows" {
			return "", "", ""
		}
		return "/", "", ""
	}

	cleaned := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleaned) && baseDir != "" {
		cleaned = filepath.Join(baseDir, cleaned)
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned, fsBrowseParent(cleaned), err.Error()
	}
	return abs, fsBrowseParent(abs), ""
}

func fsBrowseParent(path string) string {
	if path == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(path)
		rest := strings.TrimPrefix(path, vol)
		rest = strings.Trim(rest, `\/`)
		if rest == "" {
			return ""
		}
	} else if path == "/" {
		return ""
	}
	parent := filepath.Dir(path)
	if parent == path {
		return ""
	}
	return parent
}

func listWindowsDrives() []fsBrowseEntry {
	entries := make([]fsBrowseEntry, 0, 26)
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		if _, err := os.Stat(root); err == nil {
			entries = append(entries, fsBrowseEntry{
				Name:  string(c) + `:`,
				Path:  root,
				IsDir: true,
			})
		}
	}
	return entries
}
