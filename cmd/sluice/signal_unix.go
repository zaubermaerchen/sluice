//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file arms the POSIX signals supported by the sluice event syntax.

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var ignoredSignals sync.Once

func validateEventPlatform(e event) error {
	if e.kind == eventSignal {
		ignoredSignals.Do(func() {
			// Signals that are not currently armed must be harmless same-state events.
			signal.Ignore(syscall.SIGUSR1, syscall.SIGUSR2)
		})
	}
	return nil
}

func armSignal(e event) (armedEvent, error) {
	var sig os.Signal
	switch e.signal {
	case "USR1":
		sig = syscall.SIGUSR1
	case "USR2":
		sig = syscall.SIGUSR2
	default:
		return armedEvent{}, fmt.Errorf("unsupported signal %q", e.signal)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, sig)
	done := make(chan struct{})
	eventCh := make(chan struct{}, 1)
	go func() {
		select {
		case <-signals:
			eventCh <- struct{}{}
		case <-done:
		}
	}()

	var stopOnce sync.Once
	return armedEvent{
		ch: eventCh,
		stop: func() {
			stopOnce.Do(func() {
				signal.Stop(signals)
				close(done)
			})
		},
	}, nil
}
