//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package main

// This file keeps signal integration tests portable on unsupported platforms.

import "errors"

func testSignalsSupported() bool {
	return false
}

func sendTestSignal(string) error {
	return errors.New("signals are not supported on this platform")
}
