//go:build darwin && !cgo

package schema

import "errors"

func verifyJournalACL(string) error {
	return errors.New("secure journal ACL verification requires cgo on macOS")
}
