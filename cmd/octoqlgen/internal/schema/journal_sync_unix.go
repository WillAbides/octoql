//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package schema

import (
	"errors"
	"os"
)

func syncDirectory(path string) (err error) {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, directory.Close())
	}()
	err = directory.Sync()
	return err
}
