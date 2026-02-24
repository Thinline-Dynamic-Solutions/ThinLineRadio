// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
)

// triggerRestart sends SIGTERM to the current process so the graceful shutdown
// path in main() runs. If running under systemd it will be restarted
// automatically; if not, spawnNewProcess should have already launched the new
// binary before this is called.
func triggerRestart() {
	log.Println("Auto-update: sending SIGTERM for graceful restart...")
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		log.Printf("Auto-update: failed to send SIGTERM: %v", err)
	}
}

// spawnNewProcess launches the binary at exePath in a new session (Setsid)
// so it is fully detached from the current process and its controlling
// terminal.  This ensures the server restarts even when it is NOT managed by
// systemd (e.g. run directly in a terminal or via a startup script).
//
// A 5-second shell sleep is used before exec-ing the new binary.  Without it
// the new process would race to bind the port while the current process is
// still in its graceful shutdown, fail immediately, and exit — leaving the
// server down.  The sleep lets the current process finish shutting down and
// release the port before the new binary tries to bind it.
//
// When systemd IS managing the process it will also restart it after SIGTERM;
// whichever instance loses the port race exits immediately — no double-server.
func spawnNewProcess(exePath string) error {
	// "sleep 5 && exec <path>" — the shell waits 5 s then replaces itself with
	// the new binary (exec-in-place, no extra process left behind).
	script := fmt.Sprintf("sleep 5 && exec '%s'", exePath)
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // new session — detached from parent's terminal
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// applyUpdateWindows is a no-op stub on non-Windows platforms.
// It is never called on Unix; it exists only to satisfy the shared call site
// in updater.go without requiring build tags there.
func applyUpdateWindows(newBinaryPath, exePath string) error {
	return nil
}
