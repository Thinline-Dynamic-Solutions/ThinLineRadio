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
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	githubOwner         = "Thinline-Dynamic-Solutions"
	githubRepo          = "ThinLineRadio"
	githubAPIURL        = "https://api.github.com/repos/Thinline-Dynamic-Solutions/ThinLineRadio/releases/latest"
	updateCheckInterval = 12 * time.Hour
	updateCheckDelay    = 5 * time.Minute // Wait after startup before first check
)

// GitHubRelease represents the GitHub releases API response.
type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a single downloadable asset in a release.
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// UpdateInfo is returned to callers (and the admin API) describing update status.
type UpdateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	DownloadURL     string `json:"download_url,omitempty"`
	Platform        string `json:"platform"`
}

// Updater handles checking for and applying updates from GitHub Releases.
type Updater struct {
	controller *Controller
	stopChan   chan struct{}
}

// NewUpdater creates a new Updater bound to the given controller.
func NewUpdater(controller *Controller) *Updater {
	return &Updater{
		controller: controller,
		stopChan:   make(chan struct{}),
	}
}

// Start launches the background update-check goroutine if auto_update is enabled.
// The admin API endpoints work regardless of this setting.
func (u *Updater) Start() {
	if !u.controller.Config.AutoUpdate {
		log.Println("Auto-update: disabled (set auto_update = true in thinline-radio.ini to enable)")
		return
	}

	if runtime.GOOS == "windows" {
		log.Println("Auto-update: enabled (Windows — update will use PowerShell script for binary swap)")
	} else {
		log.Printf("Auto-update: enabled (checking every %s, first check in %s)", updateCheckInterval, updateCheckDelay)
	}

	go u.checkLoop()
}

// Stop signals the background goroutine to exit.
func (u *Updater) Stop() {
	select {
	case <-u.stopChan:
		// already closed
	default:
		close(u.stopChan)
	}
}

// checkLoop runs the periodic update check in the background.
func (u *Updater) checkLoop() {
	// Wait a few minutes after startup before the first check so we don't
	// slow down startup or hammer GitHub on every service restart.
	delayTimer := time.NewTimer(updateCheckDelay)
	defer delayTimer.Stop()

	ticker := time.NewTicker(updateCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-delayTimer.C:
			u.checkAndApply()
		case <-ticker.C:
			u.checkAndApply()
		case <-u.stopChan:
			return
		}
	}
}

// checkAndApply checks for an update and applies it automatically.
func (u *Updater) checkAndApply() {
	info, err := u.CheckForUpdate()
	if err != nil {
		log.Printf("Auto-update check failed: %v", err)
		return
	}

	if !info.UpdateAvailable {
		log.Printf("Auto-update: server is up to date (%s)", info.CurrentVersion)
		return
	}

	log.Printf("Auto-update: new version available %s → %s", info.CurrentVersion, info.LatestVersion)
	log.Println("Auto-update: downloading and applying update...")

	if err := u.ApplyUpdate(info.DownloadURL); err != nil {
		log.Printf("Auto-update: failed to apply update: %v", err)
	}
}

// CheckForUpdate queries the GitHub Releases API and returns update status.
// This is also called directly from the admin API handler.
func (u *Updater) CheckForUpdate() (*UpdateInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", githubAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("ThinLineRadio/%s", Version))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned HTTP %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode github response: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	updateAvailable := latestVersion != Version && isNewerVersion(latestVersion, Version)

	info := &UpdateInfo{
		CurrentVersion:  Version,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		Platform:        fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	if updateAvailable {
		assetName := buildAssetName(latestVersion)
		for _, asset := range release.Assets {
			if asset.Name == assetName {
				info.DownloadURL = asset.BrowserDownloadURL
				break
			}
		}
		if info.DownloadURL == "" {
			return info, fmt.Errorf("update available (%s) but no matching asset found for platform %s/%s (looked for: %s)",
				latestVersion, runtime.GOOS, runtime.GOARCH, assetName)
		}
	}

	return info, nil
}

// ApplyUpdate downloads the release at downloadURL, extracts the binary,
// swaps it in place, and triggers a graceful restart.
func (u *Updater) ApplyUpdate(downloadURL string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}
	// Resolve symlinks so we get the real file path.
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks on executable: %w", err)
	}

	// Create a temp directory for the download.
	tmpDir, err := os.MkdirTemp("", "thinline-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Download the release archive.
	archivePath := filepath.Join(tmpDir, "update.archive")
	log.Printf("Auto-update: downloading %s", downloadURL)
	if err := downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	log.Println("Auto-update: download complete, extracting binary...")

	// Extract the binary from the archive.
	newBinaryPath := filepath.Join(tmpDir, "thinline-radio-new")
	binaryName := "thinline-radio"
	if runtime.GOOS == "windows" {
		binaryName = "thinline-radio.exe"
		if err := extractFromZip(archivePath, binaryName, newBinaryPath); err != nil {
			return fmt.Errorf("zip extraction failed: %w", err)
		}
	} else {
		if err := extractFromTarGz(archivePath, binaryName, newBinaryPath); err != nil {
			return fmt.Errorf("tar.gz extraction failed: %w", err)
		}
	}

	// Make the new binary executable (no-op on Windows, harmless).
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("failed to chmod new binary: %w", err)
	}

	// Backup the current binary so we can restore it on failure.
	backupPath := exePath + ".bak"
	if err := os.Rename(exePath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}
	log.Printf("Auto-update: backed up current binary to %s", backupPath)

	// Platform-specific swap and restart.
	if runtime.GOOS == "windows" {
		return applyUpdateWindows(newBinaryPath, exePath, backupPath)
	}

	// Unix: atomic rename then signal ourselves to restart gracefully.
	if err := os.Rename(newBinaryPath, exePath); err != nil {
		// Restore backup so the server stays functional.
		if restoreErr := os.Rename(backupPath, exePath); restoreErr != nil {
			log.Printf("Auto-update: CRITICAL — failed to restore backup: %v", restoreErr)
		}
		return fmt.Errorf("failed to replace binary (backup restored): %w", err)
	}

	log.Printf("Auto-update: binary replaced successfully (%s → %s)", Version, exePath)
	u.controller.Logs.LogEvent(LogLevelInfo, "Auto-update applied — restarting server")

	// Give the log a moment to flush, then signal graceful shutdown.
	// systemd / the daemon manager will restart us with the new binary.
	time.AfterFunc(1*time.Second, triggerRestart)
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// buildAssetName constructs the expected GitHub release asset filename for
// the current platform, matching the naming convention used by the build scripts.
//
//	thinline-radio-{GOOS}-{GOARCH}-v{VERSION}.tar.gz   (Unix)
//	thinline-radio-{GOOS}-{GOARCH}-v{VERSION}.zip      (Windows)
func buildAssetName(version string) string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("thinline-radio-%s-%s-v%s.%s", runtime.GOOS, runtime.GOARCH, version, ext)
}

// downloadFile streams a URL to a local file.
func downloadFile(url, destPath string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// extractFromTarGz finds binaryName inside a .tar.gz and writes it to destPath.
func extractFromTarGz(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Match either "thinline-radio" or any path ending in "/thinline-radio".
		base := filepath.Base(header.Name)
		if base == binaryName {
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, tr)
			return err
		}
	}
	return fmt.Errorf("binary %q not found in archive", binaryName)
}

// extractFromZip finds binaryName inside a .zip and writes it to destPath.
func extractFromZip(archivePath, binaryName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) == binaryName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("binary %q not found in zip", binaryName)
}

// isNewerVersion returns true if candidate is strictly newer than current.
// Handles standard semver and pre-release suffixes (e.g. "7.0.0-beta9.6.1").
// A stable release (no pre-release) is considered newer than a beta with the
// same core version numbers.
func isNewerVersion(candidate, current string) bool {
	candidate = strings.TrimPrefix(candidate, "v")
	current = strings.TrimPrefix(current, "v")

	// Split into core and pre-release parts.
	cParts := strings.SplitN(candidate, "-", 2)
	rParts := strings.SplitN(current, "-", 2)

	cCore := strings.Split(cParts[0], ".")
	rCore := strings.Split(rParts[0], ".")

	// Pad to at least 3 segments.
	for len(cCore) < 3 {
		cCore = append(cCore, "0")
	}
	for len(rCore) < 3 {
		rCore = append(rCore, "0")
	}

	// Compare major.minor.patch numerically.
	for i := 0; i < 3; i++ {
		c := parseVersionInt(cCore[i])
		r := parseVersionInt(rCore[i])
		if c > r {
			return true
		}
		if c < r {
			return false
		}
	}

	// Core versions are equal — compare pre-release.
	// No pre-release (stable) > has pre-release (beta/rc).
	candidateIsStable := len(cParts) == 1
	currentIsStable := len(rParts) == 1

	if candidateIsStable && !currentIsStable {
		return true // stable beats beta with same core
	}
	if !candidateIsStable && currentIsStable {
		return false // beta doesn't beat stable with same core
	}

	// Both pre-release or both stable — simple string compare.
	if !candidateIsStable && !currentIsStable {
		return cParts[1] > rParts[1]
	}

	return false // identical
}

func parseVersionInt(s string) int {
	// Strip any non-numeric suffix (e.g. "1rc1" → 1).
	n, _ := strconv.Atoi(strings.TrimRight(s, "abcdefghijklmnopqrstuvwxyz"))
	return n
}
