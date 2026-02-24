// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Windows process creation flags.
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// triggerRestart is a no-op on Windows because the update path uses
// applyUpdateWindows (PowerShell script + os.Exit) instead of SIGTERM.
func triggerRestart() {
	log.Println("Auto-update: Windows restart is handled by the update script")
}

// applyUpdateWindows handles the Windows-specific binary swap by writing a
// small PowerShell script that:
//  1. Waits for the Go process to exit (releasing the exe lock)
//  2. Moves the new binary into place
//  3. Starts the new binary
//
// The script is launched as a fully detached process so it outlives the parent.
// The parent then calls os.Exit(0) to release the exe lock.
func applyUpdateWindows(newBinaryPath, exePath, backupPath string) error {
	scriptContent := fmt.Sprintf(`
$newBinary  = '%s'
$exePath    = '%s'
$backupPath = '%s'

# Wait for the parent process to fully exit and release the exe lock.
Start-Sleep -Seconds 3

try {
    Move-Item -Force -Path $newBinary -Destination $exePath
    Write-Host "ThinLine Radio: binary replaced successfully"
} catch {
    Write-Host "ThinLine Radio: failed to replace binary — restoring backup: $_"
    Move-Item -Force -Path $backupPath -Destination $exePath
    exit 1
}

# Start the new server process.
Start-Process -FilePath $exePath -WorkingDirectory (Split-Path $exePath)
Write-Host "ThinLine Radio: new server process started"

# Clean up this script.
Remove-Item -Path $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue
`, newBinaryPath, exePath, backupPath)

	scriptPath := filepath.Join(filepath.Dir(exePath), "thinline-update.ps1")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("failed to write update script: %w", err)
	}

	cmd := exec.Command("powershell",
		"-ExecutionPolicy", "Bypass",
		"-NonInteractive",
		"-File", scriptPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
	}

	if err := cmd.Start(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("failed to launch update script: %w", err)
	}

	log.Println("Auto-update: PowerShell update script launched, shutting down for binary swap...")
	os.Exit(0)
	return nil // unreachable — satisfies compiler
}
