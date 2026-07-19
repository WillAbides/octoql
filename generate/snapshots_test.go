// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package generate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
)

func TestStandaloneSnapshotRequiresUpdate(t *testing.T) {
	snapshotDir := t.TempDir()
	command := exec.Command("go", "test", "-run", "^TestStandaloneSnapshotRequiresUpdateHelper$", ".")
	command.Env = append(
		os.Environ(),
		"OCTOQL_TEST_MISSING_SNAPSHOT_DIR="+snapshotDir,
		"UPDATE_SNAPS=",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("go test unexpectedly succeeded:\n%s", output)
	}

	snapshot := filepath.Join(snapshotDir, "missing_1.snap.txt")
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("ordinary snapshot run created %s: %v", snapshot, err)
	}
}

func TestStandaloneSnapshotRequiresUpdateHelper(t *testing.T) {
	snapshotDir := os.Getenv("OCTOQL_TEST_MISSING_SNAPSHOT_DIR")
	if snapshotDir == "" {
		t.Skip("helper process only")
	}

	snaps.WithConfig(
		snaps.Dir(snapshotDir),
		snaps.Filename("missing"),
		snaps.Ext(".txt"),
		snaps.Raw(),
		snaps.Update(snapshotUpdateEnabled()),
	).MatchStandaloneSnapshot(t, "missing")
}

func TestNormalizeSnapshotName(t *testing.T) {
	tests := map[string]string{
		"TestGenerate/Repository.graphql/generated.go": "TestGenerate_Repository.graphql_generated.go",
		`TestGenerate\Repository.graphql\generated.go`: "TestGenerate_Repository.graphql_generated.go",
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := normalizeSnapshotName(input); got != want {
				t.Errorf("normalizeSnapshotName(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func snapshotUpdateEnabled() bool {
	return os.Getenv("UPDATE_SNAPS") == "true"
}

func normalizeSnapshotName(name string) string {
	return strings.NewReplacer("/", "_", `\`, "_").Replace(name)
}
