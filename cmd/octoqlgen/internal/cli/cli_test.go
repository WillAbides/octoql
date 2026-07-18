// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/internal/testutil"
)

const (
	cliRevision = "45d83f459620340069df7c375a8867be62616d61"
	cliSHA256   = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
)

func TestSchemaCommandRunConfiguredStdout(t *testing.T) {
	t.Parallel()

	materializer := &stubMaterializer{data: []byte("exact schema bytes\n")}
	var stdout bytes.Buffer
	command := SchemaCommand{
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
	command := SchemaCommand{
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
		command       SchemaCommand
		expectedError string
	}{
		{
			name: "multiple direct sources",
			command: SchemaCommand{
				GitHubVersion: "fpt",
				SourceURL:     "https://example.test/schema.graphql",
			},
			expectedError: "mutually exclusive",
		},
		{
			name: "missing checksum",
			command: SchemaCommand{
				SourceURL: "https://example.test/schema.graphql",
			},
			expectedError: "--sha256 is required",
		},
		{
			name: "missing github revision",
			command: SchemaCommand{
				GitHubVersion: "fpt",
				SHA256:        cliSHA256,
			},
			expectedError: "--revision is required",
		},
		{
			name: "url with revision",
			command: SchemaCommand{
				SourceURL: "https://example.test/schema.graphql",
				Revision:  cliRevision,
				SHA256:    cliSHA256,
			},
			expectedError: "--revision is only valid",
		},
		{
			name: "checksum without direct source",
			command: SchemaCommand{
				SHA256: cliSHA256,
			},
			expectedError: "--revision and --sha256 require",
		},
		{
			name: "config with direct source",
			command: SchemaCommand{
				Config:    "octoql.yaml",
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
	command := SchemaCommand{
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

func TestGenerateCommandRun(t *testing.T) {
	t.Parallel()

	var filename string
	command := GenerateCommand{
		ConfigFilename: "genqlient.yaml",
		run: func(value string) error {
			filename = value
			return nil
		},
	}
	err := command.Run()
	require.NoError(t, err)
	assert.Equal(t, "genqlient.yaml", filename)
}

func TestHelpSnapshots(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "root", args: []string{"--help"}},
		{name: "schema", args: []string{"schema", "--help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

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
			testutil.Cupaloy.SnapshotT(t, output)
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
