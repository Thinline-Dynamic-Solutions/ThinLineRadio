// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Captures standard-library log output into the admin logs table.

package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	logStdoutOrig io.Writer
	logCaptureMu  sync.Mutex
)

// InstallLogCapture redirects log package output through the logs store while
// still writing to the original destination. LogEvent uses writeLogStdout directly
// to avoid duplicate DB inserts.
func (logs *Logs) InstallLogCapture() {
	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	if logStdoutOrig != nil {
		return
	}

	logStdoutOrig = log.Writer()
	if logStdoutOrig == nil {
		logStdoutOrig = os.Stderr
	}

	log.SetOutput(io.MultiWriter(logStdoutOrig, &logCaptureWriter{logs: logs}))
}

func writeLogStdout(message string) {
	logCaptureMu.Lock()
	orig := logStdoutOrig
	logCaptureMu.Unlock()

	if orig != nil {
		_, _ = orig.Write([]byte(message + "\n"))
	} else {
		log.Println(message)
	}
}

type logCaptureWriter struct {
	logs *Logs
	buf  []byte
}

func (w *logCaptureWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		w.processLine(line)
	}
	return len(p), nil
}

func (w *logCaptureWriter) processLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	level := InferLogLevelFromMessage(line)
	if strings.HasPrefix(line, "[ERROR]") {
		level = LogLevelError
		line = strings.TrimSpace(strings.TrimPrefix(line, "[ERROR]"))
	} else if strings.HasPrefix(line, "[WARN]") {
		level = LogLevelWarn
		line = strings.TrimSpace(strings.TrimPrefix(line, "[WARN]"))
	}

	category := CategorizeLogMessage(line)
	_ = w.logs.insertCaptured(level, category, line)
}

func (logs *Logs) insertCaptured(level, category, message string) error {
	if logs.database == nil {
		return nil
	}

	logs.mutex.Lock()
	defer logs.mutex.Unlock()

	query := `INSERT INTO "logs" ("level", "category", "message", "timestamp") VALUES ($1, $2, $3, $4)`
	_, err := logs.database.Sql.Exec(query, level, category, message, timeNowUnixMilli())
	return err
}

func timeNowUnixMilli() int64 {
	return time.Now().UTC().UnixMilli()
}
