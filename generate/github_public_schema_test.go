// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package generate

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	githubPublicSchemaDir    = "testdata/github-public-schema"
	githubPublicSchemaSHA256 = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

func TestGenerateGitHubPublicSchema(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")

	schemaFilename := decompressGitHubPublicSchema(t)
	generatedFilename := filepath.Join(githubPublicSchemaDir, "generated.go")
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

func decompressGitHubPublicSchema(t *testing.T) string {
	t.Helper()

	compressed, err := os.Open(filepath.Join(githubPublicSchemaDir, "schema.docs.graphql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()

	reader, err := gzip.NewReader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	schema, err := os.CreateTemp(githubPublicSchemaDir, "schema-*.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schemaFilename := schema.Name()
	t.Cleanup(func() {
		if err := os.Remove(schemaFilename); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove decompressed schema: %v", err)
		}
	})

	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(schema, digest), reader); err != nil {
		schema.Close()
		t.Fatal(err)
	}
	if err := schema.Close(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", digest.Sum(nil)); got != githubPublicSchemaSHA256 {
		t.Fatalf("schema SHA-256 = %s, want %s", got, githubPublicSchemaSHA256)
	}

	return schemaFilename
}
