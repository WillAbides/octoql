package generate_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/willabides/octoql/generate"
)

func TestGenerateExample(t *testing.T) {
	runGenerateTest(t, &generate.Config{
		Schema:     generate.StringList{"example/schema.graphql"},
		Operations: generate.StringList{"example/genqlient.graphql"},
		Generated:  "example/generated.go",
	})
}

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

	root := filepath.Dir(filepath.Dir(thisFile))
	_, err := os.Stat(filepath.Join(root, ".gitignore"))
	if err != nil {
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
		}
	}
}
