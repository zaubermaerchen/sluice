//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file isolates startup signal disposition checks from the test process.

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSignalPreparationLifecycle(t *testing.T) {
	for _, scenario := range []string{
		"validation", "signal-startup", "duration-startup", "help", "version", "describe",
		"configuration-error", "event-fd-error", "event-setup-error", "before-arm", "signal-transitions", "event-transitions",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSignalPreparationChild$", "-test.v")
			cmd.Env = append(os.Environ(), "SLUICE_SIGNAL_PREPARATION_TEST="+scenario)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated %s: %v\n%s", scenario, err, output)
			}
		})
	}
}

func TestSignalPreparationChild(t *testing.T) {
	scenario := os.Getenv("SLUICE_SIGNAL_PREPARATION_TEST")
	if scenario == "" {
		t.Skip("only run in a child process")
	}
	// Establish and verify the child's default disposition before preparation.
	signal.Reset(syscall.SIGUSR1, syscall.SIGUSR2)
	assertIgnored := func(want bool) {
		t.Helper()
		for _, sig := range []os.Signal{syscall.SIGUSR1, syscall.SIGUSR2} {
			if got := signal.Ignored(sig); got != want {
				t.Errorf("signal.Ignored(%v) = %v, want %v", sig, got, want)
			}
		}
	}
	assertIgnored(false)

	switch scenario {
	case "validation":
		for _, e := range []event{{kind: eventSignal, signal: "USR1"}, {kind: eventSignal, signal: "USR2"}, {kind: eventDuration}} {
			if err := validateEventPlatform(e); err != nil {
				t.Fatal(err)
			}
			assertIgnored(false)
		}
		if _, err := parseConfig(singleValue{value: "block"}, singleValue{value: "signal:USR1", set: true}, singleValue{value: "signal:USR2", set: true}, []string{"open"}); err != nil {
			t.Fatal(err)
		}
		assertIgnored(false)
	case "signal-startup", "duration-startup":
		open := "duration:1h"
		wantIgnored := scenario == "signal-startup"
		if wantIgnored {
			open = "signal:USR1"
		}
		var stdout strings.Builder
		stdin := &signalPreparationReader{beforeRead: func() {
			assertIgnored(wantIgnored)
			if wantIgnored {
				// Neither signal is armed while OPEN waits for its duration close.
				// They must leave the primary stream and state unchanged.
				for _, sig := range []syscall.Signal{syscall.SIGUSR1, syscall.SIGUSR2} {
					if err := syscall.Kill(os.Getpid(), sig); err != nil {
						t.Error(err)
					}
				}
			}
		}, source: strings.NewReader("primary stream")}
		if code := runWithIO(stdin, &stdout, io.Discard, []string{"--open", open, "--close", "duration:1h", "open"}); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if stdout.String() != "primary stream" {
			t.Fatalf("stdout = %q", stdout.String())
		}
		assertIgnored(wantIgnored)
	case "before-arm":
		cfg := config{open: event{kind: eventSignal, signal: "USR1"}, close: event{kind: eventDuration, duration: time.Hour}, initial: stateOpen}
		armed := false
		code := runStateMachineWithArmer(strings.NewReader(""), io.Discard, io.Discard, cfg, func(event) (armedEvent, error) {
			armed = true
			assertIgnored(true)
			return armedEvent{}, errors.New("stop after observing preparation")
		})
		if !armed || code != 1 {
			t.Fatalf("armed = %v, exit code = %d", armed, code)
		}
	case "event-setup-error":
		cfg := config{open: event{kind: eventSignal, signal: "USR1"}, close: event{kind: eventSignal, signal: "USR2"}, eventsFDSet: true, eventsFD: 2}
		if code := runStateMachineWithArmer(strings.NewReader(""), io.Discard, io.Discard, cfg, func(event) (armedEvent, error) {
			t.Fatal("armed after event emitter setup error")
			return armedEvent{}, nil
		}); code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		assertIgnored(false)
	case "signal-transitions":
		TestRun_SignalTransitionBlocksUntilReopened(t)
	case "event-transitions":
		TestRunEventsFDEmitsSignalTransitions(t)
	default:
		args := []string{"--" + scenario}
		wantCode := 0
		if scenario == "configuration-error" {
			args = []string{"--open", "signal:USR1", "--close", "signal:USR2", "invalid"}
			wantCode = 2
		}
		if scenario == "event-fd-error" {
			// Reserve and close a real descriptor to avoid assuming fd 3 is unused.
			file, err := os.CreateTemp(t.TempDir(), "closed-fd")
			if err != nil {
				t.Fatal(err)
			}
			fd := file.Fd()
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			args = []string{"--open", "signal:USR1", "--close", "signal:USR2", "--events-fd", formatEventFD(fd), "open"}
			wantCode = 2
		}
		if code := runWithIO(strings.NewReader(""), io.Discard, io.Discard, args); code != wantCode {
			t.Fatalf("exit code = %d, want %d", code, wantCode)
		}
		assertIgnored(false)
	}
}

type signalPreparationReader struct {
	beforeRead func()
	source     io.Reader
}

func (r *signalPreparationReader) Read(p []byte) (int, error) {
	r.beforeRead()
	return r.source.Read(p)
}
