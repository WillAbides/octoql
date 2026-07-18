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

func TestGitHubRepositoryValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		host      *string
		wantHost  string
		wantError string
	}{
		{name: "default host", wantHost: "github.com"},
		{name: "DNS", host: new("github.example.com"), wantHost: "github.example.com"},
		{name: "trailing dot DNS", host: new("github.example.com."), wantHost: "github.example.com."},
		{name: "IPv4 and port", host: new("192.0.2.10:8443"), wantHost: "192.0.2.10:8443"},
		{name: "bracketed IPv6", host: new("[2001:db8::1]"), wantHost: "[2001:db8::1]"},
		{name: "empty", host: new(""), wantError: "must not be empty"},
		{name: "scheme", host: new("https://github.example.com"), wantError: "hostname and optional port"},
		{name: "empty port", host: new("github.example.com:"), wantError: "port must be nonempty"},
		{name: "malformed port", host: new("github.example.com:https"), wantError: "must be numeric"},
		{name: "invalid IPv4", host: new("999.999.999.999"), wantError: "valid IPv4"},
		{name: "malformed IPv6", host: new("[2001:::1]"), wantError: "valid bracketed IPv6"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := GitHubRepository{
				Repository: "octo-org/octo-repo",
				Revision:   testRevision,
				Path:       "schema/github.graphql",
				Host:       test.host,
			}
			err := repository.Validate()
			if test.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantError)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, repository.Host)
			assert.Equal(t, test.wantHost, *repository.Host)
		})
	}
}

func TestValidateSourceURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		url       string
		wantError string
	}{
		{name: "HTTP", url: "http://schemas.example.com/schema.graphql"},
		{name: "uppercase scheme", url: "HTTPS://schemas.example.com/schema.graphql"},
		{name: "escaped path query fragment", url: "https://schemas.example.com/a%20b?x=y%20z#part"},
		{name: "credentials", url: "https://user:password@schemas.example.com/schema.graphql", wantError: "credentials"},
		{name: "invalid percent escape", url: "https://schemas.example.com/%zz", wantError: "valid HTTP"},
		{name: "whitespace", url: "https://schemas.example.com/a b", wantError: "whitespace"},
		{name: "missing host", url: "https:///schema.graphql", wantError: "host is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateSource(Source{Url: &test.url}, testSHA256)
			if test.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantError)
				assert.NotContains(t, err.Error(), "?")
				assert.NotContains(t, err.Error(), "#")
				return
			}
			require.NoError(t, err)
		})
	}
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
