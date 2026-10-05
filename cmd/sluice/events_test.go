package main

// This file verifies optional lifecycle events and their CLI integration.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newEventEmitter(fd uintptr, diagnostics io.Writer) *eventEmitter {
	emitter, err := newEventEmitterChecked(fd, diagnostics, time.Now)
	if err != nil {
		reportEventFailure(diagnostics, err)
		return nil
	}
	return emitter
}

type testLifecycleEvent struct {
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
}

type testEventSink struct {
	reader *os.File
	writer *os.File
	fd     uintptr
}

type ioDiscardLocked struct{}

func (ioDiscardLocked) Write(p []byte) (int, error) { return len(p), nil }

func formatEventFD(fd uintptr) string {
	return strconv.FormatUint(uint64(fd), 10)
}

func (sink *testEventSink) close() {
	if sink == nil {
		return
	}
	_ = sink.reader.Close()
	_ = sink.writer.Close()
}

func TestParseEventsFD(t *testing.T) {
	for _, test := range []struct {
		value string
		want  uintptr
	}{
		{value: "3", want: 3},
		{value: "+3", want: 3},
		{value: "42", want: 42},
	} {
		t.Run(test.value, func(t *testing.T) {
			if got, err := parseEventsFD(test.value); err != nil || got != test.want {
				t.Fatalf("parseEventsFD(%q) = %d, %v; want %d, nil", test.value, got, err, test.want)
			}
		})
	}
	for _, value := range []string{"", "-1", "2", "not-a-number"} {
		t.Run("reject "+value, func(t *testing.T) {
			if _, err := parseEventsFD(value); err == nil {
				t.Fatalf("parseEventsFD(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestParseEventsFDUsesNativeUintptrRange(t *testing.T) {
	maxUintptr := ^uintptr(0)
	maxInt := uintptr(^uint(0) >> 1)
	for _, want := range []uintptr{maxInt + 1, maxUintptr} {
		value := strconv.FormatUint(uint64(want), 10)
		t.Run(value, func(t *testing.T) {
			got, err := parseEventsFD(value)
			if err != nil {
				t.Fatalf("parseEventsFD(%q) returned error: %v", value, err)
			}
			if uint64(got) != uint64(want) {
				t.Fatalf("parseEventsFD(%q) = %d, want %d", value, got, want)
			}
		})
	}

	value := strconv.FormatUint(uint64(maxUintptr), 10) + "0"
	if _, err := parseEventsFD(value); err == nil {
		t.Fatalf("parseEventsFD(%q) unexpectedly succeeded", value)
	}
}

func TestRunEventsFDRejectsDuplicateOption(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runWithIO(strings.NewReader(""), &stdout, &stderr, []string{
		"--events-fd", "3",
		"--events-fd=4",
		"--open", "duration:1h",
		"--close", "duration:1h",
		"open",
	})
	if status != 2 {
		t.Fatalf("run status = %d, want usage error; stderr = %q", status, stderr.String())
	}
	if !strings.Contains(stderr.String(), "flag specified more than once") {
		t.Fatalf("stderr = %q, want duplicate option diagnostic", stderr.String())
	}
}

func TestRunEventsFDEmitsCommittedOpenTransition(t *testing.T) {
	events := newTestEventSink(t)
	var stdout, stderr bytes.Buffer

	status := runWithIO(strings.NewReader("forwarded"), &stdout, &stderr, []string{
		"--events-fd=" + formatEventFD(events.fd),
		"--open", "duration:0s",
		"--close", "duration:1h",
		"closed",
	})
	if status != 0 {
		t.Fatalf("run status = %d, want 0; stderr = %q", status, stderr.String())
	}
	if got := stdout.String(); got != "forwarded" {
		t.Fatalf("stdout = %q, want forwarded input", got)
	}
	assertTestLifecycleEvents(t, readTestLifecycleEvents(t, events), []string{"stream-open"})
}

func TestRunEventsFDEmitsCommittedCloseTransition(t *testing.T) {
	events := newTestEventSink(t)
	var stdout, stderr bytes.Buffer

	status := runWithIO(strings.NewReader("discarded"), &stdout, &stderr, []string{
		"--events-fd", formatEventFD(events.fd),
		"--mode", "discard",
		"--open", "duration:1h",
		"--close", "duration:0s",
		"open",
	})
	if status != 0 {
		t.Fatalf("run status = %d, want 0; stderr = %q", status, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty discarded stream", stdout.String())
	}
	assertTestLifecycleEvents(t, readTestLifecycleEvents(t, events), []string{"stream-closed"})
}

func TestRunEventsFDEmitsRepeatedTransitionsInOrder(t *testing.T) {
	events := newTestEventSink(t)

	input, inputWriter := io.Pipe()
	defer inputWriter.Close()
	eventSignals := []chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)}
	armEntered := make(chan int, len(eventSignals)+1)
	armCalls := 0
	arm := func(event) (armedEvent, error) {
		index := armCalls
		armCalls++
		armEntered <- index
		if index >= len(eventSignals) {
			return armedEvent{ch: make(chan struct{}), stop: func() {}}, nil
		}
		return armedEvent{ch: eventSignals[index], stop: func() {}}, nil
	}
	cfg := config{
		mode:        modeDiscard,
		open:        event{kind: eventSignal, signal: "USR1"},
		close:       event{kind: eventSignal, signal: "USR1"},
		initial:     stateClosed,
		eventsFD:    events.fd,
		eventsFDSet: true,
	}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runStateMachineWithArmer(input, &stdout, &stderr, cfg, arm)
	}()

	select {
	case <-armEntered:
	case <-time.After(time.Second):
		t.Fatal("initial event was not armed")
	}
	reader := bufio.NewReader(events.reader)
	for index, want := range []string{"stream-open", "stream-closed", "stream-open"} {
		eventSignals[index] <- struct{}{}
		if got := readTestLifecycleEvent(t, reader); got.Event != want {
			t.Fatalf("event %d = %q, want %q", index, got.Event, want)
		}
		if index < len(eventSignals)-1 {
			select {
			case <-armEntered:
			case <-time.After(time.Second):
				t.Fatalf("event %d did not arm the next transition", index)
			}
		}
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("run status = %d, want 0; stderr = %q", status, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after EOF")
	}
}

func TestRunEventsFDDoesNotEmitInitialStateOrEOFTransitions(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "initial open",
			args: []string{
				"--open", "duration:1h",
				"--close", "duration:1h",
				"open",
			},
		},
		{
			name: "initial closed",
			args: []string{
				"--mode", "discard",
				"--open", "duration:1h",
				"--close", "duration:1h",
				"closed",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := newTestEventSink(t)
			args := append([]string{
				"--events-fd",
				formatEventFD(events.fd),
			}, test.args...)
			var stdout, stderr bytes.Buffer
			if status := runWithIO(strings.NewReader("input"), &stdout, &stderr, args); status != 0 {
				t.Fatalf("run status = %d, want 0; stderr = %q", status, stderr.String())
			}
			if records := readTestLifecycleEvents(t, events); len(records) != 0 {
				t.Fatalf("events = %#v, want no initial or EOF transition", records)
			}
		})
	}
}

func TestRunEventsFDWriteSetupFailureRejectsBeforeReading(t *testing.T) {
	stdin := &trackingReader{}
	var stdout, stderr bytes.Buffer
	status := runWithIO(stdin, &stdout, &stderr, []string{
		"--events-fd", formatEventFD(^uintptr(0) >> 1),
		"--open", "duration:1h",
		"--close", "duration:1h",
		"open",
	})
	if status != 2 {
		t.Fatalf("run status = %d, want usage error; stderr = %q", status, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stdin.read {
		t.Fatal("startup FD validation consumed stdin")
	}
}

func TestRunEventsFDDisablesAfterModeChange(t *testing.T) {
	events := newTestEventSink(t)
	assertRunContinuesAfterEventFailure(t, events,
		func() { setTestEventNonblocking(t, events, false) },
		func() { setTestEventNonblocking(t, events, true) })
	assertTestLifecycleEvents(t, readTestLifecycleEvents(t, events), nil)
}

func TestRunEventsFDDisablesAfterConsumerDisconnect(t *testing.T) {
	events := newTestEventSink(t)
	assertRunContinuesAfterEventFailure(t, events,
		func() {
			if err := events.reader.Close(); err != nil {
				t.Fatal(err)
			}
		}, func() {})
}

func TestEventEmitterDisablesAfterConsumerDisconnect(t *testing.T) {
	events := newTestEventSink(t)
	emitter, err := newEventEmitterChecked(events.fd, io.Discard, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(emitter.close)
	if err := events.reader.Close(); err != nil {
		t.Fatal(err)
	}
	emitter.emit("stream-open")
	if !emitter.disabled {
		t.Fatal("consumer disconnect did not disable event output")
	}
	emitter.emit("stream-closed")
	if !emitter.disabled {
		t.Fatal("later emit re-enabled event output after consumer disconnect")
	}
}

func assertRunContinuesAfterEventFailure(t *testing.T, events *testEventSink, fail, recoverDestination func()) {
	t.Helper()
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { _ = input.Close(); _ = inputWriter.Close() })
	ready := make(chan struct{})
	close(ready)
	copyReady := make(chan struct{})
	startupReady := make(chan struct{})
	failureReady := make(chan struct{})
	recoveryReady := make(chan struct{})
	recovered := make(chan struct{})
	releaseFailure := closeOnce(failureReady)
	releaseRecovery := closeOnce(recovered)
	t.Cleanup(releaseFailure)
	t.Cleanup(releaseRecovery)
	armCalls := 0
	arm := func(event) (armedEvent, error) {
		armCalls++
		switch armCalls {
		case 1:
			// Startup validation has completed before the first event is armed.
			close(startupReady)
			<-failureReady
		case 3:
			// The first failed write must disable output even if the sink recovers.
			close(recoveryReady)
			<-recovered
		case 4:
			close(copyReady)
			return armedEvent{ch: make(chan struct{}), stop: func() {}}, nil
		}
		return armedEvent{ch: ready, stop: func() {}}, nil
	}
	var output bytes.Buffer
	diagnostics := &eventFailureDiagnostics{lockedBuffer: newLockedBuffer(), written: make(chan struct{}, 3)}
	done := make(chan int, 1)
	go func() {
		done <- runStateMachineWithArmer(input, &output, diagnostics, config{
			mode: modeBlock, initial: stateClosed,
			open: event{kind: eventDuration}, close: event{kind: eventDuration},
			eventsFD: events.fd, eventsFDSet: true,
		}, arm)
	}()
	for _, step := range []struct {
		ready   <-chan struct{}
		change  func()
		release func()
	}{
		{startupReady, fail, releaseFailure},
		{recoveryReady, recoverDestination, releaseRecovery},
	} {
		select {
		case <-step.ready:
		case <-time.After(time.Second):
			t.Fatal("event failure prevented subsequent transitions")
		}
		step.change()
		step.release()
	}
	select {
	case <-copyReady:
	case <-time.After(time.Second):
		t.Fatal("event failure prevented subsequent transitions")
	}
	inputDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(inputWriter, "forwarded after event failure")
		_ = inputWriter.Close()
		inputDone <- err
	}()
	select {
	case status := <-done:
		if status != 0 {
			t.Fatalf("run status = %d, want 0; stderr = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("event failure prevented normal stream completion")
	}
	select {
	case err := <-inputDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("normal stream completion did not consume input")
	}
	if got := output.String(); got != "forwarded after event failure" {
		t.Fatalf("stdout = %q, want forwarded input", got)
	}
	select {
	case <-diagnostics.written:
	case <-time.After(time.Second):
		t.Fatal("event failure did not warn")
	}
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("stderr = %q, want one events warning", diagnostics.String())
	}
}

type eventFailureDiagnostics struct {
	*lockedBuffer
	written chan struct{}
}

func (diagnostics *eventFailureDiagnostics) Write(p []byte) (int, error) {
	n, err := diagnostics.lockedBuffer.Write(p)
	diagnostics.written <- struct{}{}
	return n, err
}

func waitForTestEventWarning(t *testing.T, diagnostics *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(diagnostics.String(), "events disabled:") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for event warning; stderr = %q", diagnostics.String())
}

type blockingEventDiagnostics struct {
	started chan struct{}
	release chan struct{}
	once    chan struct{}
}

func newBlockingEventDiagnostics() *blockingEventDiagnostics {
	return &blockingEventDiagnostics{
		started: make(chan struct{}),
		release: make(chan struct{}),
		once:    make(chan struct{}, 1),
	}
}

func (w *blockingEventDiagnostics) Write(p []byte) (int, error) {
	select {
	case w.once <- struct{}{}:
		close(w.started)
	default:
	}
	<-w.release
	return len(p), nil
}

func (w *blockingEventDiagnostics) releaseWrite() {
	select {
	case <-w.release:
	default:
		close(w.release)
	}
}

func readTestLifecycleEvents(t *testing.T, sink *testEventSink) []testLifecycleEvent {
	t.Helper()
	if err := sink.writer.Close(); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(sink.reader)
	var records []testLifecycleEvent
	for {
		var record testLifecycleEvent
		err := decoder.Decode(&record)
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatalf("decode lifecycle event: %v", err)
		}
		if record.Event == "" {
			t.Fatalf("lifecycle event has no event name: %#v", record)
		}
		if timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp); err != nil {
			t.Fatalf("lifecycle timestamp %q is not RFC3339Nano: %v", record.Timestamp, err)
		} else if timestamp.Location() != time.UTC {
			t.Fatalf("lifecycle timestamp location = %v, want UTC", timestamp.Location())
		}
		records = append(records, record)
	}
}

func readTestLifecycleEvent(t *testing.T, reader *bufio.Reader) testLifecycleEvent {
	t.Helper()
	line := make(chan []byte, 1)
	go func() {
		data, err := reader.ReadBytes('\n')
		if err != nil {
			line <- nil
			return
		}
		line <- data
	}()
	select {
	case data := <-line:
		if data == nil {
			t.Fatal("event stream ended before transition event")
		}
		var record testLifecycleEvent
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatalf("decode lifecycle event: %v", err)
		}
		return record
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transition event")
		return testLifecycleEvent{}
	}
}

func assertTestLifecycleEvents(t *testing.T, records []testLifecycleEvent, want []string) {
	t.Helper()
	if len(records) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(records), len(want), records)
	}
	for index, event := range want {
		if got := records[index].Event; got != event {
			t.Errorf("event %d = %q, want %q", index, got, event)
		}
	}
}

type releaseErrorReader struct {
	release <-chan struct{}
	err     error
}

func (reader releaseErrorReader) Read([]byte) (int, error) {
	<-reader.release
	return 0, reader.err
}

type overlapDiagnosticWriter struct {
	active       int32
	firstStarted chan struct{}
	releaseCh    chan struct{}
	overlap      chan struct{}
	firstOnce    sync.Once
	overlapOnce  sync.Once
	mu           sync.Mutex
	data         bytes.Buffer
	written      chan struct{}
	finished     chan struct{}
	finishedOnce sync.Once
}

func newOverlapDiagnosticWriter() *overlapDiagnosticWriter {
	return &overlapDiagnosticWriter{
		firstStarted: make(chan struct{}),
		releaseCh:    make(chan struct{}),
		overlap:      make(chan struct{}),
		written:      make(chan struct{}, 4),
		finished:     make(chan struct{}),
	}
}

func (writer *overlapDiagnosticWriter) Write(p []byte) (int, error) {
	defer func() {
		writer.written <- struct{}{}
		writer.finishedOnce.Do(func() { close(writer.finished) })
	}()
	if atomic.AddInt32(&writer.active, 1) > 1 {
		writer.overlapOnce.Do(func() { close(writer.overlap) })
	}
	defer atomic.AddInt32(&writer.active, -1)
	writer.firstOnce.Do(func() {
		close(writer.firstStarted)
		<-writer.releaseCh
	})
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.data.Write(p)
}

func (writer *overlapDiagnosticWriter) release() {
	select {
	case <-writer.releaseCh:
	default:
		close(writer.releaseCh)
	}
}

func (writer *overlapDiagnosticWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.data.String()
}

func TestRunEventsFDStalledWarningDoesNotDelayExit(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "EOF", err: io.EOF, status: 0},
		{name: "primary I/O error", err: errors.New("copy failed"), status: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := newTestEventSink(t)
			diagnostics := newOverlapDiagnosticWriter()
			done := make(chan int, 1)
			exited := make(chan struct{})
			t.Cleanup(func() {
				diagnostics.release()
				select {
				case <-exited:
				case <-time.After(time.Second):
					t.Error("run goroutine did not finish during cleanup")
				}
				select {
				case <-diagnostics.finished:
				case <-time.After(time.Second):
					t.Error("warning goroutine did not finish during cleanup")
				}
			})
			ready := make(chan struct{})
			close(ready)
			calls := 0
			arm := func(event) (armedEvent, error) {
				calls++
				if calls == 1 {
					// Fail the first event after the startup descriptor check.
					_ = events.reader.Close()
					return armedEvent{ch: ready, stop: func() {}}, nil
				}
				return armedEvent{ch: make(chan struct{}), stop: func() {}}, nil
			}
			go func() {
				defer close(exited)
				done <- runStateMachineWithArmer(releaseErrorReader{
					release: diagnostics.firstStarted, err: test.err,
				}, io.Discard, diagnostics, config{
					mode: modeBlock, initial: stateClosed,
					open: event{kind: eventDuration}, close: event{kind: eventDuration},
					eventsFD: events.fd, eventsFDSet: true,
				}, arm)
			}()
			select {
			case status := <-done:
				if status != test.status {
					t.Fatalf("run status = %d, want %d", status, test.status)
				}
			case <-time.After(time.Second):
				t.Fatal("run waited for stalled event warning")
			}
			diagnostics.release()
			select {
			case <-diagnostics.written:
			case <-time.After(time.Second):
				t.Fatal("released warning did not finish")
			}
			select {
			case <-diagnostics.overlap:
				t.Fatal("event warning and primary diagnostic overlapped")
			default:
			}
			if text := diagnostics.String(); strings.Count(text, "events disabled:") != 1 || strings.Contains(text, "I/O error:") {
				t.Fatalf("diagnostics = %q, want only one event warning", text)
			}
		})
	}
}

func TestRunEventsFDPrimaryDiagnosticRemainsSynchronous(t *testing.T) {
	events := newTestEventSink(t)
	diagnostics := newOverlapDiagnosticWriter()
	done := make(chan int, 1)
	exited := make(chan struct{})
	t.Cleanup(func() {
		diagnostics.release()
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Error("run goroutine did not finish during cleanup")
		}
	})
	go func() {
		defer close(exited)
		done <- runWithIO(errorReader{err: errors.New("copy failed")}, io.Discard, diagnostics, []string{
			"--events-fd=" + formatEventFD(events.fd),
			"--open", "duration:1h", "--close", "duration:1h", "open",
		})
	}()
	select {
	case <-diagnostics.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("primary diagnostic did not start")
	}
	select {
	case <-done:
		t.Fatal("run did not wait for ordinary primary diagnostic")
	case <-time.After(100 * time.Millisecond):
	}
	diagnostics.release()
	select {
	case status := <-done:
		if status != 1 {
			t.Fatalf("run status = %d, want 1", status)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after primary diagnostic")
	}
	if text := diagnostics.String(); text != "sluice: I/O error: copy failed\n" {
		t.Fatalf("diagnostics = %q, want one synchronous primary diagnostic", text)
	}
}
