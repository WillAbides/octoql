// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const (
	testRevision = "45d83f459620340069df7c375a8867be62616d61"
	testSHA256   = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

type corpusFixture struct {
	Name      string            `json:"name"`
	Base      string            `json:"base"`
	WantError string            `json:"want_error"`
	Mutations []fixtureMutation `json:"mutations"`
	Valid     bool              `json:"valid"`
}

type fixtureMutation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func TestSchemaCorpus(t *testing.T) {
	t.Parallel()

	fixtures, err := readCorpus()
	require.NoError(t, err)
	compiled, err := compileCanonicalSchema()
	require.NoError(t, err)

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()

			_, document, fixtureErr := buildFixture(&fixture)
			require.NoError(t, fixtureErr)
			err := compiled.Validate(document)
			if fixture.Valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, matchesSchemaReason(err, fixture.WantError), "%v", err)
		})
	}
}

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

func TestGeneratedModelUsesPresencePointers(t *testing.T) {
	t.Parallel()

	sha256 := testSHA256
	host := "github.example.com"
	sourceURL := "https://schemas.example.com/schema.graphql"
	model := Config{
		Schema: Schema{
			Path:   ".octoql/schema.graphql",
			Sha256: &sha256,
			Source: &Source{
				Url: &sourceURL,
				GithubRepository: &GitHubRepository{
					Host: &host,
				},
			},
		},
	}
	assert.Same(t, &sha256, model.Schema.Sha256)
	assert.Same(t, &sourceURL, model.Schema.Source.Url)
	assert.Same(t, &host, model.Schema.Source.GithubRepository.Host)
}

func readCorpus() ([]corpusFixture, error) {
	content, err := os.ReadFile(filepath.Join("testdata", "corpus.json"))
	if err != nil {
		return nil, err
	}
	fixtures := []corpusFixture{}
	err = json.Unmarshal(content, &fixtures)
	if err != nil {
		return nil, err
	}
	return fixtures, nil
}

func compileCanonicalSchema() (*jsonschema.Schema, error) {
	document, err := readSchemaDocument()
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	err = compiler.AddResource("octoql.schema.yaml", document)
	if err != nil {
		return nil, err
	}
	return compiler.Compile("octoql.schema.yaml")
}

func readSchemaDocument() (any, error) {
	content, err := os.ReadFile(schemaPath())
	if err != nil {
		return nil, err
	}
	jsonContent, err := yaml.YAMLToJSON(content)
	if err != nil {
		return nil, err
	}
	var document any
	err = json.Unmarshal(jsonContent, &document)
	if err != nil {
		return nil, err
	}
	return document, nil
}

func buildFixture(fixture *corpusFixture) ([]byte, any, error) {
	document, err := baseDocument(fixture.Base)
	if err != nil {
		return nil, nil, err
	}
	for _, mutation := range fixture.Mutations {
		err = applyMutation(document, mutation)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", fixture.Name, err)
		}
	}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return content, document, nil
}

func baseDocument(name string) (map[string]any, error) {
	document := map[string]any{
		"schema": map[string]any{
			"path": ".octoql/schema.graphql",
		},
		"operations": []any{"graphql/**/*.graphql"},
		"generated":  "githubapi/generated.go",
		"test_handler": map[string]any{
			"generated": "githubapitest/generated.go",
		},
	}
	if name == "local" {
		return document, nil
	}

	schema := document["schema"].(map[string]any)
	schema["sha256"] = testSHA256
	switch name {
	case "docs":
		schema["source"] = map[string]any{
			"github_docs": map[string]any{
				"version":  "fpt",
				"revision": testRevision,
			},
		}
	case "repository":
		schema["source"] = map[string]any{
			"github_repository": map[string]any{
				"repository": "octo-org/octo-repo",
				"revision":   testRevision,
				"path":       "schema/github.graphql",
			},
		}
	case "url":
		schema["source"] = map[string]any{
			"url": "https://schemas.example.com/github.graphql",
		}
	default:
		return nil, fmt.Errorf("unknown fixture base %q", name)
	}
	return document, nil
}

func applyMutation(document map[string]any, mutation fixtureMutation) error {
	segments := strings.Split(strings.TrimPrefix(mutation.Path, "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return errors.New("mutation path must not be empty")
	}

	var current any = document
	for _, segment := range segments[:len(segments)-1] {
		next, err := fixtureChild(current, segment)
		if err != nil {
			return err
		}
		current = next
	}
	last := segments[len(segments)-1]
	switch mutation.Op {
	case "remove":
		mapping, ok := current.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot remove %q from %T", last, current)
		}
		delete(mapping, last)
		return nil
	case "set":
		var value any
		err := json.Unmarshal(mutation.Value, &value)
		if err != nil {
			return err
		}
		return setFixtureChild(current, last, value)
	default:
		return fmt.Errorf("unknown mutation operation %q", mutation.Op)
	}
}

func fixtureChild(current any, segment string) (any, error) {
	switch value := current.(type) {
	case map[string]any:
		child, exists := value[segment]
		if !exists {
			return nil, fmt.Errorf("fixture path segment %q does not exist", segment)
		}
		return child, nil
	case []any:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(value) {
			return nil, fmt.Errorf("invalid fixture index %q", segment)
		}
		return value[index], nil
	default:
		return nil, fmt.Errorf("cannot traverse fixture value %T", current)
	}
}

func setFixtureChild(current any, segment string, child any) error {
	switch value := current.(type) {
	case map[string]any:
		value[segment] = child
		return nil
	case []any:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(value) {
			return fmt.Errorf("invalid fixture index %q", segment)
		}
		value[index] = child
		return nil
	default:
		return fmt.Errorf("cannot set fixture value on %T", current)
	}
}

func matchesSchemaReason(err error, reason string) bool {
	message := err.Error()
	if strings.Contains(message, reason) {
		return true
	}
	switch reason {
	case "type":
		return strings.Contains(message, "got ") && strings.Contains(message, ", want ")
	case "additionalProperties":
		return strings.Contains(message, "additional properties")
	default:
		return false
	}
}

func schemaPath() string {
	return filepath.Join("..", "..", "..", "..", "octoql.schema.yaml")
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
