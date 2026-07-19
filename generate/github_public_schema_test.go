// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package generate

import (
	"bufio"
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
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

const (
	githubPublicSchemaDir            = "testdata/github-public-schema"
	githubPublicSchemaRevision       = "27a4008f193706042a40cbb6c71cf85633249e79"
	githubPublicSchemaPath           = "src/graphql/data/fpt/schema.docs.graphql"
	githubPublicSchemaSize           = int64(1520362)
	githubPublicSchemaSHA256         = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
	githubPublicSchemaRepository     = "https://github.com/github/docs"
	githubPublicSchemaSourceURL      = "https://raw.githubusercontent.com/github/docs/" + githubPublicSchemaRevision + "/" + githubPublicSchemaPath
	githubPublicSchemaConfigFile     = "octoql.yaml"
	githubPublicSchemaMaterialized   = "schema.docs.graphql"
	githubPublicSchemaProvenanceFile = "PROVENANCE"
)

type publicSchemaCommand func(context.Context, string, io.Writer) error

type publicSchemaFixtureConfig struct {
	Schema struct {
		Path   string `yaml:"path"`
		SHA256 string `yaml:"sha256"`
		Source struct {
			GitHubDocs struct {
				Version  string `yaml:"version"`
				Revision string `yaml:"revision"`
			} `yaml:"github_docs"`
		} `yaml:"source"`
	} `yaml:"schema"`
	Operations []string `yaml:"operations"`
	Generated  string   `yaml:"generated"`
}

func TestGenerateGitHubPublicSchema(t *testing.T) {
	metadata := readGitHubPublicSchemaProvenance(t)
	fixtureConfig := readGitHubPublicSchemaConfig(t, metadata)
	schemaFilename := filepath.Join(githubPublicSchemaDir, fixtureConfig.Schema.Path)
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
		Bindings: map[string]*TypeBinding{
			"DateTime":    {Type: "time.Time"},
			"GitObjectID": {Type: "string"},
			"URI":         {Type: "string"},
		},
	}

	first, err := Generate(config)
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}
	second, err := Generate(config)
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}

	firstSource := first[generatedFilename]
	secondSource := second[generatedFilename]
	if !bytes.Equal(firstSource, secondSource) {
		t.Fatal("generation is not deterministic")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), generatedFilename, firstSource, parser.AllErrors); err != nil {
		t.Fatalf("generated Go is not syntactically valid: %v", err)
	}
	compileGitHubPublicSchemaOutput(t, generatedDir, firstSource)

	output := string(firstSource)
	for _, want := range []string{
		"func RepositorySummary(",
		"func NodesByID(",
		"func StarRepository(",
		"CreatedAt time.Time",
		"Oid string",
		"Url string",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("generated output does not contain %q", want)
		}
	}
}

func readGitHubPublicSchemaProvenance(t *testing.T) map[string]string {
	t.Helper()

	file, err := os.Open(filepath.Join(githubPublicSchemaDir, githubPublicSchemaProvenanceFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	metadata := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || key == "" || value == "" {
			t.Fatalf("invalid provenance line %q", scanner.Text())
		}
		if _, exists := metadata[key]; exists {
			t.Fatalf("duplicate provenance field %q", key)
		}
		metadata[key] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	expected := map[string]string{
		"format_version":    "2",
		"source_repository": githubPublicSchemaRepository,
		"source_commit":     githubPublicSchemaRevision,
		"source_path":       githubPublicSchemaPath,
		"source_url":        githubPublicSchemaSourceURL,
		"source_size_bytes": fmt.Sprintf("%d", githubPublicSchemaSize),
		"source_sha256":     githubPublicSchemaSHA256,
		"config":            githubPublicSchemaConfigFile,
		"materialized_path": githubPublicSchemaMaterialized,
		"materialization":   "on-demand via octoqlgen schema --config octoql.yaml",
	}
	if len(metadata) != len(expected) {
		t.Fatalf("provenance has %d fields, want %d", len(metadata), len(expected))
	}
	for key, want := range expected {
		if got := metadata[key]; got != want {
			t.Errorf("provenance %s = %q, want %q", key, got, want)
		}
	}
	return metadata
}

func readGitHubPublicSchemaConfig(t *testing.T, metadata map[string]string) publicSchemaFixtureConfig {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(githubPublicSchemaDir, metadata["config"]))
	if err != nil {
		t.Fatal(err)
	}
	var fixtureConfig publicSchemaFixtureConfig
	err = yaml.UnmarshalStrict(content, &fixtureConfig)
	if err != nil {
		t.Fatal(err)
	}
	if fixtureConfig.Schema.Path != metadata["materialized_path"] {
		t.Errorf("configured schema path = %q, want %q", fixtureConfig.Schema.Path, metadata["materialized_path"])
	}
	if fixtureConfig.Schema.SHA256 != metadata["source_sha256"] {
		t.Errorf("configured schema SHA-256 = %q, want %q", fixtureConfig.Schema.SHA256, metadata["source_sha256"])
	}
	if fixtureConfig.Schema.Source.GitHubDocs.Version != "fpt" {
		t.Errorf("configured github/docs version = %q, want %q", fixtureConfig.Schema.Source.GitHubDocs.Version, "fpt")
	}
	if fixtureConfig.Schema.Source.GitHubDocs.Revision != metadata["source_commit"] {
		t.Errorf(
			"configured github/docs revision = %q, want %q",
			fixtureConfig.Schema.Source.GitHubDocs.Revision,
			metadata["source_commit"],
		)
	}
	if len(fixtureConfig.Operations) != 1 || fixtureConfig.Operations[0] != "operations.graphql" {
		t.Errorf("configured operations = %q, want [operations.graphql]", fixtureConfig.Operations)
	}
	if fixtureConfig.Generated != "generated.go" {
		t.Errorf("configured generated path = %q, want %q", fixtureConfig.Generated, "generated.go")
	}
	return fixtureConfig
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
	moduleRoot, err := filepath.Abs("..")
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

		err := ensureGitHubPublicSchema(t.Context(), schemaFilename, "octoql.yaml", run)
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

		err = ensureGitHubPublicSchema(t.Context(), schemaFilename, "octoql.yaml", run)
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

		err := ensureGitHubPublicSchema(t.Context(), schemaFilename, "octoql.yaml", run)
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

	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	goMod := fmt.Sprintf(`module github.com/willabides/octoql-public-schema-test

go 1.26.0

require github.com/willabides/octoql v0.0.0

replace github.com/willabides/octoql => %s
`, moduleRoot)
	err = os.WriteFile(filepath.Join(generatedDir, "go.mod"), []byte(goMod), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	goSum, err := os.ReadFile(filepath.Join(moduleRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(generatedDir, "go.sum"), goSum, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(generatedDir, "generated.go"), source, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"mod", "tidy"}, {"test", "-mod=readonly", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = generatedDir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
		output, commandErr := cmd.CombinedOutput()
		if commandErr != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), commandErr, output)
		}
	}
}
