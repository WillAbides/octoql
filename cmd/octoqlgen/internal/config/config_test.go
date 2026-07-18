// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRevision = "45d83f459620340069df7c375a8867be62616d61"
	testSHA256   = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	filename := filepath.Join(configDir, DefaultFilename)
	err := os.WriteFile(filename, []byte(validConfigYAML()), 0o600)
	require.NoError(t, err)

	loaded, err := Load(filename)
	require.NoError(t, err)
	require.NotNil(t, loaded.Schema.Source)
	require.NotNil(t, loaded.Schema.Source.GithubDocs)
	assert.Equal(t, "fpt", loaded.Schema.Source.GithubDocs.Version)
	assert.Equal(t, filepath.Join(configDir, ".octoql", "schema.graphql"), loaded.SchemaPath())
	assert.Equal(
		t,
		[]string{filepath.Join(configDir, "graphql", "**", "*.graphql")},
		loaded.OperationPaths(),
	)
	assert.Equal(t, filepath.Join(configDir, "githubapi", "generated.go"), loaded.GeneratedPath())
	assert.Equal(
		t,
		filepath.Join(configDir, "githubapitest", "generated.go"),
		loaded.TestHandlerGeneratedPath(),
	)
}

func TestLoadUsesJSONTagsStrictly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		assertLoaded func(*testing.T, *Config)
		name         string
		content      string
		wantError    string
	}{
		{
			name:    "aliases",
			content: readTestFile(t, "yaml", "valid_aliases.yaml"),
			assertLoaded: func(t *testing.T, loaded *Config) {
				require.Len(t, loaded.Operations, 2)
				assert.Equal(t, loaded.Operations[0], loaded.Operations[1])
				require.NotNil(t, loaded.TestHandler)
				assert.Equal(t, loaded.Generated, loaded.TestHandler.Generated)
			},
		},
		{
			name:    "merge",
			content: readTestFile(t, "yaml", "valid_merge.yaml"),
		},
		{
			name:      "duplicate key",
			content:   readTestFile(t, "yaml", "invalid_duplicate_key.yaml"),
			wantError: "key \"path\" already set",
		},
		{
			name:      "unknown field",
			content:   validConfigYAML() + "unknown: true\n",
			wantError: `unknown field "unknown"`,
		},
		{
			name:      "multiple documents",
			content:   validConfigYAML() + "---\n{}\n",
			wantError: "multiple YAML documents are not allowed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), DefaultFilename)
			err := os.WriteFile(filename, []byte(test.content), 0o600)
			require.NoError(t, err)
			loaded, err := Load(filename)
			if test.wantError == "" {
				require.NoError(t, err)
				if test.assertLoaded != nil {
					test.assertLoaded(t, loaded)
				}
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
}

func TestLoadDoesNotValidateAgainstSchema(t *testing.T) {
	t.Parallel()

	filename := filepath.Join(t.TempDir(), DefaultFilename)
	err := os.WriteFile(filename, []byte("{}\n"), 0o600)
	require.NoError(t, err)

	loaded, err := Load(filename)
	require.NoError(t, err)
	assert.Equal(t, &Config{}, loaded)
	assert.Empty(t, loaded.TestHandlerGeneratedPath())
}

func TestUpdatePinPreservesUnrelatedFormatting(t *testing.T) {
	t.Parallel()

	content := []byte(
		"# keep this comment\n" +
			"schema:\n" +
			"  path: '.octoql/schema.graphql'\n" +
			"  sha256: \"" + testSHA256 + "\" # keep this too\n" +
			"  source:\n" +
			"    github_docs:\n" +
			"      version: fpt\n" +
			"      revision: '" + testRevision + "'\n" +
			"operations: [graphql/**/*.graphql]\n" +
			"generated: internal/githubapi/generated.go\n",
	)
	newSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newRevision := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	updated, err := UpdatePin(content, newSHA, newRevision)
	require.NoError(t, err)
	assert.Equal(
		t,
		"# keep this comment\n"+
			"schema:\n"+
			"  path: '.octoql/schema.graphql'\n"+
			"  sha256: \""+newSHA+"\" # keep this too\n"+
			"  source:\n"+
			"    github_docs:\n"+
			"      version: fpt\n"+
			"      revision: '"+newRevision+"'\n"+
			"operations: [graphql/**/*.graphql]\n"+
			"generated: internal/githubapi/generated.go\n",
		string(updated),
	)
}

func readTestFile(t *testing.T, elements ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, elements...)...))
	require.NoError(t, err)
	return string(content)
}

func validConfigYAML() string {
	return "schema:\n" +
		"  path: .octoql/schema.graphql\n" +
		"  sha256: " + testSHA256 + "\n" +
		"  source:\n" +
		"    github_docs:\n" +
		"      version: fpt\n" +
		"      revision: " + testRevision + "\n" +
		"operations:\n" +
		"  - graphql/**/*.graphql\n" +
		"generated: githubapi/generated.go\n" +
		"test_handler:\n" +
		"  generated: githubapitest/generated.go\n"
}
