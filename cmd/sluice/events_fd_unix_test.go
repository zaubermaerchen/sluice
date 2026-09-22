//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file verifies Unix event descriptor ownership and no-wait writes.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func newTestEventSink(t *testing.T) *testEventSink {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fd := writer.Fd()
	if err := unix.SetNonblock(int(fd), true); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatal(err)
	}
	sink := &testEventSink{reader: reader, writer: writer, fd: fd}
	t.Cleanup(sink.close)
	return sink
}

func TestEventEmitterClosesOnlyOwnedDescriptor(t *testing.T) {
	events := newTestEventSink(t)
	emitter := newEventEmitter(events.fd, ioDiscardLocked{})
	if emitter == nil {
		t.Fatal("newEventEmitter returned nil")
	}
	ownedFD := int(emitter.file.Fd())
	emitter.close()

	if _, err := unix.FcntlInt(uintptr(ownedFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("owned descriptor error = %v, want EBADF", err)
	}
	if _, err := events.writer.Stat(); err != nil {
		t.Fatalf("closing emitter closed caller descriptor: %v", err)
	}
}

func TestEventEmitterProtectsAndDuplicatesCallerDescriptor(t *testing.T) {
	events := newTestEventSink(t)
	originalFD := events.fd
	if _, err := unix.FcntlInt(originalFD, unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}

	emitter := newEventEmitter(originalFD, ioDiscardLocked{})
	if emitter == nil {
		t.Fatal("newEventEmitter returned nil")
	}
	t.Cleanup(emitter.close)
	ownedFD := int(emitter.file.Fd())
	if uintptr(ownedFD) == originalFD {
		t.Fatalf("owned descriptor = %d, want duplicate of %d", ownedFD, originalFD)
	}
	ownedFlags, err := unix.FcntlInt(uintptr(ownedFD), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ownedFlags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("owned descriptor flags = %#x, want FD_CLOEXEC", ownedFlags)
	}
	callerFlags, err := unix.FcntlInt(uintptr(originalFD), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if callerFlags&unix.FD_CLOEXEC != 0 {
		t.Fatalf("caller descriptor flags = %#x, want original non-CLOEXEC flags", callerFlags)
	}
}

func TestRunEventsFDRejectsUnixRegularFileBeforeReading(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "sluice-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	assertEventFDStartupRejected(t, file.Fd(), "FIFO or socket")
}

func TestRunEventsFDRejectsUnixBlockingPipeBeforeReading(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	assertEventFDStartupRejected(t, writer.Fd(), "nonblocking")
}

func TestRunEventsFDRejectsUnixReadOnlyPipeBeforeReading(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err := unix.SetNonblock(int(reader.Fd()), true); err != nil {
		t.Fatal(err)
	}
	assertEventFDStartupRejected(t, reader.Fd(), "writable")
}

func TestRunEventsFDRejectsUnixDescriptorThatWouldTruncate(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("Unix descriptor truncation alias requires a 64-bit uintptr")
	}
	events := newTestEventSink(t)
	base := uintptr(1)
	highFD := (base << 32) | events.fd
	stdin := &trackingReader{}
	var stdout, stderr bytes.Buffer
	status := runWithIO(stdin, &stdout, &stderr, []string{
		"--events-fd", strconv.FormatUint(uint64(highFD), 10),
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
	if !strings.Contains(stderr.String(), "32-bit range") {
		t.Fatalf("stderr = %q, want Unix descriptor range diagnostic", stderr.String())
	}
}

func assertEventFDStartupRejected(t *testing.T, fd uintptr, wantDiagnostic string) {
	t.Helper()
	stdin := &trackingReader{}
	var stdout, stderr bytes.Buffer
	status := runWithIO(stdin, &stdout, &stderr, []string{
		"--events-fd", strconv.FormatUint(uint64(fd), 10),
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
	if !strings.Contains(stderr.String(), wantDiagnostic) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantDiagnostic)
	}
}

func TestRunEventsFDDoesNotWaitForFullConsumer(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()

	writeFD := writeEvents.Fd()
	if err := unix.SetNonblock(int(writeFD), true); err != nil {
		t.Fatal(err)
	}
	originalFlags, err := unix.FcntlInt(writeFD, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	filler, err := unix.Dup(int(writeFD))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(filler)
	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Write(filler, buffer); err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	diagnostics := newLockedBuffer()
	status := make(chan int, 1)
	go func() {
		status <- runWithIO(strings.NewReader("input"), &output, diagnostics, []string{
			"--events-fd", strconv.FormatUint(uint64(writeFD), 10),
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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("run waited for an event consumer that could not accept a write")
	}
	if got := output.String(); got != "input" {
		t.Fatalf("stdout = %q, want input", got)
	}
	waitForTestEventWarning(t, diagnostics)
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one events warning", diagnostics.String())
	}
	flags, err := unix.FcntlInt(writeFD, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK != originalFlags&unix.O_NONBLOCK {
		t.Fatalf("caller descriptor blocking mode = %#x, want %#x", flags&unix.O_NONBLOCK, originalFlags&unix.O_NONBLOCK)
	}
}

func TestEventEmitterWarnsBeforeClose(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()
	readFD := int(readEvents.Fd())
	if err := unix.SetNonblock(readFD, true); err != nil {
		t.Fatal(err)
	}
	writeFD := writeEvents.Fd()
	if err := unix.SetNonblock(int(writeFD), true); err != nil {
		t.Fatal(err)
	}
	filler, err := unix.Dup(int(writeFD))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(filler)
	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Write(filler, buffer); err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			t.Fatal(err)
		}
	}

	diagnostics := newLockedBuffer()
	emitter := newEventEmitter(writeFD, diagnostics)
	if emitter == nil {
		t.Fatal("newEventEmitter returned nil")
	}
	defer emitter.close()
	emitter.emit("stream-open")
	drainEventPipe(t, readFD)
	emitter.emit("stream-closed")
	var eventByte [1]byte
	if n, err := unix.Read(readFD, eventByte[:]); err == nil {
		t.Fatalf("second emit wrote %d bytes after events were disabled", n)
	} else if err != unix.EAGAIN && err != unix.EWOULDBLOCK {
		t.Fatalf("second emit read = (%d, %v), want EAGAIN", n, err)
	}
	waitForTestEventWarning(t, diagnostics)
	if got := strings.Count(diagnostics.String(), "events disabled:"); got != 1 {
		t.Fatalf("diagnostics = %q, want one events warning", diagnostics.String())
	}
}

func drainEventPipe(t *testing.T, fd int) {
	t.Helper()
	buffer := make([]byte, 4096)
	for {
		n, err := unix.Read(fd, buffer)
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal("event pipe closed while draining")
		}
	}
}

func TestRunEventsFDBlockedDiagnosticsDoNotStallStream(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()
	writeFD := writeEvents.Fd()
	if err := unix.SetNonblock(int(writeFD), true); err != nil {
		t.Fatal(err)
	}
	filler, err := unix.Dup(int(writeFD))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(filler)
	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Write(filler, buffer); err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			t.Fatal(err)
		}
	}
	diagnostics := newBlockingEventDiagnostics()
	defer diagnostics.releaseWrite()
	status := make(chan int, 1)
	go func() {
		status <- runWithIO(strings.NewReader("input"), io.Discard, diagnostics, []string{
			"--events-fd", strconv.FormatUint(uint64(writeFD), 10),
			"--open", "duration:0s",
			"--close", "duration:1h",
			"closed",
		})
	}()
	select {
	case got := <-status:
		if got != 0 {
			t.Fatalf("run status = %d, want 0", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream path waited for a blocked diagnostics writer")
	}
}

func TestRunEventsFDWarningIsSerializedWithCopyDiagnostic(t *testing.T) {
	readEvents, writeEvents, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEvents.Close()
	defer writeEvents.Close()
	writeFD := writeEvents.Fd()
	if err := unix.SetNonblock(int(writeFD), true); err != nil {
		t.Fatal(err)
	}
	filler, err := unix.Dup(int(writeFD))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(filler)
	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Write(filler, buffer); err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			t.Fatal(err)
		}
	}

	diagnostics := newOverlapDiagnosticWriter()
	copyRelease := make(chan struct{})
	t.Cleanup(func() {
		diagnostics.release()
		select {
		case <-copyRelease:
		default:
			close(copyRelease)
		}
	})
	errorRendered := make(chan struct{})
	done := make(chan int, 1)
	go func() {
		done <- runWithIO(releaseErrorReader{release: copyRelease, err: diagnosticsError{rendered: errorRendered}}, io.Discard, diagnostics, []string{
			"--events-fd", strconv.FormatUint(uint64(writeFD), 10),
			"--open", "duration:0s",
			"--close", "duration:1h",
			"closed",
		})
	}()
	select {
	case <-diagnostics.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("event warning did not start")
	}
	close(copyRelease)
	select {
	case <-errorRendered:
	case <-time.After(time.Second):
		t.Fatal("copy diagnostic was not formatted")
	}
	select {
	case <-diagnostics.overlap:
		t.Fatal("event warning and copy diagnostic wrote stderr concurrently")
	case <-time.After(100 * time.Millisecond):
	}
	diagnostics.release()
	select {
	case status := <-done:
		if status != 1 {
			t.Fatalf("run status = %d, want copy error; diagnostics = %q", status, diagnostics.String())
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after releasing diagnostics")
	}
	if !strings.Contains(diagnostics.String(), "I/O error: copy failed") {
		t.Fatalf("diagnostics = %q, want copy error", diagnostics.String())
	}
}

type diagnosticsError struct {
	rendered chan struct{}
}

func (err diagnosticsError) Error() string {
	select {
	case <-err.rendered:
	default:
		close(err.rendered)
	}
	return "copy failed"
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
}

func newOverlapDiagnosticWriter() *overlapDiagnosticWriter {
	return &overlapDiagnosticWriter{
		firstStarted: make(chan struct{}),
		releaseCh:    make(chan struct{}),
		overlap:      make(chan struct{}),
	}
}

func (writer *overlapDiagnosticWriter) Write(p []byte) (int, error) {
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
