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
			name:      "removed extensions",
			content:   validConfigYAML() + "use_extensions: true\n",
			wantError: `unknown field "use_extensions"`,
		},
		{
			name:      "removed optional mode",
			content:   validConfigYAML() + "optional: pointer\n",
			wantError: `unknown field "optional"`,
		},
		{
			name:      "removed optional generic type",
			content:   validConfigYAML() + "optional_generic_type: example.com/optional.Value\n",
			wantError: `unknown field "optional_generic_type"`,
		},
		{
			name:      "subscription setting",
			content:   validConfigYAML() + "subscriptions: true\n",
			wantError: `unknown field "subscriptions"`,
		},
		{
			name: "unknown binding field",
			content: validConfigYAML() +
				"bindings:\n" +
				"  DateTime:\n" +
				"    unknown: true\n",
			wantError: `unknown field "unknown"`,
		},
		{
			name: "unknown casing field",
			content: validConfigYAML() +
				"casing:\n" +
				"  unknown: raw\n",
			wantError: `unknown field "unknown"`,
		},
		{
			name:      "invalid test handler type strategy",
			content:   validConfigYAML() + "  types: invalid\n",
			wantError: `test_handler.types must be one of "client" or "local"`,
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

func TestLoadGeneratorOptions(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	filename := filepath.Join(directory, DefaultFilename)
	content := []byte(
		"schema:\n" +
			"  path: .octoql/schema.graphql\n" +
			"  source:\n" +
			"    github_docs:\n" +
			"      version: fpt\n" +
			"      revision: " + testRevision + "\n" +
			"operations:\n" +
			"  - graphql/**/*.graphql\n" +
			"generated: generated/client.go\n" +
			"package: githubapi\n" +
			"export_operations: generated/operations.json\n" +
			"context_type: github.com/example/context.Type\n" +
			"client_getter: github.com/example/client.Get\n" +
			"bindings:\n" +
			"  DateTime:\n" +
			"    type: github.com/example/scalar.DateTime\n" +
			"    expect_exact_fields: \"{ id }\"\n" +
			"    marshaler: github.com/example/scalar.Marshal\n" +
			"    unmarshaler: github.com/example/scalar.Unmarshal\n" +
			"package_bindings:\n" +
			"  - package: github.com/example/models\n" +
			"casing:\n" +
			"  default: auto_camel_case\n" +
			"  all_enums: raw\n" +
			"  enums:\n" +
			"    IssueState: default\n" +
			"use_struct_references: true\n" +
			"omit_unreferenced_implementations: false\n" +
			"test_handler:\n" +
			"  generated: generated/testhandler.go\n" +
			"  types: local\n",
	)
	err := os.WriteFile(filename, content, 0o600)
	require.NoError(t, err)

	loaded, err := Load(filename)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(directory, ".octoql", "schema.graphql"), loaded.SchemaPath())
	require.NotNil(t, loaded.Schema.Source)
	require.NotNil(t, loaded.Schema.Source.GithubDocs)
	assert.Equal(t, "fpt", loaded.Schema.Source.GithubDocs.Version)
	assert.Equal(
		t,
		[]string{filepath.Join(directory, "graphql", "**", "*.graphql")},
		loaded.OperationPaths(),
	)
	assert.Equal(t, filepath.Join(directory, "generated", "client.go"), loaded.GeneratedPath())
	assert.Equal(t, filepath.Join(directory, "generated", "operations.json"), loaded.ExportOperationsPath())
	assert.Equal(t, filepath.Join(directory, "generated", "testhandler.go"), loaded.TestHandlerGeneratedPath())
	assert.Equal(t, TestHandlerTypesLocal, loaded.TestHandlerTypesValue())
	require.NotNil(t, loaded.Package)
	assert.Equal(t, "githubapi", *loaded.Package)
	require.NotNil(t, loaded.Bindings)
	require.Contains(t, *loaded.Bindings, "DateTime")
	require.NotNil(t, (*loaded.Bindings)["DateTime"].Type)
	assert.Equal(t, "github.com/example/scalar.DateTime", *(*loaded.Bindings)["DateTime"].Type)
	require.Len(t, loaded.PackageBindings, 1)
	assert.Equal(t, "github.com/example/models", loaded.PackageBindings[0].Package)
	require.NotNil(t, loaded.Casing)
	require.NotNil(t, loaded.Casing.Enums)
	assert.Equal(t, "default", (*loaded.Casing.Enums)["IssueState"])
	require.NotNil(t, loaded.UseStructReferences)
	assert.True(t, *loaded.UseStructReferences)
	require.NotNil(t, loaded.OmitUnreferencedImplementations)
	assert.False(t, *loaded.OmitUnreferencedImplementations)
}

func TestDocumentationConfigParses(t *testing.T) {
	t.Parallel()

	filename := filepath.Join("..", "..", "..", "..", "docs", DefaultFilename)
	_, err := Load(filename)
	require.NoError(t, err)
}

func TestParseRequiresSchemaFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantError string
	}{
		{
			name: "schema",
			content: "operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema is required",
		},
		{
			name: "schema path",
			content: "schema: {}\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.path is required",
		},
		{
			name: "operations",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"generated: generated.go\n",
			wantError: "operations is required",
		},
		{
			name: "generated",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"operations: []\n",
			wantError: "generated is required",
		},
		{
			name: "test handler generated",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"operations: []\n" +
				"generated: generated.go\n" +
				"test_handler: {}\n",
			wantError: "test_handler.generated is required",
		},
		{
			name: "package binding package",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"operations: []\n" +
				"generated: generated.go\n" +
				"package_bindings:\n" +
				"  - {}\n",
			wantError: "package_bindings[0].package is required",
		},
		{
			name: "github docs version",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    github_docs:\n" +
				"      revision: " + testRevision + "\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.github_docs.version is required",
		},
		{
			name: "github docs revision",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    github_docs:\n" +
				"      version: fpt\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.github_docs.revision is required",
		},
		{
			name: "github repository repository",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    github_repository:\n" +
				"      revision: " + testRevision + "\n" +
				"      path: schema.graphql\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.github_repository.repository is required",
		},
		{
			name: "github repository revision",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    github_repository:\n" +
				"      repository: octo-org/octo-repo\n" +
				"      path: schema.graphql\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.github_repository.revision is required",
		},
		{
			name: "github repository path",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    github_repository:\n" +
				"      repository: octo-org/octo-repo\n" +
				"      revision: " + testRevision + "\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.github_repository.path is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse([]byte(test.content))

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
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

func TestUpdatePinRejectsSharedAlias(t *testing.T) {
	t.Parallel()

	content := []byte(
		"shared: &checksum " + testSHA256 + "\n" +
			"schema:\n" +
			"  path: .octoql/schema.graphql\n" +
			"  sha256: *checksum\n",
	)
	_, err := UpdatePin(content, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared YAML alias")
}

func TestUpdatePinRejectsAnchoredPin(t *testing.T) {
	t.Parallel()

	content := []byte(
		"schema:\n" +
			"  path: .octoql/schema.graphql\n" +
			"  sha256: &checksum " + testSHA256 + "\n" +
			"generated: *checksum\n",
	)
	_, err := UpdatePin(content, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not define a YAML anchor")
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
