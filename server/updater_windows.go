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
	createNoWindow        = 0x08000000 // hide console window but keep session intact
)

// triggerRestart is a no-op on Windows because the update path uses
// applyUpdateWindows (batch script + os.Exit) instead of SIGTERM.
func triggerRestart() {
	log.Println("Auto-update: Windows restart is handled by the update script")
}

// spawnNewProcess is a no-op stub on Windows.
// Windows restarts are handled entirely by the detached batch script.
func spawnNewProcess(_ string) error {
	return nil
}

// applyUpdateWindows handles the Windows-specific binary swap using a plain
// cmd.exe batch script (.cmd) instead of PowerShell.  Batch scripts are NOT
// subject to PowerShell execution policies, so they run regardless of whether
// the machine has script execution disabled via Group Policy.
//
// The script performs ALL file operations AFTER the Go process has exited and
// released its lock on the exe:
//  1. Waits for the Go process to exit (releasing the exe lock)
//  2. Renames the current exe to .bak (safe rollback copy)
//  3. Moves the new binary into place
//  4. Starts the new binary
//  5. Logs every step to thinline-update.log in the install directory
//  6. Self-deletes
//
// Keeping ALL file operations inside the script means the old exe is never
// touched by the Go process — if the script fails to start for any reason the
// server continues running on the existing binary without any file corruption.
func applyUpdateWindows(newBinaryPath, exePath string) error {
	installDir := filepath.Dir(exePath)
	backupPath := exePath + ".bak"
	logPath := filepath.Join(installDir, "thinline-update.log")
	scriptPath := filepath.Join(installDir, "thinline-update.cmd")

	// Build a Windows batch script.  Batch files have no execution policy
	// restrictions — they run via cmd.exe regardless of PowerShell settings.
	scriptContent := fmt.Sprintf(`@echo off
setlocal

set "NEW_BINARY=%s"
set "EXE_PATH=%s"
set "BACKUP_PATH=%s"
set "LOG_FILE=%s"

call :log "=== ThinLine Radio auto-update starting ==="
call :log "New binary : %%NEW_BINARY%%"
call :log "Target exe : %%EXE_PATH%%"
call :log "Backup path: %%BACKUP_PATH%%"

call :log "Waiting 4 seconds for parent process to exit..."
ping 127.0.0.1 -n 5 >nul

if not exist "%%NEW_BINARY%%" (
    call :log "ERROR: new binary not found at '%%NEW_BINARY%%' -- aborting, original exe untouched."
    exit /b 1
)

call :log "Backing up current exe..."
move /y "%%EXE_PATH%%" "%%BACKUP_PATH%%"
if errorlevel 1 (
    call :log "ERROR: failed to back up current exe -- aborting."
    exit /b 1
)
call :log "Backup created: %%BACKUP_PATH%%"

call :log "Moving new binary into place..."
move /y "%%NEW_BINARY%%" "%%EXE_PATH%%"
if errorlevel 1 (
    call :log "ERROR: failed to move new binary -- restoring backup..."
    move /y "%%BACKUP_PATH%%" "%%EXE_PATH%%"
    if errorlevel 1 (
        call :log "CRITICAL: backup restore also failed."
    ) else (
        call :log "Backup restored successfully."
    )
    exit /b 1
)
call :log "New binary in place: %%EXE_PATH%%"

call :log "Starting new server process..."
start "" "%%EXE_PATH%%"
if errorlevel 1 (
    call :log "ERROR: failed to start new server -- exe is in place but server not running."
    call :log "Please start the server manually from: %%EXE_PATH%%"
    exit /b 1
)
call :log "New server process started."

call :log "=== Update complete ==="
del "%%~f0"
exit /b 0

:log
echo [%%DATE%% %%TIME%%] %%~1
echo [%%DATE%% %%TIME%%] %%~1 >> "%%LOG_FILE%%"
exit /b 0
`, newBinaryPath, exePath, backupPath, logPath)

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("failed to write update script: %w", err)
	}

	// Launch via cmd.exe — no execution policy, runs on any Windows machine.
	cmd := exec.Command("cmd", "/c", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
	}

	if err := cmd.Start(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("failed to launch update script: %w", err)
	}

	log.Printf("Auto-update: batch update script launched (log: %s), shutting down for binary swap...", logPath)
	os.Exit(0)
	return nil // unreachable — satisfies compiler
}
