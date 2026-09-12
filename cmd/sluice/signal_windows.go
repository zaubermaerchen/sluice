//go:build windows

package main

// This file rejects POSIX signal events while leaving duration events portable.

import "fmt"

func validateEventPlatform(e event) error {
	if e.kind == eventSignal {
		return fmt.Errorf("signal events are not supported on windows")
	}
	return nil
}

func armSignal(e event) (armedEvent, error) {
	return armedEvent{}, validateEventPlatform(e)
}
