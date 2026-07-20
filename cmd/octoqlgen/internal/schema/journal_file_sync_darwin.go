//go:build darwin

package schema

import (
	"errors"
	"os"
	"syscall"
)

func syncFile(path string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	_, _, errno := syscall.Syscall(
		syscall.SYS_FCNTL,
		file.Fd(),
		uintptr(syscall.F_FULLFSYNC),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
