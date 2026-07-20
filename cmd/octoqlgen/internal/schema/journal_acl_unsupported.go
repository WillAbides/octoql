//go:build !darwin && !linux && !windows

package schema

import "errors"

func verifyJournalACL(string) error {
	return errors.New("secure journal ACL verification is unsupported on this platform")
}
