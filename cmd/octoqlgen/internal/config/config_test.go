// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRevision = "45d83f459620340069df7c375a8867be62616d61"
	testSHA256   = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(string) string
		expectedError string
	}{
		{
			name:   "valid github docs source",
			mutate: func(input string) string { return input },
		},
		{
			name: "unknown field",
			mutate: func(input string) string {
				return input + "unknown: true\n"
			},
			expectedError: "field unknown not found",
		},
		{
			name: "multiple source variants",
			mutate: func(input string) string {
				return strings.Replace(
					input,
					"operations:",
					"    url: https://example.test/schema.graphql\noperations:",
					1,
				)
			},
			expectedError: "schema.source must set exactly one remote source variant",
		},
		{
			name: "remote source without checksum",
			mutate: func(input string) string {
				return strings.Replace(input, "  sha256: "+testSHA256+"\n", "", 1)
			},
			expectedError: "schema.sha256 is required for remote sources",
		},
		{
			name: "noncanonical checksum",
			mutate: func(input string) string {
				return strings.Replace(input, testSHA256, strings.ToUpper(testSHA256), 1)
			},
			expectedError: "canonical 64-character lowercase hexadecimal sha-256",
		},
		{
			name: "short github revision",
			mutate: func(input string) string {
				return strings.Replace(input, testRevision, testRevision[:12], 1)
			},
			expectedError: "revision must be a full lowercase hexadecimal commit sha",
		},
		{
			name: "invalid github docs version",
			mutate: func(input string) string {
				return strings.Replace(input, "version: fpt", "version: enterprise", 1)
			},
			expectedError: "version must be fpt, ghec, or ghes-X.Y",
		},
		{
			name: "url credentials",
			mutate: func(input string) string {
				return strings.Replace(
					input,
					"    github_docs:\n      version: fpt\n      revision: "+testRevision,
					"    url: https://user:password@example.test/schema.graphql",
					1,
				)
			},
			expectedError: "credentials in urls are not allowed",
		},
		{
			name: "empty url",
			mutate: func(input string) string {
				return strings.Replace(
					input,
					"    github_docs:\n      version: fpt\n      revision: "+testRevision,
					"    url: \"\"",
					1,
				)
			},
			expectedError: "scheme must be http or https",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			configDir := t.TempDir()
			filename := filepath.Join(configDir, DefaultFilename)
			content := test.mutate(validConfigYAML())
			err := os.WriteFile(filename, []byte(content), 0o600)
			require.NoError(t, err)

			loaded, err := Load(filename)
			if test.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, ".octoql/schema.graphql", loaded.Schema.Path)
			assert.Equal(t, filepath.Join(configDir, ".octoql", "schema.graphql"), loaded.SchemaPath())
			assert.Equal(
				t,
				[]string{filepath.Join(configDir, "graphql", "**", "*.graphql")},
				loaded.OperationPaths(),
			)
			assert.Equal(
				t,
				filepath.Join(configDir, "internal", "githubapi", "generated.go"),
				loaded.GeneratedPath(),
			)
			assert.Equal(
				t,
				filepath.Join(configDir, "internal", "githubapitest", "generated.go"),
				loaded.TestHandlerGeneratedPath(),
			)
		})
	}
}

func TestLoadLocalSchema(t *testing.T) {
	t.Parallel()

	content := strings.Replace(
		validConfigYAML(),
		"  sha256: "+testSHA256+"\n  source:\n    github_docs:\n      version: fpt\n      revision: "+testRevision+"\n",
		"",
		1,
	)
	filename := filepath.Join(t.TempDir(), DefaultFilename)
	err := os.WriteFile(filename, []byte(content), 0o600)
	require.NoError(t, err)

	loaded, err := Load(filename)
	require.NoError(t, err)
	assert.Empty(t, loaded.Schema.SHA256)
	assert.Equal(t, Source{}, loaded.Schema.Source)
}

func TestGitHubRepositoryValidate(t *testing.T) {
	tests := []struct {
		name          string
		repository    GitHubRepository
		expectedError string
		expectedHost  string
	}{
		{
			name: "valid github repository",
			repository: GitHubRepository{
				Repository: "octo-org/octo-repo",
				Revision:   testRevision,
				Path:       "schema/github.graphql",
			},
			expectedHost: "github.com",
		},
		{
			name: "valid enterprise repository",
			repository: GitHubRepository{
				Repository: "octo-org/octo-repo",
				Revision:   testRevision,
				Path:       "schema/github.graphql",
				Host:       "github.example.com",
			},
			expectedHost: "github.example.com",
		},
		{
			name: "invalid repository",
			repository: GitHubRepository{
				Repository: "../octo-repo",
				Revision:   testRevision,
				Path:       "schema.graphql",
			},
			expectedError: "owner/name pair",
		},
		{
			name: "traversing path",
			repository: GitHubRepository{
				Repository: "octo-org/octo-repo",
				Revision:   testRevision,
				Path:       "../schema.graphql",
			},
			expectedError: "without traversal",
		},
		{
			name: "host with scheme",
			repository: GitHubRepository{
				Repository: "octo-org/octo-repo",
				Revision:   testRevision,
				Path:       "schema.graphql",
				Host:       "https://github.example.com",
			},
			expectedError: "hostname and optional port",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := test.repository
			err := repository.Validate()
			if test.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expectedHost, repository.Host)
		})
	}
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
		"generated: internal/githubapi/generated.go\n" +
		"test_handler:\n" +
		"  generated: internal/githubapitest/generated.go\n"
}
