package cli

import (
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
			[]byte("query Viewer($filter: FilterInput) {\n  viewer(filter: $filter)\n  state\n}\n"),
			0o600,
		))

		remoteSchema := "enum IssueState { OPEN }\n" +
			"scalar CustomID\n" +
			"input FilterInput { label: String }\n" +
			"type Query { viewer(filter: FilterInput): CustomID! state: IssueState! }\n"
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
				"export_operations: githubapi/operations.json\n"+
				"context_type: \"-\"\n"+
				"bindings:\n"+
				"  CustomID:\n"+
				"    type: string\n"+
				"casing:\n"+
				"  default: auto_camel_case\n"+
				"  enums:\n"+
				"    IssueState: raw\n"+
				"use_struct_references: true\n"+
				"omit_unreferenced_implementations: false\n"+
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
		assert.Contains(t, string(clientSource), "context.Background()")
		assert.Contains(t, string(clientSource), "IssueState_OPEN")

		handlerSource, err := os.ReadFile(filepath.Join(directory, "githubapitest", "generated.go"))
		require.NoError(t, err)
		assert.Contains(t, string(handlerSource), "package githubapitest")
		assert.Contains(t, string(handlerSource), "type ViewerResponse struct")

		manifest, err := os.ReadFile(filepath.Join(directory, "githubapi", "operations.json"))
		require.NoError(t, err)
		assert.Contains(t, string(manifest), `"operationName": "Viewer"`)
		assert.Contains(t, string(manifest), filepath.Join("graphql", "viewer.graphql"))
		assert.NotContains(t, string(manifest), directory)

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
