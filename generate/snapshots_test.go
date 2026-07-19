// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package generate

import (
	"fmt"
	"os"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
)

func TestMain(m *testing.M) {
	exitCode := m.Run()
	dirty, err := snaps.Clean(m, snaps.CleanOpts{Sort: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "clean snapshots:", err)
		exitCode = 1
	}
	if dirty {
		exitCode = 1
	}
	os.Exit(exitCode)
}
