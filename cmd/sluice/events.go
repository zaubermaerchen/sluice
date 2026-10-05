package main

// This file emits optional machine-readable lifecycle events for committed stream transitions.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type lifecycleEventRecord struct {
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
}

type eventEmitter struct {
	file        *os.File
	diagnostics io.Writer
	now         func() time.Time

	mu       sync.Mutex
	disabled bool
	closed   bool
	warnOnce sync.Once
}

func newEventEmitterChecked(fd uintptr, diagnostics io.Writer, now func() time.Time) (*eventEmitter, error) {
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	if fd < 3 {
		return nil, fmt.Errorf("invalid file descriptor %d", fd)
	}
	file, err := duplicateEventFile(fd)
	if err != nil {
		return nil, fmt.Errorf("invalid event file descriptor %d: %w", fd, err)
	}
	if now == nil {
		now = time.Now
	}
	return &eventEmitter{file: file, diagnostics: diagnostics, now: now}, nil
}

func (emitter *eventEmitter) emit(event string) {
	if emitter == nil {
		return
	}
	emitter.emitRecord(lifecycleEventRecord{
		Event:     event,
		Timestamp: emitter.now().UTC().Format(time.RFC3339Nano),
	})
}

func (emitter *eventEmitter) emitRecord(record lifecycleEventRecord) {
	if emitter == nil {
		return
	}

	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if emitter.disabled || emitter.closed {
		return
	}

	line, err := json.Marshal(record)
	if err == nil {
		line = append(line, '\n')
		var written int
		written, err = writeEvent(emitter.file, line)
		if err == nil && written != len(line) {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		emitter.disabled = true
		emitter.warnOnce.Do(func() { reportEventFailure(emitter.diagnostics, err) })
	}
}

func (emitter *eventEmitter) close() {
	if emitter == nil {
		return
	}
	emitter.mu.Lock()
	if emitter.closed {
		emitter.mu.Unlock()
		return
	}
	emitter.closed = true
	_ = emitter.file.Close()
	emitter.mu.Unlock()
}

type diagnosticWriter struct {
	writer io.Writer
	mu     sync.Mutex
}

func newDiagnosticWriter(writer io.Writer) *diagnosticWriter {
	return &diagnosticWriter{writer: writer}
}

func (writer *diagnosticWriter) Write(p []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(p)
}

// Primary errors normally write synchronously. A stalled event warning must not
// hold up termination; when it owns stderr, leave this diagnostic undelivered
// rather than starting another goroutine that could also remain blocked.
func reportPrimaryFailure(diagnostics io.Writer, format string, args ...any) {
	if writer, ok := diagnostics.(*diagnosticWriter); ok {
		if !writer.mu.TryLock() {
			return
		}
		defer writer.mu.Unlock()
		diagnostics = writer.writer
	}
	_, _ = fmt.Fprintf(diagnostics, format, args...)
}

func reportEventFailure(diagnostics io.Writer, err error) {
	if diagnostics == nil {
		return
	}
	message := fmt.Sprintf("sluice: events disabled: %v\n", err)
	// A diagnostic writer can be a pipe whose reader is stalled. Reporting in a
	// separate goroutine keeps a failed observation path from stopping the
	// stream state machine or data copy. Exit never waits for this one attempt,
	// so delivery before process termination is not guaranteed.
	go func() {
		_, _ = io.WriteString(diagnostics, message)
	}()
}
