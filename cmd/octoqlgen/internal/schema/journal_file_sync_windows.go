//go:build windows

package schema

import (
	"errors"
	"os"
)

func syncFile(path string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	return file.Sync()
}
