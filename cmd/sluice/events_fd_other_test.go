//go:build !windows && !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package main

import "testing"

func newTestEventSink(t *testing.T) *testEventSink {
	t.Helper()
	t.Skip("event file descriptors are unsupported on this platform")
	return nil
}
