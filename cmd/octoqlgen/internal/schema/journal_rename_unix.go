//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package schema

import "os"

func renameFileAtomically(source, destination string) error {
	return os.Rename(source, destination)
}
