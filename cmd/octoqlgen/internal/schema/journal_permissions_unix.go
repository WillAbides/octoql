//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package schema

import "io/fs"

func journalModeIsPrivate(info fs.FileInfo) bool {
	return info.Mode().Perm()&0o077 == 0
}

func journalParentModeIsSecure(info fs.FileInfo) bool {
	return info.Mode().Perm()&0o022 == 0
}
