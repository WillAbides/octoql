//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package schema

import (
	"errors"
	"io/fs"
)

func verifyJournalOwner(_ string, _ fs.FileInfo) error {
	return errors.New("secure journal ownership checks are unsupported on this platform")
}

func verifyJournalParentOwner(_ string, _ fs.FileInfo) error {
	return errors.New("secure journal parent ownership checks are unsupported on this platform")
}
