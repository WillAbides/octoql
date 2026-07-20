//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package schema

func verifyJournalParentACL(path string) error {
	return verifyJournalACL(path)
}
