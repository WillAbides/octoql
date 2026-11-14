package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v4"
)

const (
	testRevision = "45d83f459620340069df7c375a8867be62616d61"
	testSHA256   = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

func TestLoadStrict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		assertLoaded func(*testing.T, *Config)
		name         string
		content      string
		wantError    string
	}{
		{
			name: "aliases",
			content: "schema:\n" +
				"  path: .octoql/schema.graphql\n" +
				"  sha256: &checksum " + testSHA256 + "\n" +
				"  source:\n" +
				"    repository: &repository github/docs\n" +
				"    path: &schema_path src/graphql/data/fpt/schema.docs.graphql\n" +
				"    revision: &revision " + testRevision + "\n" +
				"operations:\n" +
				"  - &operation graphql/**/*.graphql\n" +
				"  - *operation\n" +
				"generated: &generated githubapi/generated.go\n" +
				"test_handler:\n" +
				"  generated: *generated\n",
			assertLoaded: func(t *testing.T, loaded *Config) {
				require.Len(t, loaded.Operations, 2)
				assert.Equal(t, loaded.Operations[0], loaded.Operations[1])
				require.NotNil(t, loaded.TestHandler)
				assert.Equal(t, loaded.Generated, loaded.TestHandler.Generated)
			},
		},
		{
			name: "merge",
			content: "schema:\n" +
				"  <<: &schema_defaults\n" +
				"    source:\n" +
				"      repository: github/docs\n" +
				"      path: src/graphql/data/fpt/schema.docs.graphql\n" +
				"      revision: " + testRevision + "\n" +
				"  path: .octoql/schema.graphql\n" +
				"  sha256: " + testSHA256 + "\n" +
				"operations:\n" +
				"  - graphql/**/*.graphql\n" +
				"generated: githubapi/generated.go\n" +
				"test_handler:\n" +
				"  generated: githubapitest/generated.go\n",
		},
		{
			name: "duplicate key",
			content: "schema:\n" +
				"  path: .octoql/schema.graphql\n" +
				"  path: duplicate.graphql\n" +
				"operations:\n" +
				"  - graphql/**/*.graphql\n" +
				"generated: githubapi/generated.go\n" +
				"test_handler:\n" +
				"  generated: githubapitest/generated.go\n",
			wantError: `mapping key "path" already defined`,
		},
		{
			name:      "unknown field",
			content:   validConfigYAML() + "unknown: true\n",
			wantError: "field unknown not found in type config.Config",
		},
		{
			name:      "removed extensions",
			content:   validConfigYAML() + "use_extensions: true\n",
			wantError: "field use_extensions not found in type config.Config",
		},
		{
			name: "unknown binding field",
			content: validConfigYAML() +
				"bindings:\n" +
				"  DateTime:\n" +
				"    unknown: true\n",
			wantError: "field unknown not found in type config.Binding",
		},
		{
			name: "unknown casing field",
			content: validConfigYAML() +
				"casing:\n" +
				"  unknown: raw\n",
			wantError: "field unknown not found in type config.Casing",
		},
		{
			name:      "invalid test handler type strategy",
			content:   validConfigYAML() + "  types: invalid\n",
			wantError: `test_handler.types must be one of "client" or "local"`,
		},
		{
			name:      "multiple documents",
			content:   validConfigYAML() + "---\n{}\n",
			wantError: "expected single document, found multiple",
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

// TestDocumentationYAMLBlocksParse keeps the generated configuration reference
// honest: every YAML example it shows must use option names the config model
// actually recognizes.
func TestDocumentationYAMLBlocksParse(t *testing.T) {
	t.Parallel()

	filename := filepath.Join("..", "..", "..", "..", "docs", "configuration.md")
	content, err := os.ReadFile(filename)
	require.NoError(t, err)

	blocks := yamlBlocks(string(content))
	require.NotEmpty(t, blocks, "expected at least one yaml example")

	for i, block := range blocks {
		loaded := &Config{}
		err = yaml.Load([]byte(block), loaded, yaml.WithKnownFields())
		assert.NoError(t, err, "yaml block %d:\n%s", i+1, block)
	}
}

// yamlBlocks returns the contents of every fenced yaml block in a Markdown
// document.
func yamlBlocks(document string) []string {
	var blocks []string
	var current []string
	inBlock := false

	for line := range strings.SplitSeq(document, "\n") {
		if !inBlock {
			if strings.TrimSpace(line) == "```yaml" {
				inBlock = true
				current = nil
			}
			continue
		}
		if strings.TrimSpace(line) == "```" {
			inBlock = false
			blocks = append(blocks, strings.Join(current, "\n"))
			continue
		}
		current = append(current, line)
	}

	return blocks
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
			name: "source repository",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    path: schema.graphql\n" +
				"    revision: " + testRevision + "\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.repository is required",
		},
		{
			name: "source revision",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    repository: octo-org/octo-repo\n" +
				"    path: schema.graphql\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.revision is required",
		},
		{
			name: "source path",
			content: "schema:\n" +
				"  path: schema.graphql\n" +
				"  source:\n" +
				"    repository: octo-org/octo-repo\n" +
				"    revision: " + testRevision + "\n" +
				"operations: []\n" +
				"generated: generated.go\n",
			wantError: "schema.source.path is required",
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
			"    repository: github/docs\n" +
			"    path: src/graphql/data/fpt/schema.docs.graphql\n" +
			"    revision: '" + testRevision + "'\n" +
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
			"    repository: github/docs\n"+
			"    path: src/graphql/data/fpt/schema.docs.graphql\n"+
			"    revision: '"+newRevision+"'\n"+
			"operations: [graphql/**/*.graphql]\n"+
			"generated: internal/githubapi/generated.go\n",
		string(updated),
	)
}

func TestSetSchemaDirective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "adds directive",
			content: "schema:\n  path: schema.graphql\n",
			want: "# yaml-language-server: $schema=https://example.com/octoqlgen.schema.yaml\n\n" +
				"schema:\n  path: schema.graphql\n",
		},
		{
			name: "updates directive",
			content: "# yaml-language-server: $schema=https://example.com/old.yaml\n\n" +
				"# keep this comment\nschema:\n  path: schema.graphql\n",
			want: "# yaml-language-server: $schema=https://example.com/octoqlgen.schema.yaml\n\n" +
				"# keep this comment\nschema:\n  path: schema.graphql\n",
		},
		{
			name: "preserves CRLF",
			content: "# yaml-language-server: $schema=https://example.com/old.yaml\r\n\r\n" +
				"schema:\r\n  path: schema.graphql\r\n",
			want: "# yaml-language-server: $schema=https://example.com/octoqlgen.schema.yaml\r\n\r\n" +
				"schema:\r\n  path: schema.graphql\r\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			updated := SetSchemaDirective(
				[]byte(test.content),
				"https://example.com/octoqlgen.schema.yaml",
			)
			assert.Equal(t, test.want, string(updated))
		})
	}
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

func validConfigYAML() string {
	return "schema:\n" +
		"  path: .octoql/schema.graphql\n" +
		"  sha256: " + testSHA256 + "\n" +
		"  source:\n" +
		"    repository: github/docs\n" +
		"    path: src/graphql/data/fpt/schema.docs.graphql\n" +
		"    revision: " + testRevision + "\n" +
		"operations:\n" +
		"  - graphql/**/*.graphql\n" +
		"generated: githubapi/generated.go\n" +
		"test_handler:\n" +
		"  generated: githubapitest/generated.go\n"
}
