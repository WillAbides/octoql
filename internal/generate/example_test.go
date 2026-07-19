package generate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunExample(t *testing.T) {
	if _, ok := os.LookupEnv("GITHUB_TOKEN"); !ok {
		t.Skip("requires GITHUB_TOKEN to be set")
	}

	cmd := exec.CommandContext(t.Context(), "go", "run", "./example", "benjaminjkraft")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Error(err)
	}

	got := strings.TrimSpace(string(out))
	want := "benjaminjkraft is Ben Kraft created on 2009-08-03"
	if got != want {
		t.Errorf("output incorrect\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller non-ok")
	}

	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	_, err := os.Stat(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(fmt.Errorf("doesn't look like repo root: %v", err))
	}
	return root
}
