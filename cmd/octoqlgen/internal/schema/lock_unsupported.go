//go:build !(aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || windows)

package schema

import (
	"errors"
	"os"
)

func lockOSFile(*os.File, bool) (func() error, error) {
	return nil, errors.New("operating system file locking is not supported")
}

func isLockContention(error) bool {
	return false
}
