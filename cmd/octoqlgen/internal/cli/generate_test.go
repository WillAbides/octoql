// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/generate"
)

func TestGenerateConfigMapsOptions(t *testing.T) {
	t.Parallel()

	exportOperations := "operations.json"
	packageName := "githubapi"
	contextType := "github.com/example/context.Type"
	clientGetter := "github.com/example/client.Get"
	optional := "generic"
	optionalGenericType := "github.com/example/optional.Value"
	useStructReferences := true
	omitUnreferencedImplementations := false
	bindingType := "github.com/example/scalar.DateTime"
	expectExactFields := "{ id login }"
	marshaler := "github.com/example/scalar.Marshal"
	unmarshaler := "github.com/example/scalar.Unmarshal"
	defaultCasing := "auto_camel_case"
	allEnumsCasing := "raw"
	enumCasing := map[string]string{"IssueState": "default"}
	bindings := map[string]*config.Binding{
		"DateTime": {
			Type:              &bindingType,
			ExpectExactFields: &expectExactFields,
			Marshaler:         &marshaler,
			Unmarshaler:       &unmarshaler,
		},
	}
	source := &config.Config{
		Schema: config.Schema{
			Path: "schema.graphql",
		},
		Operations:       []string{"graphql/**/*.graphql"},
		Generated:        "githubapi/generated.go",
		Package:          &packageName,
		ExportOperations: &exportOperations,
		ContextType:      &contextType,
		ClientGetter:     &clientGetter,
		Bindings:         &bindings,
		PackageBindings: []config.PackageBinding{{
			Package: "github.com/example/models",
		}},
		Casing: &config.Casing{
			Default:  &defaultCasing,
			AllEnums: &allEnumsCasing,
			Enums:    &enumCasing,
		},
		Optional:                        &optional,
		OptionalGenericType:             &optionalGenericType,
		UseStructReferences:             &useStructReferences,
		OmitUnreferencedImplementations: &omitUnreferencedImplementations,
		TestHandler: &config.TestHandler{
			Generated: "githubapitest/generated.go",
		},
	}

	actual := generateConfig(source)

	assert.Equal(t, generate.StringList{"schema.graphql"}, actual.Schema)
	assert.Equal(t, generate.StringList{"graphql/**/*.graphql"}, actual.Operations)
	assert.Equal(t, "githubapi/generated.go", actual.Generated)
	assert.Equal(t, "githubapitest/generated.go", actual.TestHandlerGenerated)
	assert.Equal(t, packageName, actual.Package)
	assert.Equal(t, exportOperations, actual.ExportOperations)
	assert.Equal(t, contextType, actual.ContextType)
	assert.Equal(t, clientGetter, actual.ClientGetter)
	assert.Equal(t, optional, actual.Optional)
	assert.Equal(t, optionalGenericType, actual.OptionalGenericType)
	assert.True(t, actual.StructReferences)
	require.NotNil(t, actual.OmitUnreferencedImplementations)
	assert.False(t, *actual.OmitUnreferencedImplementations)
	assert.Equal(t, generate.CasingAutoCamelCase, actual.Casing.Default)
	assert.Equal(t, generate.CasingRaw, actual.Casing.AllEnums)
	assert.Equal(t, generate.CasingDefault, actual.Casing.Enums["IssueState"])
	require.Len(t, actual.PackageBindings, 1)
	assert.Equal(t, "github.com/example/models", actual.PackageBindings[0].Package)
	require.Contains(t, actual.Bindings, "DateTime")
	assert.Equal(t, bindingType, actual.Bindings["DateTime"].Type)
	assert.Equal(t, expectExactFields, actual.Bindings["DateTime"].ExpectExactFields)
	assert.Equal(t, marshaler, actual.Bindings["DateTime"].Marshaler)
	assert.Equal(t, unmarshaler, actual.Bindings["DateTime"].Unmarshaler)
}

func TestGenerateConfigMapsTestHandler(t *testing.T) {
	t.Parallel()

	localTypes := config.TestHandlerTypesLocal
	source := &config.Config{
		Schema:     config.Schema{Path: "schema.graphql"},
		Operations: []string{"operation.graphql"},
		Generated:  "generated.go",
		TestHandler: &config.TestHandler{
			Generated: "githubapitest/generated.go",
			Types:     &localTypes,
		},
	}

	actual := generateConfig(source)

	assert.Equal(t, "githubapitest/generated.go", actual.TestHandlerGenerated)
	assert.Equal(t, generate.TestHandlerTypesLocal, actual.TestHandlerTypes)
}

func TestGenerateCommandRun(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	schemaPath := filepath.Join(tempDir, "schema.graphql")
	generatedPath := filepath.Join(tempDir, "generated.go")
	exportPath := filepath.Join(tempDir, "operations.json")
	packageName := "client"
	exportOperations := exportPath
	loaded := &config.Config{
		Schema:           config.Schema{Path: schemaPath},
		Operations:       []string{filepath.Join(tempDir, "operation.graphql")},
		Generated:        generatedPath,
		Package:          &packageName,
		ExportOperations: &exportOperations,
	}

	materializer := &stubMaterializer{data: []byte("type Query { viewer: String }\n")}
	writer := &recordingOutputWriter{outputs: map[string][]byte{}, writes: map[string]int{}}
	didMaterialize := false
	generateCalls := 0
	command := generateCommand{
		Config:  "custom-octoqlgen.yaml",
		context: t.Context(),
		loadConfig: func(filename string) (*config.Config, error) {
			assert.Equal(t, "custom-octoqlgen.yaml", filename)
			return loaded, nil
		},
		materializer: materializer,
		generate: func(generatorConfig *generate.Config) (map[string][]byte, error) {
			generateCalls++
			didMaterialize = materializer.request.Path != ""
			assert.Equal(t, generate.StringList{schemaPath}, generatorConfig.Schema)
			return map[string][]byte{
				generatedPath: []byte("generated"),
				exportPath:    []byte("operations"),
			}, nil
		},
		outputWriter: writer,
	}

	err := command.Run()
	require.NoError(t, err)
	assert.True(t, didMaterialize)
	assert.Equal(t, 1, generateCalls)
	assert.Equal(t, 1, writer.writes[generatedPath])
	assert.Equal(t, 1, writer.writes[exportPath])
	assert.Equal(t, []byte("generated"), writer.outputs[generatedPath])
	assert.Equal(t, []byte("operations"), writer.outputs[exportPath])
}

func TestGenerateCommandRendererFailureWritesNothing(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	require.NoError(t, err)
	tempRoot := filepath.Join(repositoryRoot, "generate", "testdata", "tmp")
	require.NoError(t, os.MkdirAll(tempRoot, 0o755))
	tempDir, err := os.MkdirTemp(tempRoot, "cli-renderer-error-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(tempDir))
	})

	packageName := "client"
	loaded := &config.Config{
		Schema: config.Schema{
			Path: filepath.Join(
				repositoryRoot,
				"generate",
				"testdata",
				"queries",
				"schema.graphql",
			),
		},
		Operations: []string{filepath.Join(
			repositoryRoot,
			"generate",
			"testdata",
			"queries",
			"Repository.graphql",
		)},
		Generated: filepath.Join(tempDir, "client", "generated.go"),
		Package:   &packageName,
		TestHandler: &config.TestHandler{
			Generated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		},
	}
	renderErr := errors.New("handler renderer failed")
	writer := &recordingOutputWriter{
		outputs: map[string][]byte{},
		writes:  map[string]int{},
	}
	command := generateCommand{
		Config:  "octoqlgen.yaml",
		context: t.Context(),
		loadConfig: func(string) (*config.Config, error) {
			return loaded, nil
		},
		materializer: &stubMaterializer{},
		generate: func(*generate.Config) (map[string][]byte, error) {
			return nil, renderErr
		},
		outputWriter: writer,
	}

	err = command.Run()

	require.ErrorIs(t, err, renderErr)
	assert.Empty(t, writer.writes)
	assert.Empty(t, writer.outputs)
}

func TestGenerateCommandMaterializesConfiguredSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source config.Source
		assert func(*testing.T, config.Source)
		name   string
	}{
		{
			name: "github docs",
			source: config.Source{
				GithubDocs: &config.GithubDocs{
					Version:  "fpt",
					Revision: cliRevision,
				},
			},
			assert: func(t *testing.T, source config.Source) {
				require.NotNil(t, source.GithubDocs)
				assert.Equal(t, cliRevision, source.GithubDocs.Revision)
			},
		},
		{
			name: "github repository",
			source: config.Source{
				GithubRepository: &config.GithubRepository{
					Repository: "octo-org/octo-repo",
					Revision:   cliRevision,
					Path:       "schema.graphql",
				},
			},
			assert: func(t *testing.T, source config.Source) {
				require.NotNil(t, source.GithubRepository)
				assert.Equal(t, "octo-org/octo-repo", source.GithubRepository.Repository)
			},
		},
		{
			name: "url",
			source: config.Source{
				Url: new("https://example.test/schema.graphql"),
			},
			assert: func(t *testing.T, source config.Source) {
				require.NotNil(t, source.Url)
				assert.Equal(t, "https://example.test/schema.graphql", *source.Url)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			packageName := "client"
			materializer := &stubMaterializer{}
			command := generateCommand{
				context: t.Context(),
				loadConfig: func(string) (*config.Config, error) {
					return &config.Config{
						Schema: config.Schema{
							Path:   filepath.Join(directory, "schema.graphql"),
							Sha256: new(cliSHA256),
							Source: &test.source,
						},
						Operations: []string{filepath.Join(directory, "operation.graphql")},
						Generated:  filepath.Join(directory, "generated.go"),
						Package:    &packageName,
					}, nil
				},
				materializer: materializer,
				generate: func(*generate.Config) (map[string][]byte, error) {
					return map[string][]byte{}, nil
				},
				outputWriter: &recordingOutputWriter{},
			}

			err := command.Run()
			require.NoError(t, err)
			assert.Equal(t, cliSHA256, materializer.request.SHA256)
			test.assert(t, materializer.request.Source)
		})
	}
}

func TestGenerateSubdirectoryConfig(t *testing.T) {
	t.Parallel()

	configPath, err := filepath.Abs(
		filepath.Join(
			"..",
			"..",
			"..",
			"..",
			"generate",
			"testdata",
			"queries",
			"subpackage",
			config.DefaultFilename,
		),
	)
	require.NoError(t, err)
	materializer := &stubMaterializer{}
	command := generateCommand{
		Config:       configPath,
		context:      t.Context(),
		loadConfig:   config.Load,
		materializer: materializer,
		generate: func(generatorConfig *generate.Config) (map[string][]byte, error) {
			assert.Equal(t, "subpackage", generatorConfig.Package)
			assert.Equal(
				t,
				filepath.Join(filepath.Dir(configPath), "generated.go"),
				generatorConfig.Generated,
			)
			assert.Equal(
				t,
				generate.StringList{filepath.Join(filepath.Dir(configPath), "..", "schema.graphql")},
				generatorConfig.Schema,
			)
			return map[string][]byte{}, nil
		},
		outputWriter: &recordingOutputWriter{},
	}

	err = command.Run()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(filepath.Dir(configPath), "..", "schema.graphql"), materializer.request.Path)
}

func TestGenerateLocalHandlerSubdirectoryConfig(t *testing.T) {
	t.Parallel()

	configPath, err := filepath.Abs(
		filepath.Join(
			"..",
			"..",
			"..",
			"..",
			"internal",
			"handlertest",
			"localfixture",
			config.DefaultFilename,
		),
	)
	require.NoError(t, err)
	configDirectory := filepath.Dir(configPath)
	clientPath := filepath.Clean(filepath.Join(configDirectory, "..", "client", "generated.go"))
	handlerPath := filepath.Join(configDirectory, "githubapitest", "generated.go")
	writer := &recordingOutputWriter{}
	command := generateCommand{
		Config:       configPath,
		context:      t.Context(),
		loadConfig:   config.Load,
		materializer: schemaMaterializer(),
		generate:     generate.Generate,
		outputWriter: writer,
	}

	err = command.Run()
	require.NoError(t, err)
	require.Contains(t, writer.outputs, clientPath)
	require.Contains(t, writer.outputs, handlerPath)
	assert.Contains(t, string(writer.outputs[clientPath]), "package githubapi")
	assert.Contains(t, string(writer.outputs[handlerPath]), "package githubapitest")
	assert.NotContains(
		t,
		string(writer.outputs[handlerPath]),
		"github.com/willabides/octoql/internal/handlertest/client",
	)
}

func TestGenerateCommandMissingLocalSchema(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	packageName := "client"
	command := generateCommand{
		Config:  filepath.Join(tempDir, "octoqlgen.yaml"),
		context: t.Context(),
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				Schema:     config.Schema{Path: filepath.Join(tempDir, "missing.graphql")},
				Operations: []string{filepath.Join(tempDir, "operation.graphql")},
				Generated:  filepath.Join(tempDir, "generated.go"),
				Package:    &packageName,
			}, nil
		},
		materializer: schemaMaterializer(),
		generate: func(*generate.Config) (map[string][]byte, error) {
			return nil, errors.New("generation should not run")
		},
		outputWriter: &recordingOutputWriter{},
	}

	err := command.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local schema file")
	assert.Contains(t, err.Error(), "edit schema.source or run octoqlgen schema materialize")
}

func TestGenerateRejectsPositionalConfig(t *testing.T) {
	t.Parallel()

	err := Run(
		[]string{"generate", "genqlient.yaml"},
		"test",
		&Dependencies{
			Context: t.Context(),
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected argument")
}

func TestGenerateDoesNotDiscoverLegacyConfig(t *testing.T) {
	directory := t.TempDir()
	legacyOctoqlFilename := "octoql" + ".yaml"
	for _, filename := range []string{"genqlient.yaml", legacyOctoqlFilename} {
		err := os.WriteFile(filepath.Join(directory, filename), []byte("legacy\n"), 0o600)
		require.NoError(t, err)
	}
	originalDirectory, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(directory)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalDirectory))
	})

	expectedErr := errors.New("stop after load")
	err = Run(
		[]string{"generate"},
		"test",
		&Dependencies{
			Context: t.Context(),
			Stdout:  io.Discard,
			Stderr:  io.Discard,
			LoadConfig: func(filename string) (*config.Config, error) {
				assert.Equal(t, config.DefaultFilename, filepath.Base(filename))
				assert.NotEqual(t, "genqlient.yaml", filepath.Base(filename))
				assert.NotEqual(t, legacyOctoqlFilename, filepath.Base(filename))
				return nil, expectedErr
			},
		},
	)

	require.ErrorIs(t, err, expectedErr)
}

func TestMinimalInitConfigGenerates(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, config.DefaultFilename)
	initCmd := initCommand{
		ConfigPath: configPath,
		stdout:     io.Discard,
	}
	err := initCmd.Run()
	require.NoError(t, err)
	err = os.WriteFile(
		filepath.Join(directory, ".octoql", "schema.graphql"),
		[]byte("type Query { viewer: User }\ntype User { login: String! }\n"),
		0o600,
	)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(directory, "graphql"), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(
		filepath.Join(directory, "graphql", "viewer.graphql"),
		[]byte("query Viewer { viewer { login } }\n"),
		0o600,
	)
	require.NoError(t, err)

	err = Run(
		[]string{"generate", "--config", configPath},
		"test",
		&Dependencies{
			Context: t.Context(),
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		},
	)
	require.NoError(t, err)
	generated, err := os.ReadFile(filepath.Join(directory, "internal", "githubapi", "generated.go"))
	require.NoError(t, err)
	assert.Contains(t, string(generated), "func Viewer(")
}

func schemaMaterializer() materializer {
	return schema.NewMaterializer()
}

type recordingOutputWriter struct {
	outputs map[string][]byte
	writes  map[string]int
}

func (w *recordingOutputWriter) Write(path string, data []byte) error {
	if w.outputs == nil {
		w.outputs = map[string][]byte{}
	}
	if w.writes == nil {
		w.writes = map[string]int{}
	}
	w.outputs[path] = append([]byte{}, data...)
	w.writes[path]++
	return nil
}
