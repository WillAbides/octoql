package generate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	githubPublicSchemaDir          = "testdata/github-public-schema"
	githubPublicSchemaSize         = int64(1520362)
	githubPublicSchemaSHA256       = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
	githubPublicSchemaConfigFile   = "octoqlgen.yaml"
	githubPublicSchemaMaterialized = "schema.docs.graphql"
)

type publicSchemaCommand func(context.Context, string, io.Writer) error

func TestGenerateGitHubPublicSchema(t *testing.T) {
	t.Parallel()

	schemaFilename := filepath.Join(githubPublicSchemaDir, githubPublicSchemaMaterialized)
	configFilename := filepath.Join(githubPublicSchemaDir, githubPublicSchemaConfigFile)
	err := ensureGitHubPublicSchema(
		t.Context(),
		schemaFilename,
		configFilename,
		runGitHubPublicSchemaMaterializer,
	)
	if err != nil {
		t.Fatal(err)
	}
	validateGitHubPublicSchema(t, schemaFilename)

	generatedDir := t.TempDir()
	generatedFilename := filepath.Join(generatedDir, "generated.go")
	config := &Config{
		Schema:      []string{schemaFilename},
		Operations:  []string{filepath.Join(githubPublicSchemaDir, "operations.graphql")},
		Generated:   generatedFilename,
		Package:     "githubschema",
		ContextType: "-",
	}

	generated, err := Generate(config)
	if err != nil {
		t.Fatalf("generation: %v", err)
	}

	source := generated[generatedFilename]
	_, parseErr := parser.ParseFile(token.NewFileSet(), generatedFilename, source, parser.AllErrors)
	if parseErr != nil {
		t.Fatalf("generated Go is not syntactically valid: %v", parseErr)
	}
	compileGitHubPublicSchemaOutput(t, generatedDir, source)

	output := string(source)
	for _, want := range []string{
		"func (c *Client) RepositorySummary(",
		"func (c *Client) NodesByID(",
		"func (c *Client) StarRepository(",
		"func (c *Client) GitHubAbstractCorpus(",
		"CreatedAt time.Time",
		"Oid string",
		"Url string",
		"OctoqlOther struct",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("generated output does not contain %q", want)
		}
	}
	for _, pattern := range []string{
		`type \w*Nodes\w*Repository struct`,
		`type \w*Nodes\w*Issue struct`,
		`type \w*Nodes\w*PullRequest struct`,
	} {
		if !regexp.MustCompile(pattern).MatchString(output) {
			t.Errorf("generated output does not match %q", pattern)
		}
	}
	for _, pattern := range []string{
		"GenqlientOther",
		`type \w*Nodes\w*App struct`,
		`type \w*Search\w*Nodes\w*MarketplaceListing struct`,
	} {
		if regexp.MustCompile(pattern).MatchString(output) {
			t.Errorf("generated output unexpectedly matches %q", pattern)
		}
	}
}

func ensureGitHubPublicSchema(
	ctx context.Context,
	schemaFilename string,
	configFilename string,
	run publicSchemaCommand,
) error {
	_, err := os.Stat(schemaFilename)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking materialized GitHub schema: %w", err)
	}

	absoluteConfig, err := filepath.Abs(configFilename)
	if err != nil {
		return fmt.Errorf("resolving public schema config: %w", err)
	}
	var stderr bytes.Buffer
	err = run(ctx, absoluteConfig, &stderr)
	if err != nil {
		detail := redactGitHubPublicSchemaCommandOutput(stderr.String())
		if detail == "" {
			detail = "no command diagnostics"
		}
		return fmt.Errorf(
			"materializing the pinned public GitHub schema: %w\n"+
				"first-run materialization requires access to github.com. "+
				"Check the network, then run `go run ./cmd/octoqlgen schema --config %s` from the repository root.\n"+
				"octoqlgen stderr: %s",
			err,
			absoluteConfig,
			detail,
		)
	}
	if _, err := os.Stat(schemaFilename); err != nil {
		return fmt.Errorf("octoqlgen completed without materializing %q: %w", schemaFilename, err)
	}
	return nil
}

func runGitHubPublicSchemaMaterializer(ctx context.Context, configFilename string, stderr io.Writer) error {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}
	cmd := exec.CommandContext(
		ctx,
		"go",
		"run",
		"./cmd/octoqlgen",
		"schema",
		"--config",
		configFilename,
	)
	cmd.Dir = moduleRoot
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("running octoqlgen schema: %w", err)
	}
	return nil
}

func redactGitHubPublicSchemaCommandOutput(output string) string {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		secret, ok := os.LookupEnv(name)
		if ok && secret != "" {
			output = strings.ReplaceAll(output, secret, "[redacted]")
		}
	}
	return strings.TrimSpace(output)
}

func validateGitHubPublicSchema(t *testing.T, schemaFilename string) {
	t.Helper()

	schema, err := os.ReadFile(schemaFilename)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(schema)) != githubPublicSchemaSize {
		t.Fatalf("schema size = %d, want %d", len(schema), githubPublicSchemaSize)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(schema)); got != githubPublicSchemaSHA256 {
		t.Fatalf("schema SHA-256 = %s, want %s", got, githubPublicSchemaSHA256)
	}
}

func TestEnsureGitHubPublicSchema(t *testing.T) {
	t.Run("materializes a missing schema", func(t *testing.T) {
		schemaFilename := filepath.Join(t.TempDir(), githubPublicSchemaMaterialized)
		calls := 0
		run := func(_ context.Context, _ string, _ io.Writer) error {
			calls++
			return os.WriteFile(schemaFilename, []byte("schema"), 0o600)
		}

		err := ensureGitHubPublicSchema(t.Context(), schemaFilename, "octoqlgen.yaml", run)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("materializer calls = %d, want 1", calls)
		}
	})

	t.Run("reuses an existing schema", func(t *testing.T) {
		schemaFilename := filepath.Join(t.TempDir(), githubPublicSchemaMaterialized)
		err := os.WriteFile(schemaFilename, []byte("schema"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		run := func(_ context.Context, _ string, _ io.Writer) error {
			calls++
			return errors.New("materializer should not run")
		}

		err = ensureGitHubPublicSchema(t.Context(), schemaFilename, "octoqlgen.yaml", run)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Fatalf("materializer calls = %d, want 0", calls)
		}
	})

	t.Run("reports actionable redacted failures", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "secret-token")
		schemaFilename := filepath.Join(t.TempDir(), githubPublicSchemaMaterialized)
		run := func(_ context.Context, _ string, stderr io.Writer) error {
			_, writeErr := io.WriteString(stderr, "download failed with secret-token")
			if writeErr != nil {
				return writeErr
			}
			return errors.New("exit status 1")
		}

		err := ensureGitHubPublicSchema(t.Context(), schemaFilename, "octoqlgen.yaml", run)
		if err == nil {
			t.Fatal("expected materialization error")
		}
		message := err.Error()
		if strings.Contains(message, "secret-token") {
			t.Fatalf("error contains secret: %s", message)
		}
		for _, want := range []string{"first-run materialization requires access to github.com", "[redacted]"} {
			if !strings.Contains(message, want) {
				t.Errorf("error does not contain %q: %s", want, message)
			}
		}
	})
}

func compileGitHubPublicSchemaOutput(t *testing.T, generatedDir string, source []byte) {
	t.Helper()

	goMod := "module github.com/willabides/octoql-public-schema-test\n\ngo 1.27.3\n"
	err := os.WriteFile(filepath.Join(generatedDir, "go.mod"), []byte(goMod), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(generatedDir, "generated.go"), source, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "-mod=readonly", "./...")
	cmd.Dir = generatedDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	output, commandErr := cmd.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("go build -mod=readonly ./...: %v\n%s", commandErr, output)
	}
}
