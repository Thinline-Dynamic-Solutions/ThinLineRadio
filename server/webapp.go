// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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
	"embed"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:webapp
var webapp embed.FS

const defaultLiveWebappDir = "/opt/thinline-radio/webapp"

// liveWebappDir is a disk overlay for the Angular UI. When set (or when the
// default path has index.html), GET / and hashed JS/CSS are served from disk
// so a UI rsync does not require rebuilding or restarting the binary.
func liveWebappDir() string {
	if d := strings.TrimSpace(os.Getenv("TLR_WEBAPP_DIR")); d != "" {
		return d
	}
	if fi, err := os.Stat(filepath.Join(defaultLiveWebappDir, "index.html")); err == nil && !fi.IsDir() {
		return defaultLiveWebappDir
	}
	return ""
}

func readWebappFile(name string) ([]byte, error) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" || name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
		return nil, os.ErrNotExist
	}
	if root := liveWebappDir(); root != "" {
		p := filepath.Join(root, filepath.FromSlash(name))
		if b, err := os.ReadFile(p); err == nil {
			return b, nil
		}
	}
	return webapp.ReadFile(path.Join("webapp", name))
}
