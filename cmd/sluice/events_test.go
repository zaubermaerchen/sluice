package main

// This file verifies optional lifecycle events and their CLI integration.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testLifecycleEvent struct {
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
}

type testEventSink struct {
	reader *os.File
	writer *os.File
	fd     int
}

type ioDiscardLocked struct{}

func (ioDiscardLocked) Write(p []byte) (int, error) { return len(p), nil }

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
		want  int
	}{
		{value: "3", want: 3},
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
		"--events-fd=" + strconv.Itoa(events.fd),
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
		"--events-fd", strconv.Itoa(events.fd),
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
				strconv.Itoa(events.fd),
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
		"--events-fd", strconv.Itoa(int(^uint(0) >> 1)),
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
