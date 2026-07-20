//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package schema

func secureJournalDirectory(string) error {
	return nil
}

func secureJournalFile(string) error {
	return nil
}
