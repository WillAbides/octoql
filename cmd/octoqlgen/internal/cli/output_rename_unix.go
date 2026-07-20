//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package cli

import "os"

func renameOutputAtomically(source, destination string) error {
	return os.Rename(source, destination)
}
