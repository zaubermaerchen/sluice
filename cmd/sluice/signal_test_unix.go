//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file provides the process-signal hooks used by the Unix integration tests.

import "syscall"

func testSignalsSupported() bool {
	return true
}

func sendTestSignal(name string) error {
	var sig syscall.Signal
	switch name {
	case "USR1":
		sig = syscall.SIGUSR1
	case "USR2":
		sig = syscall.SIGUSR2
	default:
		return syscall.EINVAL
	}
	return syscall.Kill(syscall.Getpid(), sig)
}
