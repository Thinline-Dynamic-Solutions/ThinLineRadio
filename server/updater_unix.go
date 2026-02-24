// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build !windows

package main

import (
	"log"
	"os"
	"syscall"
)

// triggerRestart sends SIGTERM to the current process so the graceful shutdown
// path in main() runs and systemd (or whatever process manager) restarts with
// the newly placed binary.
func triggerRestart() {
	log.Println("Auto-update: sending SIGTERM for graceful restart...")
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		log.Printf("Auto-update: failed to send SIGTERM: %v", err)
	}
}

// applyUpdateWindows is a no-op stub on non-Windows platforms.
// It is never called on Unix; it exists only to satisfy the shared call site
// in updater.go without requiring build tags there.
func applyUpdateWindows(newBinaryPath, exePath, backupPath string) error {
	return nil
}
