package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema/githubapi/githubapitest"
	"github.com/willabides/octoql/internal"
)

const (
	cliRevision      = "45d83f459620340069df7c375a8867be62616d61"
	cliSchema        = "# preserved comment\ntype Query { viewer: String }\n"
	updatedRevision  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	updatedSchema    = "type Query { repository: String }\n"
	releaseSchemaURL = "https://github.com/WillAbides/octoql/releases/download/v1.2.3/octoqlgen.schema.yaml"
)

var (
	cliSchemaSHA256 = sha256Hex(cliSchema)
	updatedSHA256   = sha256Hex(updatedSchema)
)

func sha256Hex(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

type gitHubStubServer struct {
	graphql      *githubapitest.TestHandler
	materializer *schema.Materializer
	contents     map[string][]byte
	requests     atomic.Int32
}

func newGitHubStubServer(t *testing.T, contents map[string][]byte) *gitHubStubServer {
	graphql := githubapitest.NewTestHandler(t)
	stub := &gitHubStubServer{
		graphql:  graphql,
		contents: contents,
	}
	mux := http.NewServeMux()
	mux.Handle("/graphql", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		stub.requests.Add(1)
		graphql.ServeHTTP(writer, request)
	}))
	mux.HandleFunc("/repos/", func(writer http.ResponseWriter, request *http.Request) {
		stub.requests.Add(1)
		key := request.URL.Path + "?" + request.URL.RawQuery
		body, ok := stub.contents[key]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(body)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mat := schema.NewMaterializer()
	mat.HTTPClient = server.Client()
	mat.LookupEnvironment = func(name string) (string, bool) {
		if name == "GH_TOKEN" {
			return "github_pat_test", true
		}
		return "", false
	}
	mat.GitHubAPIBaseURL = func(string) string {
		return server.URL
	}
	mat.GitHubGraphQLEndpoint = func(string) string {
		return server.URL + "/graphql"
	}
	stub.materializer = mat
	return stub
}

func (s *gitHubStubServer) expectDocsCommit(path, oid string) {
	s.graphql.ExpectLatestCommit(githubapitest.LatestCommitVariables{
		Owner: "github",
		Name:  "docs",
		Path:  path,
	}).Respond(githubapitest.LatestCommitResponse{
		Repository: &githubapitest.LatestCommitRepository{
			DefaultBranchRef: &githubapitest.LatestCommitRepositoryDefaultBranchRef{
				Target: &githubapitest.LatestCommitRepositoryDefaultBranchRefTargetCommit{
					History: githubapitest.LatestCommitRepositoryDefaultBranchRefTargetCommitHistoryCommitHistoryConnection{
						Nodes: []*githubapitest.LatestCommitRepositoryDefaultBranchRefTargetCommitHistoryCommitHistoryConnectionNodesCommit{
							{Oid: oid},
						},
					},
				},
			},
		},
	})
}

func TestRunInit(t *testing.T) {
	t.Parallel()

	t.Run("creates config schema and gitignore for default and enterprise versions", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name       string
			extraArgs  []string
			schemaPath string
			gitignore  string
		}{
			{
				name:       "default fpt",
				schemaPath: "src/graphql/data/fpt/schema.docs.graphql",
			},
			{
				name:       "ghes preserves existing gitignore",
				extraArgs:  []string{"--schema-version", "ghes-3.21"},
				schemaPath: "src/graphql/data/ghes-3.21/schema.docs-enterprise.graphql",
				gitignore:  "keep\n",
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				directory := t.TempDir()
				configPath := filepath.Join(directory, "octoqlgen.yaml")
				gitignorePath := filepath.Join(directory, ".octoql", ".gitignore")
				if test.gitignore != "" {
					require.NoError(t, os.MkdirAll(filepath.Dir(gitignorePath), 0o755))
					require.NoError(t, os.WriteFile(gitignorePath, []byte(test.gitignore), 0o600))
				}

				remote := newGitHubStubServer(t, map[string][]byte{
					"/repos/github/docs/contents/" + test.schemaPath + "?ref=" + cliRevision: []byte(cliSchema),
				})
				remote.expectDocsCommit(test.schemaPath, cliRevision)

				var stdout bytes.Buffer
				args := append([]string{"init", "--config", configPath}, test.extraArgs...)
				err := Run(args, "v1.2.3", &Dependencies{
					Context:      t.Context(),
					Materializer: remote.materializer,
					Stdout:       &stdout,
				})
				require.NoError(t, err)

				configContent, err := os.ReadFile(configPath)
				require.NoError(t, err)
				assert.Equal(
					t,
					"# yaml-language-server: $schema="+releaseSchemaURL+"\n\n"+
						"schema:\n"+
						"  path: .octoql/schema.graphql\n"+
						"  sha256: "+cliSchemaSHA256+"\n"+
						"  source:\n"+
						"    repository: github/docs\n"+
						"    path: "+test.schemaPath+"\n"+
						"    revision: "+cliRevision+"\n"+
						"operations:\n"+
						"  - graphql/**/*.graphql\n"+
						"generated: internal/githubapi/generated.go\n",
					string(configContent),
				)
				localSchemaPath := filepath.Join(directory, ".octoql", "schema.graphql")
				writtenSchema, err := os.ReadFile(localSchemaPath)
				require.NoError(t, err)
				assert.Equal(t, cliSchema, string(writtenSchema))

				wantGitignore := "*\n!.gitignore\n"
				if test.gitignore != "" {
					wantGitignore = test.gitignore
				}
				writtenGitignore, err := os.ReadFile(gitignorePath)
				require.NoError(t, err)
				assert.Equal(t, wantGitignore, string(writtenGitignore))
				assert.Equal(t, fmt.Sprintf("created %s, %s, and %s\n", configPath, localSchemaPath, gitignorePath), stdout.String())

				if len(test.extraArgs) == 0 {
					require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module example.com/app\n\ngo 1.27.3\n"), 0o600))
					require.NoError(t, os.MkdirAll(filepath.Join(directory, "graphql"), 0o755))
					require.NoError(t, os.WriteFile(filepath.Join(directory, "graphql", "viewer.graphql"), []byte("query Viewer { viewer }\n"), 0o600))
					require.NoError(t, Run([]string{"generate", "--config", configPath}, "v1.2.3", &Dependencies{Context: t.Context()}))
					generated, readErr := os.ReadFile(filepath.Join(directory, "internal", "githubapi", "generated.go"))
					require.NoError(t, readErr)
					assert.Contains(t, string(generated), "func (c *Client) Viewer(")
				}
			})
		}
	})

	t.Run("refuses overwrites and rolls back on remote failure", func(t *testing.T) {
		t.Parallel()

		existingDir := t.TempDir()
		existingConfig := filepath.Join(existingDir, "octoqlgen.yaml")
		require.NoError(t, os.WriteFile(existingConfig, []byte("existing\n"), 0o600))
		remote := newGitHubStubServer(t, nil)
		err := Run([]string{"init", "--config", existingConfig}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: remote.materializer,
		})
		require.ErrorContains(t, err, "refusing to overwrite")
		assert.Zero(t, remote.requests.Load())

		conflictDir := t.TempDir()
		conflictSchema := filepath.Join(conflictDir, ".octoql", "schema.graphql")
		require.NoError(t, os.MkdirAll(filepath.Dir(conflictSchema), 0o755))
		require.NoError(t, os.WriteFile(conflictSchema, []byte("different\n"), 0o600))
		conflictPath := "src/graphql/data/fpt/schema.docs.graphql"
		conflictRemote := newGitHubStubServer(t, map[string][]byte{
			"/repos/github/docs/contents/" + conflictPath + "?ref=" + cliRevision: []byte(cliSchema),
		})
		conflictRemote.expectDocsCommit(conflictPath, cliRevision)
		conflictConfig := filepath.Join(conflictDir, "octoqlgen.yaml")
		err = Run([]string{"init", "--config", conflictConfig}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: conflictRemote.materializer,
		})
		require.ErrorContains(t, err, "refusing to overwrite existing schema")
		_, statErr := os.Stat(conflictConfig)
		require.ErrorIs(t, statErr, os.ErrNotExist)

		failedDir := t.TempDir()
		failedRemote := newGitHubStubServer(t, nil)
		failedRemote.expectDocsCommit(conflictPath, cliRevision)
		failedConfig := filepath.Join(failedDir, "octoqlgen.yaml")
		err = Run([]string{"init", "--config", failedConfig}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: failedRemote.materializer,
		})
		require.Error(t, err)
		_, statErr = os.Stat(failedConfig)
		require.ErrorIs(t, statErr, os.ErrNotExist)
		_, statErr = os.Stat(filepath.Join(failedDir, ".octoql", "schema.graphql"))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})
}

func TestRunSchema(t *testing.T) {
	t.Parallel()

	t.Run("defaults to fetch on local schema and writes output file from remote", func(t *testing.T) {
		t.Parallel()

		localDir := t.TempDir()
		localSchema := filepath.Join(localDir, "schema.graphql")
		localConfig := filepath.Join(localDir, "octoqlgen.yaml")
		require.NoError(t, os.WriteFile(localSchema, []byte(cliSchema), 0o600))
		require.NoError(t, os.WriteFile(localConfig, []byte(
			"schema:\n"+
				"  path: schema.graphql\n"+
				"  sha256: "+cliSchemaSHA256+"\n"+
				"operations: []\n"+
				"generated: generated.go\n",
		), 0o600))

		var stdout bytes.Buffer
		err := Run([]string{"schema", "--config", localConfig}, "v1.2.3", &Dependencies{
			Context: t.Context(),
			Stdout:  &stdout,
		})
		require.NoError(t, err)
		assert.Equal(t, cliSchema, stdout.String())

		remoteDir := t.TempDir()
		remoteConfig := filepath.Join(remoteDir, "octoqlgen.yaml")
		outputPath := filepath.Join(remoteDir, "out", "schema.graphql")
		require.NoError(t, os.WriteFile(remoteConfig, []byte(
			"schema:\n"+
				"  path: .octoql/schema.graphql\n"+
				"  sha256: "+cliSchemaSHA256+"\n"+
				"  source:\n"+
				"    repository: github/docs\n"+
				"    path: schema.graphql\n"+
				"    revision: "+cliRevision+"\n"+
				"operations: []\n"+
				"generated: generated.go\n",
		), 0o600))
		missingOutput := filepath.Join(remoteDir, "failed.graphql")
		failingRemote := newGitHubStubServer(t, nil)
		err = Run([]string{"schema", "fetch", "--config", remoteConfig, "-o", missingOutput}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: failingRemote.materializer,
		})
		require.Error(t, err)
		_, statErr := os.Stat(missingOutput)
		require.ErrorIs(t, statErr, os.ErrNotExist)

		remote := newGitHubStubServer(t, map[string][]byte{
			"/repos/github/docs/contents/schema.graphql?ref=" + cliRevision: []byte(cliSchema),
		})
		err = Run([]string{"schema", "fetch", "--config", remoteConfig, "-o", outputPath}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: remote.materializer,
		})
		require.NoError(t, err)
		written, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, cliSchema, string(written))
		companion, err := os.ReadFile(filepath.Join(filepath.Dir(outputPath), internal.DirectiveFilename))
		require.NoError(t, err)
		assert.Equal(t, internal.DirectiveFileContents, string(companion))
	})

	t.Run("updates remote pin refreshes unchanged directives and rejects local source", func(t *testing.T) {
		t.Parallel()

		directory := t.TempDir()
		configPath := filepath.Join(directory, "octoqlgen.yaml")
		schemaPath := filepath.Join(directory, "schema.graphql")
		require.NoError(t, os.WriteFile(schemaPath, []byte(cliSchema), 0o600))
		require.NoError(t, os.WriteFile(configPath, []byte(
			"# yaml-language-server: $schema=https://example.com/old.yaml\n\n"+
				"schema:\n"+
				"  path: schema.graphql\n"+
				"  sha256: "+cliSchemaSHA256+" # keep comment\n"+
				"  source:\n"+
				"    repository: github/docs\n"+
				"    path: src/graphql/data/fpt/schema.docs.graphql\n"+
				"    revision: "+cliRevision+"\n"+
				"operations: []\n"+
				"generated: generated.go\n",
		), 0o600))

		docPath := "src/graphql/data/fpt/schema.docs.graphql"
		remote := newGitHubStubServer(t, map[string][]byte{
			"/repos/github/docs/contents/" + docPath + "?ref=" + updatedRevision: []byte(updatedSchema),
		})
		remote.expectDocsCommit(docPath, updatedRevision)

		var stdout bytes.Buffer
		err := Run([]string{"schema", "update", "--config", configPath}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: remote.materializer,
			Stdout:       &stdout,
		})
		require.NoError(t, err)
		assert.Contains(t, stdout.String(), cliRevision+" -> "+updatedRevision)

		configText, err := os.ReadFile(configPath)
		require.NoError(t, err)
		assert.Contains(t, string(configText), "# yaml-language-server: $schema="+releaseSchemaURL+"\n")
		assert.Contains(t, string(configText), "sha256: "+updatedSHA256+" # keep comment")
		assert.Contains(t, string(configText), "revision: "+updatedRevision)
		writtenSchema, err := os.ReadFile(schemaPath)
		require.NoError(t, err)
		assert.Equal(t, updatedSchema, string(writtenSchema))

		// Second update with unchanged remote pin still refreshes $schema and companion directive.
		require.NoError(t, os.WriteFile(
			configPath,
			bytes.ReplaceAll(
				bytes.ReplaceAll(configText, []byte(releaseSchemaURL), []byte("https://example.com/stale.yaml")),
				[]byte("sha256: "+updatedSHA256),
				[]byte("sha256: &checksum "+updatedSHA256),
			),
			0o600,
		))
		_ = os.Remove(filepath.Join(directory, internal.DirectiveFilename))
		remote.expectDocsCommit(docPath, updatedRevision)
		stdout.Reset()
		err = Run([]string{"schema", "update", "--config", configPath}, "v1.2.3", &Dependencies{
			Context:      t.Context(),
			Materializer: remote.materializer,
			Stdout:       &stdout,
		})
		require.NoError(t, err)
		assert.Equal(t, "schema is unchanged\n", stdout.String())
		refreshedConfig, err := os.ReadFile(configPath)
		require.NoError(t, err)
		assert.Contains(t, string(refreshedConfig), releaseSchemaURL)
		companion, err := os.ReadFile(filepath.Join(directory, internal.DirectiveFilename))
		require.NoError(t, err)
		assert.Equal(t, internal.DirectiveFileContents, string(companion))

		localOnly := filepath.Join(t.TempDir(), "octoqlgen.yaml")
		require.NoError(t, os.WriteFile(localOnly, []byte(
			"schema:\n  path: schema.graphql\noperations: []\ngenerated: generated.go\n",
		), 0o600))
		err = Run([]string{"schema", "update", "--config", localOnly}, "v1.2.3", &Dependencies{
			Context: t.Context(),
		})
		require.ErrorContains(t, err, "requires a configured remote")
	})
}

func TestConfigSchemaURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  string
		revision string
		want     string
	}{
		{
			name:    "release",
			version: "v1.2.3",
			want:    releaseSchemaURL,
		},
		{
			name:    "development",
			version: "dev",
			want:    "https://raw.githubusercontent.com/WillAbides/octoql/main/octoqlgen.schema.yaml",
		},
		{
			name:     "clean development build",
			version:  "dev",
			revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			want: "https://raw.githubusercontent.com/WillAbides/octoql/" +
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/octoqlgen.schema.yaml",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, configSchemaURL(test.version, test.revision))
		})
	}
}

func TestHelpSnapshots(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "root", args: []string{"--help"}},
		{name: "generate", args: []string{"generate", "--help"}},
		{name: "init", args: []string{"init", "--help"}},
		{name: "schema", args: []string{"schema", "--help"}},
		{name: "schema-fetch", args: []string{"schema", "fetch", "--help"}},
		{name: "schema-update", args: []string{"schema", "update", "--help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := strings.TrimRight(parseCLIExit(t, "test", test.args), "\n")
			switch test.name {
			case "root":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen <command> [flags]

Generate GraphQL client code for a given schema and queries.

Flags:
  -h, --help       Show context-sensitive help.
      --version    Show version information.

Commands:
  generate [flags]
    Generate GraphQL client code.

  init [flags]
    Create an octoqlgen configuration and fetch its schema.

  schema fetch [flags]
    Fetch or verify a pinned GraphQL schema.

  schema update [flags]
    Fetch the latest configured GitHub schema and update its revision and
    checksum.

Run "octoqlgen <command> --help" for more information on a command.`))
			case "generate":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen generate [flags]

Generate GraphQL client code.

Flags:
  -h, --help           Show context-sensitive help.
      --version        Show version information.

      --config=PATH    Path to an octoqlgen configuration file.`))
			case "init":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen init [flags]

Create an octoqlgen configuration and fetch its schema.

Flags:
  -h, --help                      Show context-sensitive help.
      --version                   Show version information.

      --config=PATH               Path for the new octoqlgen configuration.
      --schema-version=VERSION    GitHub Docs schema version (fpt, ghec,
                                  or ghes-X.Y). Defaults to fpt.`))
			case "schema":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen schema <command> [flags]

Fetch or verify a pinned GraphQL schema.

Flags:
  -h, --help       Show context-sensitive help.
      --version    Show version information.

Commands:
  schema fetch [flags]
    Fetch or verify a pinned GraphQL schema.

  schema update [flags]
    Fetch the latest configured GitHub schema and update its revision and
    checksum.`))
			case "schema-update":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen schema update [flags]

Fetch the latest configured GitHub schema and update its revision and checksum.

Flags:
  -h, --help           Show context-sensitive help.
      --version        Show version information.

      --config=PATH    Path to an octoqlgen configuration file.`))
			case "schema-fetch":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen schema fetch [flags]

Fetch or verify a pinned GraphQL schema.

Flags:
  -h, --help           Show context-sensitive help.
      --version        Show version information.

      --config=PATH    Path to an octoqlgen configuration file. Defaults to
                       octoqlgen.yaml.
  -o, --output=PATH    Write the exact schema bytes to a file instead of stdout.`))
			default:
				t.Fatalf("missing inline snapshot for %s", test.name)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "v1.2.3\n", parseCLIExit(t, "v1.2.3", []string{"--version"}))
}

type exitCode int

func parseCLIExit(t *testing.T, version string, args []string) string {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	dependencies := &Dependencies{
		Context: t.Context(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	dependencies.setDefaults()
	command := newCommandTree(dependencies)
	parser, err := newParser(
		command,
		version,
		kong.Writers(&stdout, &stderr),
		kong.Exit(func(code int) {
			panic(exitCode(code))
		}),
	)
	require.NoError(t, err)
	assertExitCode(t, 0, func() {
		_, _ = parser.Parse(args)
	})
	return stdout.String() + stderr.String()
}

func assertExitCode(t *testing.T, expected int, function func()) {
	t.Helper()

	defer func() {
		recovered := recover()
		code, ok := recovered.(exitCode)
		require.True(t, ok, "expected parser exit, recovered %v", recovered)
		assert.Equal(t, exitCode(expected), code)
	}()
	function()
	t.Fatal("expected parser exit")
}
