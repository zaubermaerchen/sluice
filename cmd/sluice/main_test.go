package main

// This file verifies CLI validation, event handling, and stream state behavior.

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRun_MissingNormalOperationArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithIO(strings.NewReader(""), &stdout, &stderr, nil)

	if exitCode != 2 {
		t.Fatalf("expected usage exit code 2, got %d", exitCode)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	if !strings.Contains(stderr.String(), "--open") {
		t.Fatalf("expected required flags in stderr, got %q", stderr.String())
	}
}

func TestRun_Version(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdin := &trackingReader{}

	exitCode := runWithIO(stdin, &stdout, &stderr, []string{"--version"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if got := stdout.String(); got != "sluice dev\n" {
		t.Fatalf("expected %q, got %q", "sluice dev\n", got)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if stdin.read {
		t.Fatal("expected version mode not to read stdin")
	}
}

func TestRun_Version_UsesInjectedValue(t *testing.T) {
	previousVersion := version
	version = "1.2.3"
	defer func() { version = previousVersion }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithIO(&trackingReader{}, &stdout, &stderr, []string{"--version"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "sluice 1.2.3\n" {
		t.Fatalf("expected %q, got %q", "sluice 1.2.3\n", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRun_Version_AcceptsGoFlagSpellings(t *testing.T) {
	for _, arg := range []string{"-version", "--version", "-version=true", "--version=true"} {
		t.Run(arg, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithIO(&trackingReader{}, &stdout, &stderr, []string{arg})
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
			}
			if got := stdout.String(); got != "sluice "+version+"\n" {
				t.Fatalf("expected %q, got %q", "sluice "+version+"\n", got)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
		})
	}
}

func TestRun_Version_RejectsExtraArguments(t *testing.T) {
	const wantVersionDiagnostic = "--version must be used alone and set to true"

	normalArgs := []string{"--open", "duration:0s", "--close", "duration:1h", "closed"}
	tests := []struct {
		name                  string
		args                  []string
		wantVersionDiagnostic bool
	}{
		{name: "version then positional", args: []string{"--version", "extra"}, wantVersionDiagnostic: true},
		{name: "positional then version", args: []string{"extra", "--version"}},
		{name: "version then valid flags", args: []string{"--version", "--open", "duration:1s", "--close", "duration:1s", "closed"}, wantVersionDiagnostic: true},
		{name: "valid flags then version", args: []string{"--open", "duration:1s", "--close", "duration:1s", "--version", "closed"}, wantVersionDiagnostic: true},
		{name: "version with mode flag", args: []string{"--version", "--mode", "discard"}, wantVersionDiagnostic: true},
		{name: "version repeated", args: []string{"--version", "--version"}, wantVersionDiagnostic: true},
		{name: "version with unknown flag", args: []string{"--version", "--unknown"}},
		{name: "unknown flag then version", args: []string{"--unknown", "--version"}},
		{name: "short version false", args: []string{"-version=false"}, wantVersionDiagnostic: true},
		{name: "long version false", args: []string{"--version=false"}, wantVersionDiagnostic: true},
		{name: "version false with valid flags", args: append([]string{"--version=false"}, normalArgs...), wantVersionDiagnostic: true},
		{name: "valid flags with version false", args: []string{"--open", "duration:0s", "--close", "duration:1h", "--version=false", "closed"}, wantVersionDiagnostic: true},
		{name: "version true then false", args: append([]string{"--version", "--version=false"}, normalArgs...), wantVersionDiagnostic: true},
		{name: "version false then true", args: append([]string{"--version=false", "--version"}, normalArgs...), wantVersionDiagnostic: true},
		{name: "version false repeated", args: append([]string{"--version=false", "--version=false"}, normalArgs...), wantVersionDiagnostic: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			stdin := &trackingReader{}

			exitCode := runWithIO(stdin, &stdout, &stderr, test.args)
			if exitCode != 2 {
				t.Fatalf("expected usage exit code 2, got %d; stderr=%q", exitCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "Usage of sluice:") {
				t.Fatalf("expected usage in stderr, got %q", stderr.String())
			}
			if test.wantVersionDiagnostic && !strings.Contains(stderr.String(), "sluice: "+wantVersionDiagnostic+"\n") {
				t.Fatalf("expected version diagnostic %q in stderr, got %q", wantVersionDiagnostic, stderr.String())
			}
			if stdin.read {
				t.Fatal("expected version validation not to read stdin")
			}
		})
	}
}

func TestRun_ArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing open", args: []string{"--close", "duration:1s", "closed"}},
		{name: "missing close", args: []string{"--open", "duration:1s", "closed"}},
		{name: "missing state", args: []string{"--open", "duration:1s", "--close", "duration:1s"}},
		{name: "invalid mode", args: []string{"--mode", "passthrough", "--open", "duration:1s", "--close", "duration:1s", "closed"}},
		{name: "invalid state", args: []string{"--open", "duration:1s", "--close", "duration:1s", "paused"}},
		{name: "extra state", args: []string{"--open", "duration:1s", "--close", "duration:1s", "open", "closed"}},
		{name: "duplicate open", args: []string{"--open", "duration:1s", "--open", "duration:2s", "--close", "duration:1s", "closed"}},
		{name: "duplicate close", args: []string{"--open", "duration:1s", "--close", "duration:1s", "--close", "duration:2s", "closed"}},
		{name: "duplicate mode", args: []string{"--mode", "block", "--mode", "discard", "--open", "duration:1s", "--close", "duration:1s", "closed"}},
		{name: "invalid event", args: []string{"--open", "file:name", "--close", "duration:1s", "closed"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithIO(strings.NewReader(""), &stdout, &stderr, test.args)

			if exitCode != 2 {
				t.Fatalf("expected usage exit code 2, got %d; stderr=%q", exitCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "Usage of sluice:") {
				t.Fatalf("expected usage in stderr, got %q", stderr.String())
			}
		})
	}
}

func TestParseEvent(t *testing.T) {
	tests := []struct {
		input    string
		kind     eventKind
		signal   string
		duration time.Duration
	}{
		{input: "signal:USR1", kind: eventSignal, signal: "USR1"},
		{input: "signal:SIGUSR1", kind: eventSignal, signal: "USR1"},
		{input: "signal:USR2", kind: eventSignal, signal: "USR2"},
		{input: "signal:SIGUSR2", kind: eventSignal, signal: "USR2"},
		{input: "duration:250ms", kind: eventDuration, duration: 250 * time.Millisecond},
		{input: "duration:0s", kind: eventDuration, duration: 0},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := parseEvent(test.input)
			if err != nil {
				t.Fatalf("parseEvent(%q) returned error: %v", test.input, err)
			}
			if got.kind != test.kind || got.signal != test.signal || got.duration != test.duration {
				t.Fatalf("parseEvent(%q) = %#v, want kind=%v signal=%q duration=%s", test.input, got, test.kind, test.signal, test.duration)
			}
		})
	}
}

func TestParseEvent_RejectsInvalidDurationsAndForms(t *testing.T) {
	for _, input := range []string{
		"duration:",
		"duration:-1s",
		"duration:not-a-duration",
		"duration:1",
		"signal:",
		"signal:SIGTERM",
		"signal:USR3",
		"file:name",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseEvent(input); err == nil {
				t.Fatalf("parseEvent(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestArmEvent_DurationZero(t *testing.T) {
	armed, err := armEvent(event{kind: eventDuration})
	if err != nil {
		t.Fatalf("armEvent returned error: %v", err)
	}
	defer armed.stop()

	select {
	case <-armed.ch:
	case <-time.After(time.Second):
		t.Fatal("duration:0s was not armed immediately")
	}
}

func TestEventForState_ArmsOnlyTheTransitionEvent(t *testing.T) {
	open := event{kind: eventDuration, duration: time.Second}
	close := event{kind: eventDuration, duration: 2 * time.Second}
	cfg := config{open: open, close: close}

	if got := eventForState(cfg, stateClosed); got != open {
		t.Fatalf("closed state arms %#v, want %#v", got, open)
	}
	if got := eventForState(cfg, stateOpen); got != close {
		t.Fatalf("open state arms %#v, want %#v", got, close)
	}
}

func TestStream_BlockReaderWaitsUntilOpen(t *testing.T) {
	var stdout bytes.Buffer
	readCalled := make(chan struct{}, 1)
	var source blockProbeReader
	source.read = func(p []byte) (int, error) {
		readCalled <- struct{}{}
		return 0, io.EOF
	}

	stream := newStream(&source, &stdout, modeBlock)
	stream.setState(stateClosed)
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(stream.writer, stream.reader)
		copyDone <- err
	}()

	select {
	case <-readCalled:
		t.Fatal("reader was called while the stream was closed")
	case <-time.After(100 * time.Millisecond):
	}

	stream.setState(stateOpen)
	select {
	case <-readCalled:
	case <-time.After(time.Second):
		t.Fatal("reader was not called after opening")
	}
	select {
	case err := <-copyDone:
		if err != nil {
			t.Fatalf("copy returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("copy did not finish after the reader reached EOF")
	}
}

func TestStream_BlockPropagatesBackpressure(t *testing.T) {
	source, input := io.Pipe()
	var stdout bytes.Buffer
	stream := newStream(source, &stdout, modeBlock)
	stream.setState(stateClosed)

	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(stream.writer, stream.reader)
		copyDone <- err
	}()

	writeDone := make(chan error, 1)
	go func() {
		_, err := input.Write([]byte("backpressure"))
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		input.Close()
		if err == nil {
			t.Fatal("input write completed while the stream was closed")
		}
	case <-time.After(100 * time.Millisecond):
	}

	stream.setState(stateOpen)
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("input write returned error after opening: %v", err)
		}
	case <-time.After(time.Second):
		input.Close()
		t.Fatal("input write did not complete after opening")
	}

	if err := input.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil {
			t.Fatalf("copy returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("copy did not finish after closing input")
	}
	if got := stdout.String(); got != "backpressure" {
		t.Fatalf("expected forwarded input, got %q", got)
	}
}

type blockProbeReader struct {
	read func([]byte) (int, error)
}

func (r *blockProbeReader) Read(p []byte) (int, error) {
	return r.read(p)
}

func closeOnce(ch chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() { close(ch) })
	}
}

type eofGateReader struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (r *eofGateReader) Read(p []byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	p[0] = 'x'
	return 1, io.EOF
}

func TestRun_DurationOpensClosedStream(t *testing.T) {
	const input = "opened by duration\n"
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithIO(strings.NewReader(input), &stdout, &stderr, []string{
		"--open", "duration:0s",
		"--close", "duration:1h",
		"closed",
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
	}
	if stdout.String() != input {
		t.Fatalf("expected forwarded input %q, got %q", input, stdout.String())
	}
}

func TestRun_TwoZeroDurationsDoNotHangBeforeStarting(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithIO(strings.NewReader(""), &stdout, &stderr, []string{
			"--open", "duration:0s",
			"--close", "duration:0s",
			"closed",
		})
	}()

	select {
	case exitCode := <-done:
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("zero-duration transitions did not reach EOF")
	}
}

func TestRun_OpenForwardsInputUntilEOF(t *testing.T) {
	const input = "first\x00second\n"
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithIO(strings.NewReader(input), &stdout, &stderr, []string{
		"--open", "duration:1h",
		"--close", "duration:1h",
		"open",
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
	}
	if stdout.String() != input {
		t.Fatalf("expected forwarded input %q, got %q", input, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRun_ClosedDiscardReadsToEOF(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithIO(strings.NewReader("discard me"), &stdout, &stderr, []string{
		"--mode", "discard",
		"--open", "duration:1h",
		"--close", "duration:1h",
		"closed",
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected discarded input, got %q", stdout.String())
	}
}

func TestRun_ReportsReaderAndWriterErrors(t *testing.T) {
	t.Run("reader", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		wantErr := errors.New("reader failed")

		exitCode := runWithIO(errorReader{err: wantErr}, &stdout, &stderr, []string{
			"--open", "duration:1h",
			"--close", "duration:1h",
			"open",
		})

		if exitCode == 0 {
			t.Fatal("expected non-zero exit code")
		}
		if !strings.Contains(stderr.String(), wantErr.Error()) {
			t.Fatalf("expected reader error %q in stderr, got %q", wantErr, stderr.String())
		}
	})

	t.Run("writer", func(t *testing.T) {
		var stderr bytes.Buffer
		wantErr := errors.New("writer failed")

		exitCode := runWithIO(strings.NewReader("write me"), errorWriter{err: wantErr}, &stderr, []string{
			"--open", "duration:1h",
			"--close", "duration:1h",
			"open",
		})

		if exitCode == 0 {
			t.Fatal("expected non-zero exit code")
		}
		if !strings.Contains(stderr.String(), wantErr.Error()) {
			t.Fatalf("expected writer error %q in stderr, got %q", wantErr, stderr.String())
		}
	})
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type trackingReader struct {
	read bool
}

func (r *trackingReader) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ io.Reader = errorReader{}
var _ io.Writer = errorWriter{}

func TestRun_Help(t *testing.T) {
	wantContent := []string{
		"  sluice [--mode block|discard] --open EVENT --close EVENT open|closed",
		"open|closed is the initial stream state",
		"signal event forms and examples are POSIX-only",
		"signal:USR1 / signal:SIGUSR1",
		"signal:USR2 / signal:SIGUSR2",
		"duration:DURATION",
		"block: do not read stdin while closed; propagates backpressure upstream",
		"discard: read and discard stdin while closed",
		"sluice --open signal:USR1 --close signal:USR2 closed",
		"sluice --open signal:USR1 --close signal:USR1 closed",
		"sluice --open duration:5s --close duration:10s closed",
		"(default block)",
	}

	for _, args := range [][]string{{"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			stdin := &trackingReader{}

			exitCode := runWithIO(stdin, &stdout, &stderr, args)

			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}

			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}

			got := stderr.String()
			if !strings.Contains(got, "Usage of sluice:") {
				t.Fatalf("expected usage in stderr, got %q", got)
			}
			if strings.Contains(got, "Usage: sluice") {
				t.Fatalf("expected one usage heading, got %q", got)
			}

			for _, content := range wantContent {
				if !strings.Contains(got, content) {
					t.Fatalf("expected help to contain %q, got %q", content, got)
				}
			}

			if !strings.Contains(got, "-version") {
				t.Fatalf("expected version flag in usage, got %q", got)
			}
			if stdin.read {
				t.Fatal("expected help mode not to read stdin")
			}
		})
	}
}

func TestRun_InvalidFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, []string{"--unknown"})

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "flag provided but not defined: -unknown") {
		t.Fatalf("expected invalid flag error in stderr, got %q", got)
	}

	if !strings.Contains(got, "Usage of sluice:") {
		t.Fatalf("expected usage in stderr, got %q", got)
	}
}

func TestRun_HelpWithInvalidFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, []string{"--help", "--unknown"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "Usage of sluice:") {
		t.Fatalf("expected usage in stderr, got %q", got)
	}

	if strings.Contains(got, "flag provided but not defined") {
		t.Fatalf("did not expect invalid flag error when help is requested, got %q", got)
	}
}

func TestRun_SignalTransitionBlocksUntilReopened(t *testing.T) {
	if !testSignalsSupported() {
		t.Skip("process signals are not supported on this platform")
	}

	pipeReader, inputWriter := io.Pipe()
	input := newReadGate(pipeReader)
	stdout := newLockedBuffer()
	output := newFirstWriteGate(stdout)
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithIO(input, output, &stderr, []string{
			"--open", "duration:500ms",
			"--close", "signal:USR2",
			"open",
		})
	}()
	defer func() {
		output.release()
		input.stop()
		cleanupSignalRun(t, inputWriter, done, "USR1", "USR2")
	}()

	if !input.waitForRead(time.Second) {
		t.Fatal("initial open read did not start")
	}
	input.allowRead()
	firstWrite := writeAsync(inputWriter, "before\n")
	if !output.waitForWrite(time.Second) {
		t.Fatal("initial write did not reach the destination")
	}
	if err := sendTestSignal("USR2"); err != nil {
		t.Fatalf("send close signal: %v", err)
	}
	select {
	case err := <-firstWrite:
		if err != nil {
			t.Fatalf("initial open write failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("initial open write did not finish")
	}
	if !input.waitForSourceRead(time.Second) {
		t.Fatal("initial source read did not start")
	}
	// The signal handler and state loop must process the close before the
	// blocked destination is released. If the state stayed open, the next
	// gated read below would start immediately and fail the assertion.
	time.Sleep(100 * time.Millisecond)
	output.release()
	if input.waitForRead(200 * time.Millisecond) {
		t.Fatal("reader started while the block-mode stream was closed")
	}

	closedWrite := writeAsync(inputWriter, "while-closed\n")
	select {
	case err := <-closedWrite:
		t.Fatalf("closed block write completed before reopening: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	// OPEN is a duration event for this leg, so it is armed only after the
	// signal-driven close and does not have a second signal-arm race.
	if !input.waitForRead(time.Second) {
		t.Fatal("reader did not resume after the open transition")
	}
	input.allowRead()
	if err := waitForWrite(closedWrite, time.Second); err != nil {
		t.Fatalf("write did not complete after reopening: %v", err)
	}
	if !stdout.waitFor("before\nwhile-closed\n", time.Second) {
		t.Fatalf("bytes were not forwarded across the open intervals; got %q", stdout.String())
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	input.allowRead()

	select {
	case exitCode := <-done:
		if exitCode != 0 {
			t.Fatalf("expected successful EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after reopening and EOF")
	}
}

func TestRun_SameSignalRepeatedlyTogglesState(t *testing.T) {
	pipeReader, inputWriter := io.Pipe()
	stdout := newLockedBuffer()
	output := newFirstWriteGate(stdout)
	var stderr bytes.Buffer
	arms := make(chan chan struct{}, 4)
	arm := func(got event) (armedEvent, error) {
		if got.kind != eventSignal || got.signal != "USR1" {
			t.Fatalf("armed %#v, want signal:USR1", got)
		}
		ch := make(chan struct{}, 1)
		arms <- ch
		return armedEvent{ch: ch, stop: func() {}}, nil
	}
	cfg := config{
		mode:    modeBlock,
		open:    event{kind: eventSignal, signal: "USR1"},
		close:   event{kind: eventSignal, signal: "USR1"},
		initial: stateClosed,
	}
	done := make(chan int, 1)
	go func() {
		done <- runStateMachineWithArmer(pipeReader, output, &stderr, cfg, arm)
	}()
	defer func() {
		output.release()
		_ = inputWriter.Close()
	}()

	firstWrite := writeAsync(inputWriter, "first\n")
	select {
	case err := <-firstWrite:
		t.Fatalf("initially closed write completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	firstArm := <-arms
	firstArm <- struct{}{}
	secondArm := <-arms
	if !output.waitForWrite(time.Second) {
		t.Fatal("first open write did not reach the destination")
	}
	if err := waitForWrite(firstWrite, time.Second); err != nil {
		t.Fatalf("write did not complete after opening: %v", err)
	}

	secondArm <- struct{}{}
	thirdArm := <-arms
	output.release()
	if !stdout.waitFor("first\n", time.Second) {
		t.Fatalf("first open bytes were not forwarded; got %q", stdout.String())
	}
	secondWrite := writeAsync(inputWriter, "second\n")
	select {
	case err := <-secondWrite:
		t.Fatalf("second write completed while toggled closed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	thirdArm <- struct{}{}
	<-arms
	if err := waitForWrite(secondWrite, time.Second); err != nil {
		t.Fatalf("second write did not complete after reopening: %v", err)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	select {
	case exitCode := <-done:
		if exitCode != 0 {
			t.Fatalf("expected successful EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after repeated toggles and EOF")
	}
	if got := stdout.String(); got != "first\nsecond\n" {
		t.Fatalf("expected bytes from both open intervals, got %q", got)
	}
}

func TestRun_EOFObservedDuringOpenReadCompletesAfterCloseTransition(t *testing.T) {
	input := &eofGateReader{started: make(chan struct{}), release: make(chan struct{})}
	releaseInput := closeOnce(input.release)
	output := newFirstWriteGate(&bytes.Buffer{})
	var stderr bytes.Buffer
	firstEvent := make(chan struct{}, 1)
	nextEvent := make(chan struct{}, 1)
	nextArmEntered := make(chan struct{})
	allowNextArm := make(chan struct{})
	var allowNextArmOnce sync.Once
	allowArm := func() {
		allowNextArmOnce.Do(func() { close(allowNextArm) })
	}
	var armCalls int
	arm := func(event) (armedEvent, error) {
		armCalls++
		switch armCalls {
		case 1:
			return armedEvent{ch: firstEvent, stop: func() {}}, nil
		case 2:
			close(nextArmEntered)
			<-allowNextArm
			return armedEvent{ch: nextEvent, stop: func() {}}, nil
		default:
			return armedEvent{}, errors.New("unexpected extra arm")
		}
	}
	cfg := config{
		mode:    modeBlock,
		open:    event{kind: eventSignal, signal: "USR1"},
		close:   event{kind: eventSignal, signal: "USR2"},
		initial: stateOpen,
	}
	done := make(chan int, 1)
	go func() {
		done <- runStateMachineWithArmer(input, output, &stderr, cfg, arm)
	}()
	finished := false
	defer func() {
		releaseInput()
		output.release()
		allowArm()
		select {
		case nextEvent <- struct{}{}:
		default:
		}
		if !finished {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	}()

	select {
	case <-input.started:
	case <-time.After(time.Second):
		t.Fatal("open read did not start")
	}
	releaseInput()
	if !output.waitForWrite(time.Second) {
		t.Fatal("copy did not reach the gated write")
	}
	firstEvent <- struct{}{}
	select {
	case <-nextArmEntered:
	case <-time.After(time.Second):
		t.Fatal("close transition did not attempt to arm the next event")
	}
	output.release()
	allowArm()

	select {
	case exitCode := <-done:
		finished = true
		if exitCode != 0 {
			t.Fatalf("expected successful EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("EOF observed by an open read waited for a later open event")
	}
}

func TestRun_ArmsNextEventBeforePublishingState(t *testing.T) {
	firstReadStarted := make(chan struct{})
	secondReadStarted := make(chan struct{})
	releaseFirstRead := make(chan struct{})
	releaseSecondRead := make(chan struct{})
	closeFirstRead := closeOnce(releaseFirstRead)
	closeSecondRead := closeOnce(releaseSecondRead)
	var readCount int
	input := &blockProbeReader{read: func([]byte) (int, error) {
		readCount++
		switch readCount {
		case 1:
			close(firstReadStarted)
			<-releaseFirstRead
			return 0, nil
		case 2:
			close(secondReadStarted)
			<-releaseSecondRead
			return 0, io.EOF
		default:
			return 0, errors.New("unexpected extra read")
		}
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	initialEvent := make(chan struct{}, 1)
	initialEvent <- struct{}{}
	transitionEvent := make(chan struct{}, 1)
	nextEvent := make(chan struct{}, 1)
	nextArmEntered := make(chan struct{})
	allowNextArm := make(chan struct{})
	var allowNextArmOnce sync.Once
	allowArm := func() {
		allowNextArmOnce.Do(func() { close(allowNextArm) })
	}
	var armCalls int
	arm := func(event) (armedEvent, error) {
		armCalls++
		switch armCalls {
		case 1:
			return armedEvent{ch: initialEvent, stop: func() {}}, nil
		case 2:
			return armedEvent{ch: transitionEvent, stop: func() {}}, nil
		case 3:
			close(nextArmEntered)
			<-allowNextArm
			return armedEvent{ch: nextEvent, stop: func() {}}, nil
		default:
			return armedEvent{}, errors.New("unexpected extra arm")
		}
	}
	cfg := config{
		mode:    modeBlock,
		open:    event{kind: eventDuration, duration: time.Hour},
		close:   event{kind: eventSignal, signal: "USR2"},
		initial: stateClosed,
	}
	done := make(chan int, 1)
	go func() {
		done <- runStateMachineWithArmer(input, &stdout, &stderr, cfg, arm)
	}()
	finished := false
	defer func() {
		closeFirstRead()
		closeSecondRead()
		allowArm()
		select {
		case nextEvent <- struct{}{}:
		default:
		}
		if !finished {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	}()

	select {
	case <-firstReadStarted:
	case <-time.After(time.Second):
		t.Fatal("initial open read did not start")
	}
	transitionEvent <- struct{}{}
	select {
	case <-nextArmEntered:
	case <-time.After(time.Second):
		t.Fatal("open transition did not attempt to arm the next event")
	}
	closeFirstRead()
	select {
	case <-secondReadStarted:
	case <-time.After(time.Second):
		t.Fatal("reader stayed disabled while the next event was being armed")
	}
	allowArm()
	closeSecondRead()

	select {
	case exitCode := <-done:
		finished = true
		if exitCode != 0 {
			t.Fatalf("expected successful EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after the source reached EOF")
	}
}

func TestRun_StartupArmsNextEventBeforeStoppingCurrent(t *testing.T) {
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	releaseInput := closeOnce(releaseRead)
	var readStartOnce sync.Once
	input := &blockProbeReader{read: func([]byte) (int, error) {
		readStartOnce.Do(func() { close(readStarted) })
		<-releaseRead
		return 0, io.EOF
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	initialEvent := make(chan struct{}, 1)
	initialEvent <- struct{}{}
	firstStopped := make(chan struct{})
	secondArmEntered := make(chan struct{})
	allowSecondArm := make(chan struct{})
	var allowSecondArmOnce sync.Once
	allowArm := func() {
		allowSecondArmOnce.Do(func() { close(allowSecondArm) })
	}
	var armCalls int
	arm := func(event) (armedEvent, error) {
		armCalls++
		switch armCalls {
		case 1:
			return armedEvent{ch: initialEvent, stop: closeOnce(firstStopped)}, nil
		case 2:
			close(secondArmEntered)
			<-allowSecondArm
			return armedEvent{ch: make(chan struct{}), stop: func() {}}, nil
		default:
			return armedEvent{}, errors.New("unexpected extra arm")
		}
	}
	cfg := config{
		mode:    modeBlock,
		open:    event{kind: eventDuration, duration: time.Hour},
		close:   event{kind: eventDuration, duration: time.Hour},
		initial: stateClosed,
	}
	done := make(chan int, 1)
	go func() {
		done <- runStateMachineWithArmer(input, &stdout, &stderr, cfg, arm)
	}()
	finished := false
	defer func() {
		allowArm()
		releaseInput()
		if !finished {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	}()

	select {
	case <-secondArmEntered:
	case <-time.After(time.Second):
		t.Fatal("startup did not attempt to arm the next event")
	}
	select {
	case <-firstStopped:
		t.Fatal("startup stopped the current event before arming the next event")
	default:
	}
	select {
	case <-readStarted:
		t.Fatal("startup published the new state before arming the next event")
	default:
	}

	allowArm()
	select {
	case <-firstStopped:
	case <-time.After(time.Second):
		t.Fatal("startup did not stop the current event after arming the next event")
	}
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("reader did not start after the next event was armed")
	}
	releaseInput()

	select {
	case exitCode := <-done:
		finished = true
		if exitCode != 0 {
			t.Fatalf("expected successful EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after startup transition")
	}
}

func TestRun_DurationStartsWhenStateIsEntered(t *testing.T) {
	if !testSignalsSupported() {
		t.Skip("process signals are not supported on this platform")
	}

	pipeReader, inputWriter := io.Pipe()
	input := newReadGate(pipeReader)
	stdout := newLockedBuffer()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithIO(input, stdout, &stderr, []string{
			"--open", "signal:USR1",
			"--close", "duration:500ms",
			"closed",
		})
	}()
	defer func() {
		input.stop()
		cleanupSignalRun(t, inputWriter, done, "USR1")
	}()

	firstWrite := writeAsync(inputWriter, "first\n")
	select {
	case err := <-firstWrite:
		t.Fatalf("initially closed write completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// The close duration must not age while the sluice is still closed. Waiting
	// longer than that duration before opening makes an early-started timer
	// observable without relying on an exact transition timestamp.
	time.Sleep(700 * time.Millisecond)
	if err := sendTestSignal("USR1"); err != nil {
		t.Fatalf("send open signal: %v", err)
	}
	if !input.waitForRead(time.Second) {
		t.Fatal("reader did not start after opening")
	}
	input.allowRead()
	if err := waitForWrite(firstWrite, time.Second); err != nil {
		t.Fatalf("first write did not complete after opening: %v", err)
	}
	if !stdout.waitFor("first\n", time.Second) {
		t.Fatalf("first bytes were not forwarded after opening; got %q", stdout.String())
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	input.allowRead()

	select {
	case exitCode := <-done:
		if exitCode != 0 {
			t.Fatalf("expected successful EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after duration re-arm")
	}
}

func TestRun_DiscardClosedBytesThenForwardsOpenBytes(t *testing.T) {
	if !testSignalsSupported() {
		t.Skip("process signals are not supported on this platform")
	}

	pipeReader, inputWriter := io.Pipe()
	input := newReadGate(pipeReader)
	stdout := newLockedBuffer()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWithIO(input, stdout, &stderr, []string{
			"--mode", "discard",
			"--open", "signal:USR1",
			"--close", "signal:USR2",
			"closed",
		})
	}()
	defer func() {
		input.stop()
		cleanupSignalRun(t, inputWriter, done, "USR1", "USR2")
	}()
	if !input.waitForRead(time.Second) {
		t.Fatal("closed discard read did not start")
	}
	input.allowRead()

	closedWrite := writeAsync(inputWriter, "discarded\n")
	if err := waitForWrite(closedWrite, time.Second); err != nil {
		t.Fatalf("closed discard write failed: %v", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("closed bytes were written in discard mode: %q", got)
	}

	if !input.waitForRead(time.Second) {
		t.Fatal("next discard read did not start")
	}
	if err := sendTestSignal("USR1"); err != nil {
		t.Fatalf("send open signal: %v", err)
	}
	// Signal delivery is asynchronous. Keep the pending source read gated until
	// the state loop has had a chance to consume the signal; the output below
	// remains the observable assertion that the OPEN state was reached.
	time.Sleep(100 * time.Millisecond)
	input.allowRead()
	openWrite := writeAsync(inputWriter, "forwarded\n")
	if err := waitForWrite(openWrite, time.Second); err != nil {
		t.Fatalf("open write failed: %v", err)
	}
	if !stdout.waitFor("forwarded\n", time.Second) {
		t.Fatalf("open bytes were not forwarded; got %q", stdout.String())
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	input.allowRead()

	select {
	case exitCode := <-done:
		if exitCode != 0 {
			t.Fatalf("expected successful discard EOF exit, got %d; stderr=%q", exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("discard run did not finish at EOF")
	}
}

func TestRun_SignalEventsAreRejectedOnUnsupportedPlatforms(t *testing.T) {
	mode := singleValue{value: "block"}
	open := singleValue{value: "signal:USR1", set: true}
	close := singleValue{value: "duration:1s", set: true}
	_, err := parseConfig(mode, open, close, []string{"closed"})
	if testSignalsSupported() {
		if err != nil {
			t.Fatalf("signal event unexpectedly rejected on supported platform: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("signal event unexpectedly accepted on unsupported platform")
	}
	if !strings.Contains(err.Error(), "signal events are not supported") {
		t.Fatalf("expected unsupported-signal error, got %v", err)
	}
}

type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func newLockedBuffer() *lockedBuffer {
	return &lockedBuffer{}
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func (b *lockedBuffer) waitFor(want string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if b.String() == want {
			return true
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return b.String() == want
		}
	}
}

func writeAsync(writer *io.PipeWriter, input string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte(input))
		done <- err
	}()
	return done
}

func waitForWrite(done <-chan error, timeout time.Duration) error {
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return errors.New("timed out waiting for input write")
	}
}

type readGate struct {
	source        io.Reader
	started       chan struct{}
	sourceStarted chan struct{}
	allow         chan struct{}
	stopCh        chan struct{}
	stopOnce      sync.Once
}

func newReadGate(source io.Reader) *readGate {
	return &readGate{
		source:        source,
		started:       make(chan struct{}, 1),
		sourceStarted: make(chan struct{}, 1),
		allow:         make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
	}
}

func (r *readGate) Read(p []byte) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.allow:
		select {
		case r.sourceStarted <- struct{}{}:
		default:
		}
		return r.source.Read(p)
	case <-r.stopCh:
		return 0, io.EOF
	}
}

func (r *readGate) waitForRead(timeout time.Duration) bool {
	select {
	case <-r.started:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *readGate) waitForSourceRead(timeout time.Duration) bool {
	select {
	case <-r.sourceStarted:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (r *readGate) allowRead() {
	select {
	case r.allow <- struct{}{}:
	case <-r.stopCh:
	}
}

func (r *readGate) stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}

type firstWriteGate struct {
	destination io.Writer
	started     chan struct{}
	releaseCh   chan struct{}
	releaseOnce sync.Once
	writeOnce   sync.Once
}

func newFirstWriteGate(destination io.Writer) *firstWriteGate {
	return &firstWriteGate{
		destination: destination,
		started:     make(chan struct{}, 1),
		releaseCh:   make(chan struct{}),
	}
}

func (w *firstWriteGate) Write(p []byte) (int, error) {
	w.writeOnce.Do(func() {
		close(w.started)
		<-w.releaseCh
	})
	return w.destination.Write(p)
}

func (w *firstWriteGate) waitForWrite(timeout time.Duration) bool {
	select {
	case <-w.started:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (w *firstWriteGate) release() {
	w.releaseOnce.Do(func() { close(w.releaseCh) })
}

func cleanupSignalRun(t *testing.T, inputWriter *io.PipeWriter, done <-chan int, signals ...string) {
	t.Helper()
	if err := inputWriter.Close(); err != nil {
		t.Logf("close test input: %v", err)
	}
	if testSignalsSupported() {
		for i := 0; i < 3; i++ {
			for _, signal := range signals {
				_ = sendTestSignal(signal)
			}
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Log("test run did not finish during cleanup")
	}
}
