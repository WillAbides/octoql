//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package schema

import "errors"

func syncFile(string) error {
	return errors.New("file synchronization is unsupported on this platform")
}
