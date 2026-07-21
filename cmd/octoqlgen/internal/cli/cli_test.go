package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
)

const (
	cliRevision     = "45d83f459620340069df7c375a8867be62616d61"
	cliSHA256       = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
	cliSchema       = "type Query { viewer: String }\n"
	cliSchemaSHA256 = "76d5d8240ac50f1721905f16acfb5674556feab2da90eff33e82755bcf701dfb"
	updatedRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	updatedSchema   = "type Query { repository: String }\n"
	updatedSHA256   = "b082461f59dcec42fd647c52ea85b373f052a824814813d78b71d48c3fc47bc4"
)

func TestSchemaCommandRunConfiguredStdout(t *testing.T) {
	t.Parallel()

	materializer := &stubMaterializer{data: []byte("exact schema bytes\n")}
	var stdout bytes.Buffer
	command := schemaFetchCommand{
		Config:  "custom.yaml",
		context: t.Context(),
		loadConfig: func(filename string) (*config.Config, error) {
			assert.Equal(t, "custom.yaml", filename)
			return &config.Config{
				Schema: config.Schema{
					Path:   ".octoql/schema.graphql",
					Sha256: new(cliSHA256),
					Source: &config.Source{
						Repository: "octo-org/octo-repo",
						Path:       "schema.graphql",
						Revision:   cliRevision,
					},
				},
			}, nil
		},
		materializer: materializer,
		outputWriter: &stubOutputWriter{},
		stdout:       &stdout,
	}

	err := command.Run()
	require.NoError(t, err)
	assert.Equal(t, "exact schema bytes\n", stdout.String())
	assert.Equal(t, ".octoql/schema.graphql", materializer.request.Path)
	assert.Equal(t, cliSHA256, materializer.request.SHA256)
}

func TestSchemaCommandRunConfiguredOutput(t *testing.T) {
	t.Parallel()

	materializer := &stubMaterializer{data: []byte("exact schema bytes\n")}
	outputWriter := &stubOutputWriter{}
	var stdout bytes.Buffer
	command := schemaFetchCommand{
		Output:  "schema.graphql",
		context: t.Context(),
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				Schema: config.Schema{
					Path:   ".octoql/schema.graphql",
					Sha256: new(cliSHA256),
					Source: &config.Source{
						Repository: "github/docs",
						Path:       "src/graphql/data/ghec/schema.docs.graphql",
						Revision:   cliRevision,
					},
				},
			}, nil
		},
		materializer: materializer,
		outputWriter: outputWriter,
		stdout:       &stdout,
	}

	err := command.Run()
	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Equal(t, "schema.graphql", outputWriter.path)
	assert.Equal(t, []byte("exact schema bytes\n"), outputWriter.data)
	assert.Equal(t, "github/docs", materializer.request.Source.Repository)
	assert.Equal(t, "src/graphql/data/ghec/schema.docs.graphql", materializer.request.Source.Path)
	assert.Equal(t, cliRevision, materializer.request.Source.Revision)
}

func TestSchemaCommandFetchFailureDoesNotWriteOutput(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("fetch failed")
	outputWriter := &stubOutputWriter{}
	command := schemaFetchCommand{
		Output:  "schema.graphql",
		context: t.Context(),
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{Schema: config.Schema{Path: "configured.graphql"}}, nil
		},
		materializer: &stubMaterializer{err: expectedErr},
		outputWriter: outputWriter,
		stdout:       bytes.NewBuffer(nil),
	}

	err := command.Run()
	require.ErrorIs(t, err, expectedErr)
	assert.Empty(t, outputWriter.path)
}

func TestRunSchemaDefaultsToFetch(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	materializer := &stubMaterializer{data: []byte("schema")}
	err := Run(
		[]string{"schema"},
		"test",
		&Dependencies{
			Context: t.Context(),
			LoadConfig: func(filename string) (*config.Config, error) {
				assert.Empty(t, filename)
				return &config.Config{Schema: config.Schema{Path: "schema.graphql"}}, nil
			},
			Materializer: materializer,
			Stdout:       &stdout,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "schema", stdout.String())
	assert.Equal(t, "schema.graphql", materializer.request.Path)
}

func TestAtomicOutputWriter(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "nested", "schema.graphql")
	writer := atomicOutputWriter{}
	err := writer.Write(destination, []byte("first"))
	require.NoError(t, err)
	err = writer.Write(destination, []byte("second"))
	require.NoError(t, err)

	data, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, []byte("second"), data)
	tempFiles, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".schema.graphql.tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, tempFiles)
}

func TestInitCommandRun(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "octoqlgen.yaml")
	var stdout bytes.Buffer
	resolver := &stubRemoteResolver{
		result: schema.RemoteResult{
			Revision: cliRevision,
			SHA256:   cliSchemaSHA256,
			Data:     []byte(cliSchema),
		},
	}
	command := initCommand{
		ConfigPath:    configPath,
		SchemaVersion: "fpt",
		context:       t.Context(),
		resolver:      resolver,
		stdout:        &stdout,
	}

	err := command.Run()
	require.NoError(t, err)
	assert.Equal(t, "github/docs", resolver.source.Repository)
	assert.Equal(t, "src/graphql/data/fpt/schema.docs.graphql", resolver.source.Path)
	assert.Empty(t, resolver.source.Revision)
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(
		t,
		"schema:\n"+
			"  path: .octoql/schema.graphql\n"+
			"  sha256: "+cliSchemaSHA256+"\n"+
			"  source:\n"+
			"    repository: github/docs\n"+
			"    path: src/graphql/data/fpt/schema.docs.graphql\n"+
			"    revision: "+cliRevision+"\n"+
			"operations:\n"+
			"  - graphql/**/*.graphql\n"+
			"generated: internal/githubapi/generated.go\n",
		string(content),
	)
	gitignorePath := filepath.Join(filepath.Dir(configPath), ".octoql", ".gitignore")
	gitignore, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, "*\n!.gitignore\n", string(gitignore))
	schemaPath := filepath.Join(filepath.Dir(configPath), ".octoql", "schema.graphql")
	schemaContent, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Equal(t, cliSchema, string(schemaContent))
	assert.Equal(t, "created "+configPath+", "+schemaPath+", and "+gitignorePath+"\n", stdout.String())
}

func TestRunInitSchemaVersion(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectedPath string
	}{
		{
			name:         "default",
			expectedPath: "src/graphql/data/fpt/schema.docs.graphql",
		},
		{
			name:         "explicit",
			args:         []string{"--schema-version", "ghes-3.21"},
			expectedPath: "src/graphql/data/ghes-3.21/schema.docs-enterprise.graphql",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			configPath := filepath.Join(t.TempDir(), "octoqlgen.yaml")
			resolver := &stubRemoteResolver{
				result: schema.RemoteResult{
					Revision: cliRevision,
					SHA256:   cliSchemaSHA256,
					Data:     []byte(cliSchema),
				},
			}
			args := []string{"init", "--config", configPath}
			args = append(args, test.args...)
			err := Run(
				args,
				"test",
				&Dependencies{
					Context:        t.Context(),
					RemoteResolver: resolver,
				},
			)
			require.NoError(t, err)
			assert.Equal(t, "github/docs", resolver.source.Repository)
			assert.Equal(t, test.expectedPath, resolver.source.Path)
			assert.Empty(t, resolver.source.Revision)
		})
	}
}

func TestInitCommandPreservesExistingGitignore(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	gitignorePath := filepath.Join(directory, ".octoql", ".gitignore")
	err := os.MkdirAll(filepath.Dir(gitignorePath), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(gitignorePath, []byte("keep\n"), 0o600)
	require.NoError(t, err)
	command := initCommand{
		ConfigPath:    filepath.Join(directory, "octoqlgen.yaml"),
		SchemaVersion: "fpt",
		context:       t.Context(),
		resolver: &stubRemoteResolver{
			result: schema.RemoteResult{
				Revision: cliRevision,
				SHA256:   cliSchemaSHA256,
				Data:     []byte(cliSchema),
			},
		},
		stdout: io.Discard,
	}

	err = command.Run()
	require.NoError(t, err)
	gitignore, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(gitignore))
}

func TestInitCommandRefusesExistingConfig(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "octoqlgen.yaml")
	err := os.WriteFile(configPath, []byte("existing\n"), 0o600)
	require.NoError(t, err)
	command := initCommand{
		ConfigPath:    configPath,
		SchemaVersion: "fpt",
		context:       t.Context(),
		resolver: &stubRemoteResolver{
			err: errors.New("resolver should not be called"),
		},
		stdout: io.Discard,
	}

	err = command.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to overwrite")
}

func TestInitCommandResolverFailureDoesNotCreateFiles(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	expectedErr := errors.New("resolve failed")
	command := initCommand{
		ConfigPath:    filepath.Join(directory, "octoqlgen.yaml"),
		SchemaVersion: "fpt",
		context:       t.Context(),
		resolver:      &stubRemoteResolver{err: expectedErr},
		stdout:        io.Discard,
	}

	err := command.Run()
	require.ErrorIs(t, err, expectedErr)
	_, configErr := os.Stat(command.ConfigPath)
	require.ErrorIs(t, configErr, os.ErrNotExist)
	_, schemaErr := os.Stat(filepath.Join(directory, ".octoql", "schema.graphql"))
	require.ErrorIs(t, schemaErr, os.ErrNotExist)
}

func TestInitCommandRefusesExistingDifferentSchema(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	schemaPath := filepath.Join(directory, ".octoql", "schema.graphql")
	err := os.MkdirAll(filepath.Dir(schemaPath), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, []byte("existing\n"), 0o600)
	require.NoError(t, err)
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	command := initCommand{
		ConfigPath:    configPath,
		SchemaVersion: "fpt",
		context:       t.Context(),
		resolver: &stubRemoteResolver{
			result: schema.RemoteResult{
				Revision: cliRevision,
				SHA256:   cliSchemaSHA256,
				Data:     []byte(cliSchema),
			},
		},
		stdout: io.Discard,
	}

	err = command.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to overwrite existing schema")
	_, configErr := os.Stat(configPath)
	require.ErrorIs(t, configErr, os.ErrNotExist)
	content, readErr := os.ReadFile(schemaPath)
	require.NoError(t, readErr)
	assert.Equal(t, "existing\n", string(content))
}

func TestSchemaUpdateCommandRejectsLocalSource(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "octoqlgen.yaml")
	err := os.WriteFile(
		configPath,
		[]byte(
			"schema:\n"+
				"  path: schema.graphql\n"+
				"operations: []\n"+
				"generated: generated.go\n",
		),
		0o600,
	)
	require.NoError(t, err)
	command := schemaUpdateCommand{
		Config:     configPath,
		context:    t.Context(),
		loadConfig: config.Load,
		stdout:     io.Discard,
	}

	err = command.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a configured remote")
}

func TestSchemaUpdateCommandRefreshesRepositoryPin(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	err := os.WriteFile(schemaPath, []byte(cliSchema), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(
		configPath,
		[]byte(
			"schema:\n"+
				"  path: schema.graphql\n"+
				"  sha256: "+cliSchemaSHA256+"\n"+
				"  source:\n"+
				"    repository: github/docs\n"+
				"    path: src/graphql/data/fpt/schema.docs.graphql\n"+
				"    revision: "+cliRevision+"\n"+
				"operations: []\n"+
				"generated: generated.go\n",
		),
		0o600,
	)
	require.NoError(t, err)
	resolver := &stubRemoteResolver{
		result: schema.RemoteResult{
			Revision: updatedRevision,
			SHA256:   updatedSHA256,
			Data:     []byte(updatedSchema),
		},
	}
	command := schemaUpdateCommand{
		Config:       configPath,
		context:      t.Context(),
		loadConfig:   config.Load,
		resolver:     resolver,
		outputWriter: atomicOutputWriter{},
		stdout:       io.Discard,
	}

	err = command.Run()
	require.NoError(t, err)
	assert.Equal(t, "github/docs", resolver.source.Repository)
	assert.Equal(t, "src/graphql/data/fpt/schema.docs.graphql", resolver.source.Path)
	assert.Equal(t, cliRevision, resolver.source.Revision)
	updated, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, updatedSHA256, updated.Schema.SHA256Value())
	assert.Equal(t, updatedRevision, updated.Schema.Source.Revision)
	schemaContent, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Equal(t, updatedSchema, string(schemaContent))
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
				"test",
				kong.Writers(&stdout, &stderr),
				kong.Exit(func(code int) {
					panic(exitCode(code))
				}),
			)
			require.NoError(t, err)
			assertExitCode(t, 0, func() {
				_, _ = parser.Parse(test.args)
			})
			output := strings.TrimRight(stdout.String()+stderr.String(), "\n")
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

	var stdout bytes.Buffer
	dependencies := &Dependencies{
		Context: t.Context(),
		Stdout:  &stdout,
	}
	dependencies.setDefaults()
	command := newCommandTree(dependencies)
	parser, err := newParser(
		command,
		"v1.2.3",
		kong.Writers(&stdout, io.Discard),
		kong.Exit(func(code int) {
			panic(exitCode(code))
		}),
	)
	require.NoError(t, err)
	assertExitCode(t, 0, func() {
		_, _ = parser.Parse([]string{"--version"})
	})
	assert.Equal(t, "v1.2.3\n", stdout.String())
}

type exitCode int

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

type stubMaterializer struct {
	err     error
	request schema.Request
	data    []byte
}

func (m *stubMaterializer) Materialize(
	_ context.Context,
	request *schema.Request,
) ([]byte, error) {
	m.request = *request
	return m.data, m.err
}

type stubOutputWriter struct {
	err  error
	path string
	data []byte
}

type stubRemoteResolver struct {
	err    error
	result schema.RemoteResult
	source config.Source
}

func (r *stubRemoteResolver) Resolve(_ context.Context, source config.Source) (schema.RemoteResult, error) {
	r.source = source
	return r.result, r.err
}

func (w *stubOutputWriter) Write(path string, data []byte) error {
	w.path = path
	w.data = append([]byte{}, data...)
	return w.err
}
