//go:build windows

package main

// This file verifies Windows event-pipe validation and no-wait writes.

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func newTestEventSink(t *testing.T) *testEventSink {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fd := writer.Fd()
	handle := windows.Handle(fd)
	var mode uint32
	if err := windows.GetNamedPipeHandleState(handle, &mode, nil, nil, nil, nil, 0); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatal(err)
	}
	nowait := mode | windows.PIPE_NOWAIT
	if err := windows.SetNamedPipeHandleState(handle, &nowait, nil, nil); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatal(err)
	}
	sink := &testEventSink{reader: reader, writer: writer, fd: fd}
	t.Cleanup(sink.close)
	return sink
}

func TestEventEmitterClosesOnlyOwnedWindowsHandle(t *testing.T) {
	events := newTestEventSink(t)
	emitter := newEventEmitter(events.fd, ioDiscardLocked{})
	if emitter == nil {
		t.Fatal("newEventEmitter returned nil")
	}
	emitter.close()
	if _, err := events.writer.Stat(); err != nil {
		t.Fatalf("closing emitter closed caller handle: %v", err)
	}
}

func TestRunEventsFDRejectsWindowsRegularFileBeforeReading(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "sluice-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stdin := &trackingReader{}
	var stdout, stderr bytes.Buffer
	status := runWithIO(stdin, &stdout, &stderr, []string{
		"--events-fd", strconv.FormatUint(uint64(file.Fd()), 10),
		"--open", "duration:1h",
		"--close", "duration:1h",
		"open",
	})
	if status != 2 {
		t.Fatalf("run status = %d, want usage error; stderr = %q", status, stderr.String())
	}
	if stdin.read {
		t.Fatal("startup FD validation consumed stdin")
	}
	if !strings.Contains(stderr.String(), "named pipe") {
		t.Fatalf("stderr = %q, want named-pipe validation diagnostic", stderr.String())
	}
}

func TestRunEventsFDRejectsBlockingWindowsPipeBeforeReading(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	stdin := &trackingReader{}
	var stdout, stderr bytes.Buffer
	status := runWithIO(stdin, &stdout, &stderr, []string{
		"--events-fd", strconv.FormatUint(uint64(writer.Fd()), 10),
		"--open", "duration:1h",
		"--close", "duration:1h",
		"open",
	})
	if status != 2 {
		t.Fatalf("run status = %d, want usage error; stderr = %q", status, stderr.String())
	}
	if stdin.read {
		t.Fatal("startup FD validation consumed stdin")
	}
	if !strings.Contains(stderr.String(), "PIPE_NOWAIT") {
		t.Fatalf("stderr = %q, want PIPE_NOWAIT validation diagnostic", stderr.String())
	}
}

func TestRunEventsFDRejectsReadOnlyWindowsPipeBeforeReading(t *testing.T) {
	name, err := windows.UTF16PtrFromString(`\\.\pipe\sluice-test-` + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND, windows.PIPE_TYPE_BYTE|windows.PIPE_NOWAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(reader)
	if mode := windowsEventPipeMode(t, reader); mode&windows.PIPE_NOWAIT == 0 {
		t.Fatalf("read-only pipe mode = %#x, want PIPE_NOWAIT", mode)
	}

	stdin := &trackingReader{}
	var stdout, stderr bytes.Buffer
	status := runWithIO(stdin, &stdout, &stderr, []string{
		"--events-fd", formatEventFD(uintptr(reader)),
		"--open", "duration:1h",
		"--close", "duration:1h",
		"open",
	})
	if status != 2 {
		t.Fatalf("run status = %d, want usage error; stderr = %q", status, stderr.String())
	}
	if stdin.read || stdout.Len() != 0 {
		t.Fatal("startup FD validation processed stdin")
	}
	if !strings.Contains(stderr.String(), "writable") {
		t.Fatalf("stderr = %q, want writable-pipe validation diagnostic", stderr.String())
	}
}

func TestRunEventsFDDoesNotWaitForFullWindowsConsumer(t *testing.T) {
	events := newTestEventSink(t)
	handle := windows.Handle(events.fd)
	fillWindowsEventPipe(t, handle)
	originalMode := windowsEventPipeMode(t, handle)
	var output bytes.Buffer
	diagnostics := newLockedBuffer()
	status := make(chan int, 1)
	go func() {
		status <- runWithIO(strings.NewReader("input"), &output, diagnostics, []string{
			"--events-fd", formatEventFD(events.fd),
			"--open", "duration:0s",
			"--close", "duration:1h",
			"closed",
		})
	}()
	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("run status = %d, want 0; diagnostics = %q", got, diagnostics.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run waited for a full Windows event consumer")
	}
	if output.String() != "input" {
		t.Fatalf("stdout = %q, want input", output.String())
	}
	waitForTestEventWarning(t, diagnostics)
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one events warning", diagnostics.String())
	}
	if got := windowsEventPipeMode(t, handle); got != originalMode {
		t.Fatalf("event pipe mode = %#x, want %#x", got, originalMode)
	}
}

func windowsEventPipeMode(t *testing.T, handle windows.Handle) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetNamedPipeHandleState(handle, &mode, nil, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	return mode
}

func fillWindowsEventPipe(t *testing.T, handle windows.Handle) {
	t.Helper()
	buffer := make([]byte, 4096)
	for total := 0; total < 16<<20; {
		var written uint32
		err := windows.WriteFile(handle, buffer, &written, nil)
		total += int(written)
		if err != nil || written == 0 {
			return
		}
	}
	t.Fatalf("filled event pipe without making it unavailable")
}
