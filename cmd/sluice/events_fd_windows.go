//go:build windows

package main

// This file duplicates Windows event handles and performs no-wait pipe writes.

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validateEventDescriptor(fd int) error {
	handle := windows.Handle(fd)
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return err
	}
	if fileType != windows.FILE_TYPE_PIPE {
		return errors.New("event file descriptor must refer to a named pipe")
	}
	var mode uint32
	if err := windows.GetNamedPipeHandleState(handle, &mode, nil, nil, nil, nil, 0); err != nil {
		return err
	}
	if mode&windows.PIPE_NOWAIT == 0 {
		return errors.New("event pipe must already use PIPE_NOWAIT mode")
	}
	return nil
}

func duplicateEventFile(fd int) (*os.File, error) {
	if err := validateEventDescriptor(fd); err != nil {
		return nil, err
	}
	var owned windows.Handle
	if err := windows.DuplicateHandle(
		windows.CurrentProcess(),
		windows.Handle(fd),
		windows.CurrentProcess(),
		&owned,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	closeOwnedHandle := true
	defer func() {
		if closeOwnedHandle {
			_ = windows.CloseHandle(owned)
		}
	}()

	file := os.NewFile(uintptr(owned), "sluice events")
	if file == nil {
		return nil, fmt.Errorf("invalid duplicated event handle %v", owned)
	}
	if err := windows.SetHandleInformation(owned, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = file.Close()
		closeOwnedHandle = false
		return nil, fmt.Errorf("protect duplicated event handle: %w", err)
	}
	closeOwnedHandle = false
	return file, nil
}

func writeEvent(file *os.File, data []byte) (int, error) {
	connection, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var written uint32
	var writeErr error
	if err := connection.Control(func(raw uintptr) {
		handle := windows.Handle(raw)
		if err := validateEventDescriptor(int(handle)); err != nil {
			writeErr = err
			return
		}
		writeErr = windows.WriteFile(handle, data, &written, nil)
	}); err != nil {
		return 0, err
	}
	return int(written), writeErr
}
