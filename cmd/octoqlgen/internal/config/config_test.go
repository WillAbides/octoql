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
		name      string
		content   string
		wantError string
	}{
		{
			name:    "aliases",
			content: readTestFile(t, "yaml", "valid_aliases.yaml"),
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), DefaultFilename)
			err := os.WriteFile(filename, []byte(test.content), 0o600)
			require.NoError(t, err)
			_, err = Load(filename)
			if test.wantError == "" {
				require.NoError(t, err)
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
