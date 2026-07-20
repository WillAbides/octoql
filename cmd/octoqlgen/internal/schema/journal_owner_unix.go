//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package schema

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func verifyJournalOwner(_ string, info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("journal owner metadata is unavailable")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("journal is owned by uid %d, not the current user", stat.Uid)
	}
	return nil
}

func verifyJournalParentOwner(_ string, info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("journal owner metadata is unavailable")
	}
	if stat.Uid == uint32(os.Getuid()) || stat.Uid == 0 {
		return nil
	}
	return fmt.Errorf("journal parent is owned by uid %d, not the current user or root", stat.Uid)
}
