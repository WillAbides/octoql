//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package schema

import "errors"

func secureJournalDirectory(string) error {
	return errors.New("secure journal directory permissions are unsupported on this platform")
}

func secureJournalFile(string) error {
	return errors.New("secure journal file permissions are unsupported on this platform")
}
