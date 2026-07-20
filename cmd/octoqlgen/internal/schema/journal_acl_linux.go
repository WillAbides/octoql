//go:build linux

package schema

import (
	"errors"

	"golang.org/x/sys/unix"
)

func verifyJournalACL(path string) error {
	err := rejectJournalACL(path, "system.posix_acl_access")
	if err != nil {
		return err
	}
	return rejectJournalACL(path, "system.posix_acl_default")
}

func rejectJournalACL(path, name string) error {
	_, err := unix.Getxattr(path, name, nil)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	if err == nil {
		return errors.New("POSIX ACL is not allowed")
	}
	return err
}
