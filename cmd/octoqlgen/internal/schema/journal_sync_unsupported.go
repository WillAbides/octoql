//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package schema

import "errors"

func syncDirectory(string) error {
	return errors.New("directory synchronization is unsupported on this platform")
}
