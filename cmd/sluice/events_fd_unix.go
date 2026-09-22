//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file duplicates Unix event descriptors and performs no-wait writes.

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func validateEventDescriptor(fd int) error {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFIFO && stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return errors.New("event file descriptor must refer to a FIFO or socket")
	}
	if flags&unix.O_ACCMODE == unix.O_RDONLY {
		return errors.New("event file descriptor must be writable")
	}
	if flags&unix.O_NONBLOCK == 0 {
		return errors.New("event file descriptor must already be nonblocking")
	}
	return nil
}

func duplicateEventFile(fd int) (*os.File, error) {
	if err := validateEventDescriptor(fd); err != nil {
		return nil, err
	}
	ownedFD, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	closeOwnedFD := true
	defer func() {
		if closeOwnedFD {
			_ = unix.Close(ownedFD)
		}
	}()

	file := os.NewFile(uintptr(ownedFD), "sluice events")
	if file == nil {
		return nil, fmt.Errorf("invalid duplicated file descriptor %d", ownedFD)
	}
	unix.CloseOnExec(ownedFD)
	closeOwnedFD = false
	return file, nil
}

func writeEvent(file *os.File, data []byte) (int, error) {
	connection, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var written int
	var writeErr error
	if err := connection.Control(func(raw uintptr) {
		fd := int(raw)
		if err := validateEventDescriptor(fd); err != nil {
			writeErr = err
			return
		}
		written, writeErr = unix.Write(fd, data)
	}); err != nil {
		return 0, err
	}
	return written, writeErr
}
