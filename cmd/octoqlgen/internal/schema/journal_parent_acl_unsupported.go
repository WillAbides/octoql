//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package schema

import "errors"

func verifyJournalParentACL(string) error {
	return errors.New("secure journal parent ACL verification is unsupported on this platform")
}
