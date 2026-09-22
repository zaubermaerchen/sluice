//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file verifies signal-triggered lifecycle observations on Unix targets.

import (
	"bufio"
	"bytes"
	"io"
	"testing"
	"time"
)

func TestRunEventsFDEmitsSignalTransitions(t *testing.T) {
	if !testSignalsSupported() {
		t.Skip("process signals are not supported on this platform")
	}

	events := newTestEventSink(t)

	pipeReader, inputWriter := io.Pipe()
	input := newReadGate(pipeReader)
	var stdout bytes.Buffer
	diagnostics := newLockedBuffer()
	done := make(chan int, 1)
	go func() {
		done <- runWithIO(input, &stdout, diagnostics, []string{
			"--mode", "discard",
			"--events-fd", formatEventFD(events.fd),
			"--open", "signal:USR1",
			"--close", "signal:USR1",
			"closed",
		})
	}()
	defer func() {
		input.stop()
		cleanupSignalRun(t, inputWriter, done, "USR1")
	}()

	if !input.waitForRead(time.Second) {
		t.Fatal("signal test did not start its gated read")
	}
	reader := bufio.NewReader(events.reader)
	if err := sendTestSignal("USR1"); err != nil {
		t.Fatal(err)
	}
	if got := readTestLifecycleEvent(t, reader).Event; got != "stream-open" {
		t.Fatalf("first signal event = %q, want stream-open", got)
	}
	if err := sendTestSignal("USR1"); err != nil {
		t.Fatal(err)
	}
	if got := readTestLifecycleEvent(t, reader).Event; got != "stream-closed" {
		t.Fatalf("second signal event = %q, want stream-closed", got)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	input.allowRead()

	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("run status = %d, want 0; stderr = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("signal event run did not finish after EOF")
	}
}
