//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package main

// This file duplicates Unix event descriptors and performs no-wait writes.

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Unix descriptor syscalls use a signed 32-bit kernel descriptor even when
// Go's int and uintptr are wider.
const maxUnixEventFD = 1<<31 - 1

func eventFDPlatform() (bool, []string, string) {
	return true, []string{"FIFO", "socket"}, "O_NONBLOCK"
}

func unixEventFD(fd uintptr) (int, error) {
	if fd > maxUnixEventFD {
		return 0, errors.New("event file descriptor exceeds the Unix 32-bit range")
	}
	return int(fd), nil
}

func validateEventDescriptor(fd uintptr) error {
	fdInt, err := unixEventFD(fd)
	if err != nil {
		return err
	}
	flags, err := unix.FcntlInt(uintptr(fdInt), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fdInt, &stat); err != nil {
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

func duplicateEventFile(fd uintptr) (*os.File, error) {
	fdInt, err := unixEventFD(fd)
	if err != nil {
		return nil, err
	}
	if err := validateEventDescriptor(fd); err != nil {
		return nil, err
	}
	ownedFD, err := unix.Dup(fdInt)
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
		fd, err := unixEventFD(raw)
		if err != nil {
			writeErr = err
			return
		}
		if err := validateEventDescriptor(raw); err != nil {
			writeErr = err
			return
		}
		written, writeErr = unix.Write(fd, data)
	}); err != nil {
		return 0, err
	}
	return written, writeErr
}
