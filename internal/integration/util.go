package integration

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/willabides/octoql/internal/generate"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller non-ok")
	}

	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(fmt.Errorf("doesn't look like repo root: %v", err))
	}
	return root
}

func runGenerateTest(t *testing.T, config *generate.Config) {
	t.Helper()

	err := config.ValidateAndFillDefaults(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	generated, err := generate.Generate(config)
	if err != nil {
		t.Fatal(err)
	}

	for filename, content := range generated {
		expectedContent, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(content, expectedContent) {
			t.Errorf("mismatch in %s", filename)
			if testing.Verbose() {
				t.Errorf("got:\n%s\nwant:\n%s\n", content, expectedContent)
			}
			if os.Getenv("UPDATE_SNAPS") == "true" {
				err = os.WriteFile(filename, content, 0o644)
				if err != nil {
					t.Errorf("unable to update generated file %s: %v", filename, err)
				} else {
					t.Errorf("updated generated file for %s", filename)
				}
			}
		}
	}
}
