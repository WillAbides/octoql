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
	cliRevision = "45d83f459620340069df7c375a8867be62616d61"
	cliSHA256   = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

func TestSchemaCommandRunConfiguredStdout(t *testing.T) {
	t.Parallel()

	materializer := &stubMaterializer{data: []byte("exact schema bytes\n")}
	var stdout bytes.Buffer
	command := schemaMaterializeCommand{
		Config:  "custom.yaml",
		context: t.Context(),
		loadConfig: func(filename string) (*config.Config, error) {
			assert.Equal(t, "custom.yaml", filename)
			return &config.Config{
				Schema: config.Schema{
					Path:   ".octoql/schema.graphql",
					Sha256: new(cliSHA256),
					Source: new(config.Source{Url: new("https://example.test/schema.graphql")}),
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

func TestSchemaCommandRunDirectOutput(t *testing.T) {
	t.Parallel()

	materializer := &stubMaterializer{data: []byte("exact schema bytes\n")}
	outputWriter := &stubOutputWriter{}
	var stdout bytes.Buffer
	command := schemaMaterializeCommand{
		Output:        "schema.graphql",
		GitHubVersion: "ghec",
		Revision:      cliRevision,
		SHA256:        cliSHA256,
		context:       t.Context(),
		loadConfig: func(string) (*config.Config, error) {
			return nil, errors.New("config should not be loaded")
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
	require.NotNil(t, materializer.request.Source.GithubDocs)
	assert.Equal(t, "ghec", materializer.request.Source.GithubDocs.Version)
	assert.Equal(t, cliRevision, materializer.request.Source.GithubDocs.Revision)
}

func TestSchemaCommandDirectValidation(t *testing.T) {
	tests := []struct {
		name          string
		command       schemaMaterializeCommand
		expectedError string
	}{
		{
			name: "multiple direct sources",
			command: schemaMaterializeCommand{
				GitHubVersion: "fpt",
				SourceURL:     "https://example.test/schema.graphql",
			},
			expectedError: "mutually exclusive",
		},
		{
			name: "missing checksum",
			command: schemaMaterializeCommand{
				SourceURL: "https://example.test/schema.graphql",
			},
			expectedError: "--sha256 is required",
		},
		{
			name: "missing github revision",
			command: schemaMaterializeCommand{
				GitHubVersion: "fpt",
				SHA256:        cliSHA256,
			},
			expectedError: "--revision is required",
		},
		{
			name: "url with revision",
			command: schemaMaterializeCommand{
				SourceURL: "https://example.test/schema.graphql",
				Revision:  cliRevision,
				SHA256:    cliSHA256,
			},
			expectedError: "--revision is only valid",
		},
		{
			name: "checksum without direct source",
			command: schemaMaterializeCommand{
				SHA256: cliSHA256,
			},
			expectedError: "--revision and --sha256 require",
		},
		{
			name: "config with direct source",
			command: schemaMaterializeCommand{
				Config:    "octoqlgen.yaml",
				SourceURL: "https://example.test/schema.graphql",
				SHA256:    cliSHA256,
			},
			expectedError: "--config cannot be combined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command := test.command
			command.context = t.Context()
			command.loadConfig = config.Load
			command.materializer = &stubMaterializer{}
			command.outputWriter = &stubOutputWriter{}
			command.stdout = bytes.NewBuffer(nil)

			err := command.Run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
		})
	}
}

func TestSchemaCommandMaterializeFailureDoesNotWriteOutput(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("materialize failed")
	outputWriter := &stubOutputWriter{}
	command := schemaMaterializeCommand{
		Output:    "schema.graphql",
		SourceURL: "https://example.test/schema.graphql",
		SHA256:    cliSHA256,
		context:   t.Context(),
		loadConfig: func(string) (*config.Config, error) {
			return nil, errors.New("config should not be loaded")
		},
		materializer: &stubMaterializer{err: expectedErr},
		outputWriter: outputWriter,
		stdout:       bytes.NewBuffer(nil),
	}

	err := command.Run()
	require.ErrorIs(t, err, expectedErr)
	assert.Empty(t, outputWriter.path)
}

func TestRunSchemaDefaultsToMaterialize(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	materializer := &stubMaterializer{data: []byte("schema")}
	err := Run(
		[]string{
			"schema",
			"--source-url",
			"https://example.test/schema.graphql",
			"--sha256",
			cliSHA256,
		},
		"test",
		&Dependencies{
			Context:      t.Context(),
			Materializer: materializer,
			Stdout:       &stdout,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "schema", stdout.String())
	assert.Equal(t, "https://example.test/schema.graphql", *materializer.request.Source.Url)
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
	command := initCommand{
		ConfigPath: configPath,
		stdout:     &stdout,
	}

	err := command.Run()
	require.NoError(t, err)
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(
		t,
		"schema:\n  path: .octoql/schema.graphql\noperations:\n  - graphql/**/*.graphql\ngenerated: internal/githubapi/generated.go\n",
		string(content),
	)
	gitignorePath := filepath.Join(filepath.Dir(configPath), ".octoql", ".gitignore")
	gitignore, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, "*\n!.gitignore\n", string(gitignore))
	assert.Equal(t, "created "+configPath+" and "+gitignorePath+"\n", stdout.String())
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
		ConfigPath: filepath.Join(directory, "nested", "octoqlgen.yaml"),
		stdout:     io.Discard,
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
		ConfigPath: configPath,
		stdout:     io.Discard,
	}

	err = command.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to overwrite")
}

func TestSchemaUpdateCommandRejectsLocalSource(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "octoqlgen.yaml")
	err := os.WriteFile(configPath, []byte("schema:\n  path: schema.graphql\n"), 0o600)
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

func TestSchemaUpdateCommandDoesNotRollbackAfterSchemaPublication(t *testing.T) {
	configWriteErr := errors.New("config write failed")
	publishedConfigErr := errors.New("published config invalid")
	tests := []struct {
		name                string
		configWriteErr      error
		publishedConfigErr  error
		expectedError       error
		expectedErrorString string
	}{
		{
			name:                "config publication failure",
			configWriteErr:      configWriteErr,
			expectedError:       configWriteErr,
			expectedErrorString: "config update failed",
		},
		{
			name:                "published config validation failure",
			publishedConfigErr:  publishedConfigErr,
			expectedError:       publishedConfigErr,
			expectedErrorString: "validating published config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			configPath := filepath.Join(directory, "octoqlgen.yaml")
			schemaPath := filepath.Join(directory, "schema.graphql")
			oldSHA256 := strings.Repeat("a", 64)
			configContent := []byte("schema:\n  path: schema.graphql\n  sha256: " +
				oldSHA256 + "\n  source:\n    url: https://example.test/schema.graphql\n")
			err := os.WriteFile(configPath, configContent, 0o600)
			require.NoError(t, err)
			canonicalConfigPath, err := canonicalPath(configPath)
			require.NoError(t, err)
			err = os.WriteFile(schemaPath, []byte("type Query { old: String }\n"), 0o600)
			require.NoError(t, err)

			loaded := &config.Config{
				Schema: config.Schema{
					Path:   schemaPath,
					Sha256: &oldSHA256,
					Source: new(config.Source{Url: new("https://example.test/schema.graphql")}),
				},
			}
			outputWriter := &schemaUpdateOutputWriter{
				configPath: canonicalConfigPath,
				configErr:  test.configWriteErr,
			}
			loadCalls := 0
			command := schemaUpdateCommand{
				Config:  configPath,
				context: t.Context(),
				loadConfig: func(string) (*config.Config, error) {
					loadCalls++
					if loadCalls == 2 && test.publishedConfigErr != nil {
						return nil, test.publishedConfigErr
					}
					return loaded, nil
				},
				resolver: &stubRemoteResolver{
					result: schema.RemoteResult{
						Data:     []byte("type Query { updated: String }\n"),
						Revision: cliRevision,
						SHA256:   cliSHA256,
					},
				},
				outputWriter: outputWriter,
				stdout:       io.Discard,
			}

			err = command.Run()
			require.Error(t, err)
			assert.ErrorIs(t, err, test.expectedError)
			assert.Contains(t, err.Error(), test.expectedErrorString)
			assert.Equal(t, []string{schemaPath, canonicalConfigPath}, outputWriter.paths)
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
    Create an octoqlgen configuration and materialized schema.

  schema materialize [flags]
    Materialize or verify a pinned GraphQL schema.

  schema update [flags]
    Update a configured remote schema pin.

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

Create an octoqlgen configuration and materialized schema.

Flags:
  -h, --help           Show context-sensitive help.
      --version        Show version information.

      --config=PATH    Path for the new octoqlgen configuration.`))
			case "schema":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen schema <command> [flags]

Materialize or verify a pinned GraphQL schema.

Flags:
  -h, --help       Show context-sensitive help.
      --version    Show version information.

Commands:
  schema materialize [flags]
    Materialize or verify a pinned GraphQL schema.

  schema update [flags]
    Update a configured remote schema pin.`))
			case "schema-update":
				snaps.MatchInlineSnapshot(t, output, snaps.Inline(`Usage: octoqlgen schema update [flags]

Update a configured remote schema pin.

Flags:
  -h, --help           Show context-sensitive help.
      --version        Show version information.

      --config=PATH    Path to an octoqlgen configuration file.`))
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
	request schema.Request,
) ([]byte, error) {
	m.request = request
	return m.data, m.err
}

type stubOutputWriter struct {
	err  error
	path string
	data []byte
}

func (w *stubOutputWriter) Write(path string, data []byte) error {
	w.path = path
	w.data = append([]byte{}, data...)
	return w.err
}

type schemaUpdateOutputWriter struct {
	configPath string
	configErr  error
	paths      []string
}

func (w *schemaUpdateOutputWriter) Write(path string, _ []byte) error {
	w.paths = append(w.paths, path)
	if path == w.configPath {
		return w.configErr
	}
	return nil
}

type stubRemoteResolver struct {
	err    error
	result schema.RemoteResult
}

func (r *stubRemoteResolver) Resolve(
	_ context.Context,
	_ config.Source,
) (schema.RemoteResult, error) {
	return r.result, r.err
}
