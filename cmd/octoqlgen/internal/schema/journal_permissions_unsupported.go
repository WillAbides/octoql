//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package schema

import "io/fs"

func journalModeIsPrivate(_ fs.FileInfo) bool {
	return true
}

func journalParentModeIsSecure(_ fs.FileInfo) bool {
	return true
}
