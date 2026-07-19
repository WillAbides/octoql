// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package generate

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	githubPublicSchemaDir            = "testdata/github-public-schema"
	githubPublicSchemaRevision       = "27a4008f193706042a40cbb6c71cf85633249e79"
	githubPublicSchemaPath           = "src/graphql/data/fpt/schema.docs.graphql"
	githubPublicSchemaSize           = int64(1520362)
	githubPublicSchemaSHA256         = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
	githubPublicSchemaGzipSize       = int64(191850)
	githubPublicSchemaGzipSHA256     = "5f36217e3b90327c50e648ee31c621db6720debc31b876b7bf69caeff31dcf16"
	githubPublicSchemaRepository     = "https://github.com/github/docs"
	githubPublicSchemaSourceURL      = "https://raw.githubusercontent.com/github/docs/" + githubPublicSchemaRevision + "/" + githubPublicSchemaPath
	githubPublicSchemaFixture        = "schema.docs.graphql.gz"
	githubPublicSchemaChecksumFile   = "schema.docs.graphql.sha256"
	githubPublicSchemaProvenanceFile = "PROVENANCE"
)

func TestGenerateGitHubPublicSchema(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")

	metadata := readGitHubPublicSchemaProvenance(t)
	validateGitHubPublicSchemaChecksumFile(t, metadata)
	schemaFilename := decompressGitHubPublicSchema(t, metadata)
	generatedDir := t.TempDir()
	generatedFilename := filepath.Join(generatedDir, "generated.go")
	config := &Config{
		Schema:      []string{schemaFilename},
		Operations:  []string{filepath.Join(githubPublicSchemaDir, "operations.graphql")},
		Generated:   generatedFilename,
		Package:     "githubschema",
		ContextType: "-",
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
	_, parseErr := parser.ParseFile(token.NewFileSet(), generatedFilename, firstSource, parser.AllErrors)
	if parseErr != nil {
		t.Fatalf("generated Go is not syntactically valid: %v", parseErr)
	}
	compileGitHubPublicSchemaOutput(t, generatedDir, firstSource)

	omit := false
	optOutConfig := *config
	optOutConfig.OmitUnreferencedImplementations = &omit
	optOut, err := Generate(&optOutConfig)
	if err != nil {
		t.Fatalf("opt-out generation: %v", err)
	}
	optOutSource := optOut[generatedFilename]
	defaultImplementations := bytes.Count(firstSource, []byte(") implementsGraphQLInterface"))
	optOutImplementations := bytes.Count(optOutSource, []byte(") implementsGraphQLInterface"))
	if optOutImplementations <= defaultImplementations*2 {
		t.Fatalf(
			"default implementation count = %d, opt-out = %d; omission did not materially bound output",
			defaultImplementations,
			optOutImplementations,
		)
	}
	if len(firstSource) >= len(optOutSource) {
		t.Fatalf("default output size = %d, opt-out = %d", len(firstSource), len(optOutSource))
	}

	output := string(firstSource)
	for _, want := range []string{
		"func RepositorySummary(",
		"func NodesByID(",
		"func StarRepository(",
		"func GitHubAbstractCorpus(",
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
		"format_version":      "1",
		"source_repository":   githubPublicSchemaRepository,
		"source_commit":       githubPublicSchemaRevision,
		"source_path":         githubPublicSchemaPath,
		"source_url":          githubPublicSchemaSourceURL,
		"source_size_bytes":   strconv.FormatInt(githubPublicSchemaSize, 10),
		"source_sha256":       githubPublicSchemaSHA256,
		"fixture":             githubPublicSchemaFixture,
		"fixture_compression": "gzip",
		"fixture_size_bytes":  strconv.FormatInt(githubPublicSchemaGzipSize, 10),
		"fixture_sha256":      githubPublicSchemaGzipSHA256,
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

func validateGitHubPublicSchemaChecksumFile(t *testing.T, metadata map[string]string) {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(githubPublicSchemaDir, githubPublicSchemaChecksumFile))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(content))
	if len(fields) != 2 {
		t.Fatalf("%s must contain a checksum and filename", githubPublicSchemaChecksumFile)
	}
	if fields[0] != githubPublicSchemaSHA256 || fields[0] != metadata["source_sha256"] {
		t.Fatalf("%s checksum = %q, want %q", githubPublicSchemaChecksumFile, fields[0], githubPublicSchemaSHA256)
	}
	if fields[1] != "schema.docs.graphql" {
		t.Fatalf("%s filename = %q, want %q", githubPublicSchemaChecksumFile, fields[1], "schema.docs.graphql")
	}
}

func decompressGitHubPublicSchema(t *testing.T, metadata map[string]string) string {
	t.Helper()

	compressedFilename := filepath.Join(githubPublicSchemaDir, metadata["fixture"])
	compressed, err := os.Open(compressedFilename)
	if err != nil {
		t.Fatal(err)
	}
	compressedDigest := sha256.New()
	compressedSize, err := io.Copy(compressedDigest, compressed)
	closeErr := compressed.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if compressedSize != githubPublicSchemaGzipSize {
		t.Fatalf("compressed schema size = %d, want %d", compressedSize, githubPublicSchemaGzipSize)
	}
	if got := fmt.Sprintf("%x", compressedDigest.Sum(nil)); got != githubPublicSchemaGzipSHA256 {
		t.Fatalf("compressed schema SHA-256 = %s, want %s", got, githubPublicSchemaGzipSHA256)
	}

	compressed, err = os.Open(compressedFilename)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	schemaFilename := filepath.Join(t.TempDir(), "schema.docs.graphql")
	schema, err := os.Create(schemaFilename)
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.New()
	rawSize, err := io.Copy(io.MultiWriter(schema, digest), io.LimitReader(reader, githubPublicSchemaSize+1))
	if err != nil {
		schema.Close()
		t.Fatal(err)
	}
	if err := schema.Close(); err != nil {
		t.Fatal(err)
	}
	if rawSize != githubPublicSchemaSize {
		t.Fatalf("schema size = %d, want %d", rawSize, githubPublicSchemaSize)
	}
	if got := fmt.Sprintf("%x", digest.Sum(nil)); got != githubPublicSchemaSHA256 {
		t.Fatalf("schema SHA-256 = %s, want %s", got, githubPublicSchemaSHA256)
	}

	return schemaFilename
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
