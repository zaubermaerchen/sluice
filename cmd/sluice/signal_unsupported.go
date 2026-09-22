//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package main

// This file rejects POSIX signal events on platforms without USR1/USR2 support.

import "fmt"

const signalEventsSupported = false

func validateEventPlatform(e event) error {
	if e.kind == eventSignal {
		return fmt.Errorf("signal events are not supported on this platform")
	}
	return nil
}

func armSignal(e event) (armedEvent, error) {
	return armedEvent{}, validateEventPlatform(e)
}
