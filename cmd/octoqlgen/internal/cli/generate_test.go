package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/internal"
)

func TestRunGenerate(t *testing.T) {
	t.Parallel()

	t.Run("materializes remote schema maps config options and writes all outputs", func(t *testing.T) {
		t.Parallel()

		directory := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(directory, "go.mod"),
			[]byte("module example.com/app\n\ngo 1.27.3\n"),
			0o600,
		))
		operationDir := filepath.Join(directory, "graphql")
		require.NoError(t, os.MkdirAll(operationDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(operationDir, "viewer.graphql"),
			[]byte("query Viewer($filter: FilterInput) {\n"+
				"  viewer(filter: $filter)\n"+
				"  state\n"+
				"  echo(value: \"contains ` backtick\")\n"+
				"  ip_address_list { snake_case_field }\n"+
				"  uri\n"+
				"}\n"),
			0o600,
		))

		remoteSchema := "enum IssueState { OPEN }\n" +
			"scalar CustomID\n" +
			"scalar URI\n" +
			"type snake_case_type { snake_case_field: String! }\n" +
			"input FilterInput { label: String }\n" +
			"type Query {\n" +
			"  viewer(filter: FilterInput): CustomID!\n" +
			"  state: IssueState!\n" +
			"  echo(value: String!): String!\n" +
			"  ip_address_list: snake_case_type!\n" +
			"  uri: URI!\n" +
			"}\n"
		remoteSHA256 := sha256Hex(remoteSchema)

		configPath := filepath.Join(directory, config.DefaultFilename)
		require.NoError(t, os.WriteFile(configPath, []byte(
			"schema:\n"+
				"  path: .octoql/schema.graphql\n"+
				"  sha256: "+remoteSHA256+"\n"+
				"  source:\n"+
				"    repository: octo-org/octo-repo\n"+
				"    path: schema.graphql\n"+
				"    revision: "+cliRevision+"\n"+
				"operations:\n"+
				"  - graphql/**/*.graphql\n"+
				"generated: githubapi/generated.go\n"+
				"package: githubapi\n"+
				"bindings:\n"+
				"  CustomID: string\n"+
				"  URI: net/url.URL\n"+
				"test_handler:\n"+
				"  generated: githubapitest/generated.go\n"+
				"  types: local\n",
		), 0o600))

		remote := newGitHubStubServer(t, map[string][]byte{
			"/repos/octo-org/octo-repo/contents/schema.graphql?ref=" + cliRevision: []byte(remoteSchema),
		})
		err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: remote.materializer,
		})
		require.NoError(t, err)

		clientSource, err := os.ReadFile(filepath.Join(directory, "githubapi", "generated.go"))
		require.NoError(t, err)
		assert.Contains(t, string(clientSource), "package githubapi")
		assert.Contains(t, string(clientSource), "func (c *Client) Viewer(")
		assert.Contains(t, string(clientSource), "ctx context.Context")
		assert.Contains(t, string(clientSource), "IssueStateOpen")
		assert.Contains(t, string(clientSource), `"net/url"`)
		assert.Contains(t, string(clientSource), "url.URL")
		assert.Contains(t, string(clientSource), `Query:         "\nquery Viewer`)
		assert.Contains(t, string(clientSource), "contains ` backtick")
		assert.NotContains(t, string(clientSource), "Query:         `")
		assert.NotContains(t, string(clientSource), "Viewer_Operation")

		handlerSource, err := os.ReadFile(filepath.Join(directory, "githubapitest", "generated.go"))
		require.NoError(t, err)
		assert.Contains(t, string(handlerSource), "package githubapitest")
		assert.Contains(t, string(handlerSource), "type ViewerResponse struct")

		companion, err := os.ReadFile(filepath.Join(directory, ".octoql", internal.DirectiveFilename))
		require.NoError(t, err)
		assert.Equal(t, internal.DirectiveFileContents, string(companion))
	})

	t.Run("validates output collisions before materialization", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name      string
			yaml      string
			wantParts []string
		}{
			{
				name:      "config output collision",
				yaml:      "schema:\n  path: schema.graphql\noperations:\n  - op.graphql\ngenerated: " + config.DefaultFilename + "\n",
				wantParts: []string{"generated output path", "config"},
			},
			{
				name: "schema output collision",
				yaml: "schema:\n" +
					"  path: generated.go\n" +
					"  sha256: " + cliSchemaSHA256 + "\n" +
					"  source:\n" +
					"    repository: octo-org/octo-repo\n" +
					"    path: schema.graphql\n" +
					"    revision: " + cliRevision + "\n" +
					"operations: []\n" +
					"generated: generated.go\n",
				wantParts: []string{"generated output path", "schema input path"},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				directory := t.TempDir()
				configPath := filepath.Join(directory, config.DefaultFilename)
				require.NoError(t, os.WriteFile(configPath, []byte(test.yaml), 0o600))
				remote := newGitHubStubServer(t, nil)

				err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
					Context:      t.Context(),
					Materializer: remote.materializer,
				})
				require.Error(t, err)
				for _, part := range test.wantParts {
					assert.Contains(t, err.Error(), part)
				}
				assert.Zero(t, remote.requests.Load())
			})
		}
	})

	t.Run("rejects missing local schema and writes nothing on invalid operations", func(t *testing.T) {
		t.Parallel()

		directory := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(directory, "go.mod"),
			[]byte("module example.com/app\n\ngo 1.27.3\n"),
			0o600,
		))
		configPath := filepath.Join(directory, config.DefaultFilename)
		generatedPath := filepath.Join(directory, "client", "generated.go")
		require.NoError(t, os.WriteFile(configPath, []byte(
			"schema:\n  path: schema.graphql\noperations:\n  - op.graphql\ngenerated: client/generated.go\n",
		), 0o600))

		err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
			Context: t.Context(),
		})
		require.ErrorContains(t, err, "local schema file")
		assert.Contains(t, err.Error(), "edit schema.source or run octoqlgen schema fetch")

		err = Run([]string{"generate", "genqlient.yaml"}, "v1.2.3", &Dependencies{
			Context: t.Context(),
		})
		require.ErrorContains(t, err, "unexpected argument")

		require.NoError(t, os.WriteFile(filepath.Join(directory, "schema.graphql"), []byte(cliSchema), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "op.graphql"), []byte("query Broken { unknown }\n"), 0o600))
		err = Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
			Context: t.Context(),
		})
		require.ErrorContains(t, err, `Cannot query field "unknown"`)
		_, statErr := os.Stat(generatedPath)
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("resolves parent-relative paths from subdirectory config", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()
		subDir := filepath.Join(rootDir, "subpackage")
		require.NoError(t, os.MkdirAll(subDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(rootDir, "go.mod"), []byte("module example.com/app\n\ngo 1.27.3\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(rootDir, "schema.graphql"), []byte(cliSchema), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(rootDir, "viewer.graphql"), []byte("query Viewer { viewer }\n"), 0o600))
		configPath := filepath.Join(subDir, config.DefaultFilename)
		require.NoError(t, os.WriteFile(configPath, []byte(
			"schema:\n  path: ../schema.graphql\noperations:\n  - ../viewer.graphql\ngenerated: generated.go\n",
		), 0o600))

		err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
			Context: t.Context(),
		})
		require.NoError(t, err)
		generated, err := os.ReadFile(filepath.Join(subDir, "generated.go"))
		require.NoError(t, err)
		assert.Contains(t, string(generated), "package subpackage")
	})
}

func TestRunGeneratePackageAndGlobDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("discovers module packages and emits mismatch warning", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name        string
			moduleDecl  string
			subDir      string
			packageYAML string
			existingGo  string
			wantPackage string
			wantWarning string
		}{
			{
				name:        "versioned module with comment",
				moduleDecl:  "module example.com/app/v2 // app comment",
				wantPackage: "app",
			},
			{
				name:        "explicit package for non-identifier module",
				moduleDecl:  "module example.com/foo-bar",
				packageYAML: "package: foobar\n",
				wantPackage: "foobar",
			},
			{
				name:        "subdirectory fallback for non-identifier module",
				moduleDecl:  "module example.com/foo-bar",
				subDir:      "client",
				wantPackage: "client",
			},
			{
				name:        "explicit package overrides sibling go file with warning",
				moduleDecl:  "module example.com/client",
				packageYAML: "package: override\n",
				existingGo:  "package client\n",
				wantPackage: "override",
				wantWarning: "package setting in octoqlgen.yaml 'override' looks wrong",
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				dir := filepath.Join(t.TempDir(), test.subDir)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(test.moduleDecl+"\n\ngo 1.27.3\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(cliSchema), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "op.graphql"), []byte("query Q { viewer }\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "generated.go"), []byte("not a package clause\n"), 0o600))
				if test.existingGo != "" {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.go"), []byte(test.existingGo), 0o600))
				}
				configPath := filepath.Join(dir, config.DefaultFilename)
				require.NoError(t, os.WriteFile(configPath, []byte(
					"schema:\n  path: schema.graphql\noperations:\n  - op.graphql\ngenerated: generated.go\n"+
						test.packageYAML+"test_handler:\n  generated: githubapitest/generated.go\n",
				), 0o600))

				var stderr bytes.Buffer
				err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
					Context: t.Context(),
					Stderr:  &stderr,
				})
				require.NoError(t, err)
				generated, err := os.ReadFile(filepath.Join(dir, "generated.go"))
				require.NoError(t, err)
				assert.Contains(t, string(generated), "package "+test.wantPackage)
				if test.wantWarning == "" {
					assert.Empty(t, stderr.String())
					return
				}
				assert.Contains(t, stderr.String(), test.wantWarning)
			})
		}
	})

	t.Run("deduplicates operation globs and excludes schema and directive files", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.27.3\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(cliSchema), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, internal.DirectiveFilename), []byte(internal.DirectiveFileContents), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.graphql"), []byte("query A { viewer }\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "z.graphql"), []byte("query Z { viewer }\n"), 0o600))
		configPath := filepath.Join(dir, config.DefaultFilename)
		require.NoError(t, os.WriteFile(configPath, []byte(
			"schema:\n  path: schema.graphql\noperations:\n  - z.graphql\n  - \"**/*.graphql\"\n  - a.graphql\ngenerated: generated.go\n",
		), 0o600))

		err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{Context: t.Context()})
		require.NoError(t, err)
		generated, err := os.ReadFile(filepath.Join(dir, "generated.go"))
		require.NoError(t, err)
		assert.Contains(t, string(generated), "func (c *Client) A(")
		assert.Contains(t, string(generated), "func (c *Client) Z(")

		for name, onlyCase := range map[string]struct {
			schemaFile string
			opContent  string
			wantErr    string
		}{
			"schema is only match": {
				schemaFile: "schema.graphql",
				wantErr:    "no queries found",
			},
			"companion directive is only match": {
				schemaFile: "schema.graphqls",
				wantErr:    "did not match any files",
			},
			"malformed operation file": {
				schemaFile: "schema.graphql",
				opContent:  "type Mutation { viewer: String }\n",
				wantErr:    "invalid query-spec file",
			},
		} {
			caseDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(caseDir, "go.mod"), []byte("module example.com/app\n\ngo 1.27.3\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(caseDir, onlyCase.schemaFile), []byte(cliSchema), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(caseDir, internal.DirectiveFilename), []byte(internal.DirectiveFileContents), 0o600))
			if onlyCase.opContent != "" {
				require.NoError(t, os.WriteFile(filepath.Join(caseDir, "bad.graphql"), []byte(onlyCase.opContent), 0o600))
			}
			caseConfig := filepath.Join(caseDir, config.DefaultFilename)
			require.NoError(t, os.WriteFile(caseConfig, []byte(
				"schema:\n  path: "+onlyCase.schemaFile+"\noperations:\n  - \"**/*.graphql\"\ngenerated: generated.go\n",
			), 0o600))
			err = Run([]string{"generate", "--config", caseConfig}, "v1.2.3", &Dependencies{Context: t.Context()})
			require.ErrorContainsf(t, err, onlyCase.wantErr, "case %s", name)
		}
	})
}

func TestRunGenerateConfigErrors(t *testing.T) {
	t.Parallel()

	const baseYAML = "schema:\n  path: schema.graphql\noperations:\n  - op.graphql\ngenerated: client/generated.go\n"
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "yaml alias collision",
			yaml:    "schema:\n  path: schema.graphql\noperations: []\ngenerated: &out client/generated.go\ntest_handler:\n  generated: *out\n",
			wantErr: "generated and test_handler.generated output paths must be different",
		},
		{
			name:    "duplicate yaml key",
			yaml:    "schema:\n  path: a.graphql\n  path: b.graphql\noperations: []\ngenerated: g.go\n",
			wantErr: `mapping key "path" already defined`,
		},
		{
			name:    "unknown root field",
			yaml:    baseYAML + "unknown: true\n",
			wantErr: "field unknown not found in type config.Config",
		},
		{
			name:    "nested binding mapping rejected",
			yaml:    baseYAML + "bindings:\n  DateTime:\n    type: time.Time\n",
			wantErr: "cannot construct !!map into string",
		},
		{
			name:    "empty binding value",
			yaml:    baseYAML + "bindings:\n  DateTime: \"\"\n",
			wantErr: "bindings.DateTime must not be empty",
		},
		{
			name:    "invalid test handler strategy",
			yaml:    baseYAML + "test_handler:\n  generated: h/generated.go\n  types: invalid\n",
			wantErr: `test_handler.types must be one of "client" or "local"`,
		},
		{
			name:    "multiple yaml documents",
			yaml:    baseYAML + "---\n{}\n",
			wantErr: "expected single document, found multiple",
		},
		{
			name:    "missing schema",
			yaml:    "operations: []\ngenerated: g.go\n",
			wantErr: "schema is required",
		},
		{
			name:    "missing schema path",
			yaml:    "schema: {}\noperations: []\ngenerated: g.go\n",
			wantErr: "schema.path is required",
		},
		{
			name:    "missing operations",
			yaml:    "schema:\n  path: s.graphql\ngenerated: g.go\n",
			wantErr: "operations is required",
		},
		{
			name:    "missing generated",
			yaml:    "schema:\n  path: s.graphql\noperations: []\n",
			wantErr: "generated is required",
		},
		{
			name:    "missing test handler generated",
			yaml:    baseYAML + "test_handler: {}\n",
			wantErr: "test_handler.generated is required",
		},
		{
			name:    "missing source repository",
			yaml:    "schema:\n  path: s.graphql\n  source:\n    path: s.graphql\n    revision: " + cliRevision + "\noperations: []\ngenerated: g.go\n",
			wantErr: "schema.source.repository is required",
		},
		{
			name:    "missing source revision",
			yaml:    "schema:\n  path: s.graphql\n  source:\n    repository: o/r\n    path: s.graphql\noperations: []\ngenerated: g.go\n",
			wantErr: "schema.source.revision is required",
		},
		{
			name:    "missing source path",
			yaml:    "schema:\n  path: s.graphql\n  source:\n    repository: o/r\n    revision: " + cliRevision + "\noperations: []\ngenerated: g.go\n",
			wantErr: "schema.source.path is required",
		},
		{
			name:    "invalid go package identifier",
			yaml:    baseYAML + "package: invalid-package\n",
			wantErr: "invalid package in octoqlgen.yaml",
		},
		{
			name:    "test handler in client package",
			yaml:    baseYAML + "package: client\ntest_handler:\n  generated: client/handler.go\n",
			wantErr: "separate package",
		},
		{
			name:    "test handler in invalid package directory",
			yaml:    baseYAML + "package: client\ntest_handler:\n  generated: bad-package-name/handler.go\n",
			wantErr: "unable to identify test handler package",
		},
		{
			name:    "client types with main package",
			yaml:    baseYAML + "package: main\ntest_handler:\n  generated: githubapitest/generated.go\n",
			wantErr: "cannot import a generated client in package main",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.27.3\n"), 0o600))
			configPath := filepath.Join(dir, config.DefaultFilename)
			require.NoError(t, os.WriteFile(configPath, []byte(test.yaml), 0o600))

			err := Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{
				Context: t.Context(),
			})
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
