package generate

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dataDir                     = "testdata/queries"
	errorsDir                   = "testdata/errors"
	githubGenerationDefaultsDir = "testdata/github-generation-defaults"
)

func testBindings() map[string]string {
	return map[string]string{
		"ID":              "github.com/willabides/octoql/internal/testutil.ID",
		"DateTime":        "time.Time",
		"PreciseDateTime": "time.Time",
		"URI":             "string",
		"GitObjectID":     "string",
		"BigInt":          "int64",
		"Date":            "github.com/willabides/octoql/internal/testutil.Date",
		"JSON":            "interface{}",
		"ComplexJSON":     "[]map[string]*[]*map[string]interface{}",
		"Account":         "github.com/willabides/octoql/internal/testutil.Account",
	}
}

type testGenerateSpec struct {
	schema      string
	operations  string
	packageName string
	bindings    map[string]string
}

func generateTestSource(t *testing.T, spec *testGenerateSpec) (string, error) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	require.NoError(t, os.WriteFile(schemaPath, []byte(spec.schema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(spec.operations), 0o600))

	packageName := spec.packageName
	if packageName == "" {
		packageName = "client"
	}
	config := &Config{
		Schema:     schemaPath,
		Operations: []string{operationPath},
		Generated:  filepath.Join(dir, "generated.go"),
		Package:    packageName,
		Bindings:   spec.bindings,
	}
	outputs, err := Generate(config)
	if err != nil {
		return "", err
	}
	return string(outputs[config.Generated]), nil
}

func newModuleTempDir(t *testing.T) string {
	tempRoot := filepath.Join("testdata", "tmp")
	require.NoError(t, os.MkdirAll(tempRoot, 0o755))
	dir, err := os.MkdirTemp(tempRoot, "test-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
	})
	return dir
}

// compileModulePackages compiles generated Go files within the current module
// so they can import internal/testutil.
func compileModulePackages(t *testing.T, files map[string][]byte) {
	t.Helper()
	if len(files) == 0 {
		return
	}
	tempDir := newModuleTempDir(t)
	writeNamedPackages(t, tempDir, files)
	compileGoDir(t, tempDir, false)
}

func compileStandaloneGo(t *testing.T, files map[string][]byte) {
	t.Helper()
	if len(files) == 0 {
		return
	}
	tempDir := t.TempDir()
	err := os.WriteFile(
		filepath.Join(tempDir, "go.mod"),
		[]byte("module probe\n\ngo 1.27.3\n"),
		0o600,
	)
	require.NoError(t, err)
	writeNamedPackages(t, tempDir, files)
	compileGoDir(t, tempDir, true)
}

func writeNamedPackages(t *testing.T, rootDir string, files map[string][]byte) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for index, name := range names {
		packageDir := filepath.Join(
			rootDir,
			fmt.Sprintf("pkg_%02d_%s", index, sanitizeBuildDirName(name)),
		)
		require.NoError(t, os.MkdirAll(packageDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(packageDir, "generated.go"), files[name], 0o600))
	}
}

func compileGoDir(t *testing.T, dir string, standalone bool) {
	t.Helper()
	args := []string{"build"}
	env := append(os.Environ(), "GOWORK=off")
	if standalone {
		args = append(args, "-mod=readonly")
		env = append(env, "GOPROXY=off", "GOSUMDB=off")
	}
	args = append(args, "./...")
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "generated code does not compile:\n%s", output)
}

func sanitizeBuildDirName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	return builder.String()
}

// TestGenerate is a snapshot-based test of code-generation.
//
// This file just has the test runner; the actual data is all in
// testdata/queries.  Specifically, the schema used for all the queries is in
// schema.graphql; the queries themselves are in TestName.graphql.  The test
// asserts that running octoqlgen on that query produces the generated code in
// an external snapshot.
//
// To update the snapshots (if the code-generator has changed), run the test
// with `UPDATE_SNAPS=true`. Generated Go snapshots are compiled, so the test
// verifies the snapshot rather than only the in-memory generated output.
func TestGenerate(t *testing.T) {
	t.Parallel()

	snapshotsToBuild := make(map[string][]byte)
	for _, test := range []struct {
		name            string
		sourceFilenames []string
	}{
		{
			name:            "GraphShapes.graphql",
			sourceFilenames: []string{"GraphShapes.graphql"},
		},
		{
			name:            "Inputs.graphql",
			sourceFilenames: []string{"Inputs.graphql"},
		},
		{
			name:            "Naming.graphql",
			sourceFilenames: []string{"Naming.graphql"},
		},
		{
			name:            "PredeclaredOperationNames.graphql",
			sourceFilenames: []string{"PredeclaredOperationNames.graphql"},
		},
		{
			name: "MultipleOperationFiles",
			sourceFilenames: []string{
				"Naming.graphql",
				"PredeclaredOperationNames.graphql",
			},
		},
	} {
		goFilename := test.name + ".go"
		operationFilenames := make([]string, 0, len(test.sourceFilenames))
		for _, sourceFilename := range test.sourceFilenames {
			operationFilenames = append(operationFilenames, filepath.Join(dataDir, sourceFilename))
		}

		t.Run(test.name, func(t *testing.T) {
			generated, err := Generate(&Config{
				Schema:     filepath.Join(dataDir, "schema.graphql"),
				Operations: operationFilenames,
				Package:    "test",
				Generated:  goFilename,
				Bindings:   testBindings(),
			})
			if err != nil {
				t.Fatal(err)
			}
			assert.NotContains(t, string(generated[goFilename]), "func (v *GitHubInputsVariables) Get")

			t.Run(goFilename, func(t *testing.T) {
				snapshot := matchGeneratedSnapshot(t, generated[goFilename])
				if snapshot != nil {
					snapshotsToBuild[normalizeSnapshotName(t.Name())] = snapshot
				}
			})
		})
	}
	compileModulePackages(t, snapshotsToBuild)
}

func TestGenerateNormalizesConfig(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	var warnings bytes.Buffer
	for name, content := range map[string]string{
		"go.mod":                     "module example.com/app\n\ngo 1.27.3\n",
		"client/existing.go":         "package api\n\nfunc unfinished(",
		"client/api_test.go":         "package api_test\n",
		"client/generated.go":        "not a package clause\n",
		"githubapitest/generated.go": "",
		"schema.graphql":             "type Query { field: String! }\n",
		"operation.graphql":          "query Q { field }\n",
	} {
		filename := filepath.Join(directory, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
		require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
	}
	config := &Config{
		Schema:               filepath.Join(directory, "schema.graphql"),
		Operations:           []string{filepath.Join(directory, "operation.graphql")},
		Generated:            filepath.Join(directory, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(directory, "githubapitest", "generated.go"),
		Warnings:             &warnings,
	}

	outputs, err := Generate(config)
	require.NoError(t, err)
	source := string(outputs[config.Generated])
	assert.Contains(t, source, "package api")
	assert.Contains(t, source, "ctx context.Context,")
	assert.Equal(t, "example.com/app/client", config.pkgPath)
	assert.Contains(t, string(outputs[config.TestHandlerGenerated]), "package githubapitest")
	assert.Empty(t, warnings.String())

	repeated, err := Generate(config)
	require.NoError(t, err)
	assert.Equal(t, outputs, repeated)
}

func TestGenerateRuntimeImportAliasCollision(t *testing.T) {
	t.Parallel()

	source, err := generateTestSource(t, &testGenerateSpec{
		packageName: "collision",
		schema: `
scalar JSON

interface Node {
  id: ID!
}

type Item implements Node {
  id: ID!
}

type Query {
  value(input: JSON!): JSON!
  node: Node
}
`,
		operations: `
query Value($input: JSON!) {
  value(input: $input)
  node {
    id
    ... on Item {
      id
    }
  }
}
`,
		bindings: map[string]string{
			"JSON": "github.com/willabides/octoql/internal/generate/testdata/json.Value",
		},
	})
	require.NoError(t, err)

	assert.Contains(t, source, `"github.com/willabides/octoql/internal/generate/testdata/json"`)
	assert.Contains(t, source, `json2 "encoding/json"`)
	assert.Contains(t, source, "Input json.Value")
	assert.Contains(t, source, "json2.Marshal")
	compileModulePackages(t, map[string][]byte{"runtime_import_alias_collision": []byte(source)})
}

func TestGenerateRejectsRuntimeIdentifierCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		schema    string
		operation string
		wantError string
	}{
		{
			name:      "runtime method collision",
			operation: "query RateLimit {\n  value\n}\n",
			wantError: `generated Client method "RateLimit" conflicts with operation "RateLimit"`,
		},
		{
			name:      "partial data error enum values variable collision",
			schema:    "enum PartialDataError {\n  VALUE\n}\ntype Query {\n  value: PartialDataError!\n}\n",
			operation: "query All {\n  value\n}\n",
			wantError: `generated partial data error "AllPartialDataError" conflicts with a generated enum values variable`,
		},
		{
			name:      "private runtime declaration collision",
			operation: "query Value @octoqlgen(typename: \"_octoqlPayload\") {\n  value\n}\n",
			wantError: `generated identifier "_octoqlPayload" uses reserved prefix "_octoql"`,
		},
		{
			name:      "reserved runtime operation prefix",
			operation: "query _octoqlOperation {\n  value\n}\n",
			wantError: `generated identifier "_octoqlOperation" uses reserved prefix "_octoql"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			schema := test.schema
			if schema == "" {
				schema = "type Query {\n  value: String!\n}\n"
			}
			_, err := generateTestSource(t, &testGenerateSpec{
				schema:      schema,
				operations:  test.operation,
				packageName: "collision",
			})

			require.EqualError(t, err, test.wantError)
		})
	}
}

func TestGenerateWithTestHandlerUsesOnePlan(t *testing.T) {
	t.Parallel()

	rootDir := newModuleTempDir(t)

	compiledOutputs := make(map[string][]byte)
	for _, strategy := range []TestHandlerTypeStrategy{
		TestHandlerTypesClient,
		TestHandlerTypesLocal,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			strategyDir := filepath.Join(rootDir, string(strategy))
			outputs := testGenerateWithTestHandlerUsesOnePlan(t, strategyDir, strategy)
			maps.Copy(compiledOutputs, outputs)
		})
	}
	compileGeneratedOutputs(t, rootDir, compiledOutputs)
}

func testGenerateWithTestHandlerUsesOnePlan(
	t *testing.T,
	tempDir string,
	strategy TestHandlerTypeStrategy,
) map[string][]byte {
	config := &Config{
		Schema:               filepath.Join(dataDir, "schema.graphql"),
		Operations:           []string{filepath.Join(dataDir, "GraphShapes.graphql")},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		TestHandlerTypes:     strategy,
		Bindings:             testBindings(),
	}
	err := config.ValidateAndFillDefaults("")
	if err != nil {
		t.Fatal(err)
	}

	outputs, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %d, want 2", len(outputs))
	}
	if config.testHandlerPackage != "githubapitest" {
		t.Fatalf(
			"test handler package = %q, want githubapitest",
			config.testHandlerPackage,
		)
	}

	secondOutputs, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	for filename, content := range outputs {
		if !bytes.Equal(content, secondOutputs[filename]) {
			t.Fatalf("output %q is not deterministic", filename)
		}
	}

	reversePlan, err := buildGenerationPlan(config)
	require.NoError(t, err)
	handlerFirst, err := renderTestHandler(reversePlan)
	require.NoError(t, err)
	clientSecond, err := renderClient(reversePlan)
	require.NoError(t, err)
	clientAgain, err := renderClient(reversePlan)
	require.NoError(t, err)
	assert.Equal(t, outputs[config.TestHandlerGenerated], handlerFirst)
	assert.Equal(t, outputs[config.Generated], clientSecond)
	assert.Equal(t, clientSecond, clientAgain)
	handlerSource := string(outputs[config.TestHandlerGenerated])
	clientImport := config.pkgPath + `"`
	switch strategy {
	case TestHandlerTypesLocal:
		assert.NotContains(t, handlerSource, clientImport)
		assert.Contains(t, handlerSource, "type SearchRepositoriesResponse struct")
	default:
		assert.Contains(t, handlerSource, clientImport)
	}

	return outputs
}

func TestGenerateLocalTestHandlerBindings(t *testing.T) {
	t.Parallel()

	rootDir := newModuleTempDir(t)
	compiledOutputs := make(map[string][]byte)

	newConfig := func(
		t *testing.T,
		schema string,
		operation string,
	) (*Config, string) {
		tempDir, err := os.MkdirTemp(rootDir, "case-")
		require.NoError(t, err)
		schemaPath := filepath.Join(tempDir, "schema.graphql")
		err = os.WriteFile(schemaPath, []byte(schema), 0o600)
		require.NoError(t, err)
		operationPath := filepath.Join(tempDir, "operation.graphql")
		err = os.WriteFile(operationPath, []byte(operation), 0o600)
		require.NoError(t, err)
		config := &Config{
			Schema:               schemaPath,
			Operations:           []string{operationPath},
			Generated:            filepath.Join(tempDir, "client", "generated.go"),
			TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
			TestHandlerTypes:     TestHandlerTypesLocal,
			Package:              "client",
		}
		err = config.ValidateAndFillDefaults("")
		require.NoError(t, err)
		return config, tempDir
	}

	const scalarSchema = `
scalar Bound
scalar Unused
type Query {
  value(input: Bound): Bound
}
`
	const scalarOperation = `
query Value($input: Bound) {
  value(input: $input)
}
`

	t.Run("reachable client binding rejected", func(t *testing.T) {
		config, _ := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]string{
			"Bound": config.pkgPath + ".ClientScalar",
		}

		outputs, err := Generate(config)

		require.Error(t, err)
		assert.Nil(t, outputs)
		assert.Contains(t, err.Error(), "test_handler.types local cannot use")
		assert.Contains(t, err.Error(), "generated client package")
		assert.Contains(t, err.Error(), "ClientScalar")
	})

	t.Run("handler binding resolves without self import", func(t *testing.T) {
		// This temporary fixture deliberately exports its binding because the
		// generated client imports it from the generated handler package.
		config, tempDir := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]string{
			"Bound": config.testHandlerPkgPath + ".HandlerScalar",
		}

		outputs, err := Generate(config)

		require.NoError(t, err)
		handlerSource := string(outputs[config.TestHandlerGenerated])
		assert.Contains(t, handlerSource, "HandlerScalar")
		assert.NotContains(t, handlerSource, `"`+config.testHandlerPkgPath+`"`)
		outputs[filepath.Join(tempDir, "githubapitest", "support.go")] = []byte(
			"package githubapitest\n\ntype HandlerScalar string\n",
		)
		maps.Copy(compiledOutputs, outputs)
	})

	t.Run("external binding compiles", func(t *testing.T) {
		config, _ := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]string{
			"Bound": "github.com/willabides/octoql/internal/testutil.Date",
		}

		outputs, err := Generate(config)

		require.NoError(t, err)
		handlerSource := string(outputs[config.TestHandlerGenerated])
		assert.Contains(
			t,
			handlerSource,
			`"github.com/willabides/octoql/internal/testutil"`,
		)
		maps.Copy(compiledOutputs, outputs)
	})

	t.Run("unused client binding does not block", func(t *testing.T) {
		config, _ := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]string{
			"Bound":  "string",
			"Unused": config.pkgPath + ".ClientScalar",
		}

		outputs, err := Generate(config)

		require.NoError(t, err)
		assert.NotContains(
			t,
			string(outputs[config.TestHandlerGenerated]),
			`"`+config.pkgPath+`"`,
		)
		maps.Copy(compiledOutputs, outputs)
	})

	t.Run("colliding external import aliases are deterministic", func(t *testing.T) {
		config, tempDir := newConfig(
			t,
			`
scalar First
scalar Second
type Query {
  first: First!
  second: Second!
}
`,
			`
query Alpha { first }
query Beta { second }
`,
		)
		firstDirectory := filepath.Join(tempDir, "first", "shared")
		secondDirectory := filepath.Join(tempDir, "second", "shared")
		for _, directory := range []string{firstDirectory, secondDirectory} {
			mkdirErr := os.MkdirAll(directory, 0o755)
			require.NoError(t, mkdirErr)
			writeErr := os.WriteFile(
				filepath.Join(directory, "value.go"),
				[]byte("package shared\n\ntype Value string\n"),
				0o600,
			)
			require.NoError(t, writeErr)
		}
		firstAbsolute, err := filepath.Abs(firstDirectory)
		require.NoError(t, err)
		secondAbsolute, err := filepath.Abs(secondDirectory)
		require.NoError(t, err)
		firstPackage, err := packagePathFromModule(firstAbsolute)
		require.NoError(t, err)
		secondPackage, err := packagePathFromModule(secondAbsolute)
		require.NoError(t, err)
		config.Bindings = map[string]string{
			"First":  firstPackage + ".Value",
			"Second": secondPackage + ".Value",
		}

		first, err := Generate(config)
		require.NoError(t, err)
		second, err := Generate(config)
		require.NoError(t, err)

		assert.Equal(t, first[config.TestHandlerGenerated], second[config.TestHandlerGenerated])
		handlerSource := string(first[config.TestHandlerGenerated])
		assert.Contains(t, handlerSource, `"`+firstPackage+`"`)
		assert.Contains(t, handlerSource, `shared2 "`+secondPackage+`"`)
		maps.Copy(compiledOutputs, first)
	})

	t.Run("local types do not import main client", func(t *testing.T) {
		config, _ := newConfig(t, "type Query { value: String! }\n", "query Value { value }\n")
		config.Package = "main"

		outputs, err := Generate(config)
		require.NoError(t, err)
		assert.NotContains(t, string(outputs[config.TestHandlerGenerated]), config.pkgPath+`"`)
	})

	compileGeneratedOutputs(t, rootDir, compiledOutputs)
}

func TestGenerateTestHandlerIdentifierValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		schema    string
		operation string
		types     TestHandlerTypeStrategy
		wantError string
	}{
		{
			name:      "typename collision with test handler runtime",
			schema:    "type Query { value: String! }\n",
			operation: "query Value @octoqlgen(typename: \"TestHandler\") {\n  value\n}\n",
			wantError: "conflicts with test handler runtime",
		},
		{
			name:      "lowercase operation with client types",
			schema:    "type Query { viewer: String! }\n",
			operation: "query getViewer { viewer }\n",
			types:     TestHandlerTypesClient,
			wantError: `test handler operation "getViewer" must begin with an uppercase letter`,
		},
		{
			name:      "lowercase operation with local types",
			schema:    "type Query { viewer: String! }\n",
			operation: "query getViewer { viewer }\n",
			types:     TestHandlerTypesLocal,
			wantError: `test handler operation "getViewer" must begin with an uppercase letter`,
		},
		{
			name: "enum value and expectation",
			schema: `enum Get {
  NODE_EXPECTATION
}
type Query {
  value: Get!
}
`,
			operation: "query GetNode { value }\n",
			wantError: `generated identifier "GetNodeExpectation"`,
		},
		{
			name: "client type and runtime",
			schema: `type NewTestHandler {
  id: ID!
}
type Query {
  value: NewTestHandler!
}
`,
			operation: `fragment NewTestHandler on NewTestHandler {
  id
}
query Value {
  value {
    ...NewTestHandler
  }
}
`,
			wantError: `generated identifier "NewTestHandler"`,
		},
		{
			name:   "variables alias and operation",
			schema: "type Query { viewer(value: String!): String! }\n",
			operation: `query Foo($value: String!) {
  viewer(value: $value)
}
query FooVariables($value: String!) {
  viewer(value: $value)
}
`,
			wantError: `generated variables type "FooVariables" conflicts with operation "FooVariables"`,
		},
		{
			name: "variables alias and enum values variable",
			schema: `enum Variables {
  VALUE
}
type Query {
  viewer(value: Variables!): String!
}
`,
			operation: `query All($value: Variables!) {
  viewer(value: $value)
}
`,
			wantError: `generated variables type "AllVariables" conflicts with a generated enum values variable`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(tempDir, "go.mod"),
				[]byte("module example.com/app\n\ngo 1.27.3\n"),
				0o600,
			))
			schemaPath := filepath.Join(tempDir, "schema.graphql")
			operationPath := filepath.Join(tempDir, "operation.graphql")
			require.NoError(t, os.WriteFile(schemaPath, []byte(test.schema), 0o600))
			require.NoError(t, os.WriteFile(operationPath, []byte(test.operation), 0o600))

			config := &Config{
				Schema:               schemaPath,
				Operations:           []string{operationPath},
				Generated:            filepath.Join(tempDir, "client", "generated.go"),
				TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
				TestHandlerTypes:     test.types,
				Package:              "client",
			}
			require.NoError(t, config.ValidateAndFillDefaults(tempDir))

			_, err := Generate(config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
}

func TestGenerateTestHandlerClientImportAliasAvoidsGeneratedNames(t *testing.T) {
	t.Parallel()

	rootDir := newModuleTempDir(t)
	compiledOutputs := make(map[string][]byte)

	for _, packageName := range []string{
		"StatusReady",
		"StatusQueryResponse",
		"ExpectStatusQuery",
		"DefaultStatusQuery",
		"ResetStatusQuery",
	} {
		t.Run(packageName, func(t *testing.T) {
			tempDir, err := os.MkdirTemp(rootDir, "case-")
			require.NoError(t, err)

			schemaPath := filepath.Join(tempDir, "schema.graphql")
			operationPath := filepath.Join(tempDir, "operation.graphql")
			require.NoError(
				t,
				os.WriteFile(
					schemaPath,
					[]byte("enum Status { READY }\ntype Query { status: Status! }\n"),
					0o600,
				),
			)
			require.NoError(
				t,
				os.WriteFile(
					operationPath,
					[]byte("query StatusQuery { status }\n"),
					0o600,
				),
			)
			config := &Config{
				Schema:               schemaPath,
				Operations:           []string{operationPath},
				Generated:            filepath.Join(tempDir, "client", "generated.go"),
				TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
				Package:              packageName,
			}
			require.NoError(t, config.ValidateAndFillDefaults(""))

			outputs, err := Generate(config)
			require.NoError(t, err)
			handlerSource := string(outputs[config.TestHandlerGenerated])
			expectedImport := packageName + `2 "`
			assert.Contains(t, handlerSource, expectedImport)
			maps.Copy(compiledOutputs, outputs)
		})
	}
	compileGeneratedOutputs(t, rootDir, compiledOutputs)
}

func compileGeneratedOutputs(
	t *testing.T,
	directory string,
	outputs map[string][]byte,
) {
	t.Helper()

	if len(outputs) == 0 {
		return
	}
	for filename, content := range outputs {
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
		require.NoError(t, os.WriteFile(filename, content, 0o600))
	}
	compileGoDir(t, directory, false)
}

func TestOmitUnreferencedImplementations(t *testing.T) {
	t.Parallel()

	config := &Config{
		Schema:     filepath.Join(dataDir, "schema.graphql"),
		Operations: []string{filepath.Join(githubGenerationDefaultsDir, "operations.graphql")},
		Generated:  "generated.go",
		Package:    "githubdefaults",
	}
	generated, err := Generate(config)
	require.NoError(t, err)
	source := string(generated[config.Generated])

	assert.Contains(t, source, "type GitHubAbstractSelectionsNodeRepository struct")
	assert.Contains(t, source, "type GitHubAbstractSelectionsNodeOctoqlOther struct")
	assert.NotContains(t, source, "type GitHubAbstractSelectionsNodeIssue struct")

	catchAllPattern := regexp.MustCompile(`type (\w+OctoqlOther) struct`)
	matches := catchAllPattern.FindAllStringSubmatch(source, -1)
	require.NotEmpty(t, matches)
	names := make(map[string]bool, len(matches))
	for _, match := range matches {
		require.Falsef(t, names[match[1]], "duplicate catch-all %s", match[1])
		names[match[1]] = true
	}
}

// TestGenerateErrors is a snapshot-based test of error text.
//
// For each .go or .graphql file in testdata/errors, it asserts that the given
// query returns an error, and that the error's string-text matches the
// snapshot.  The snapshotting is useful to ensure we don't accidentally make
// the text less readable, drop the line numbers, etc.  We include both .go and
// .graphql tests for some of the test cases, to make sure the line numbers
// work in both cases.  Tests may include a .schema.graphql file of their own,
// or use the shared schema.graphql in the same directory for convenience.
func TestGenerateErrors(t *testing.T) {
	t.Parallel()

	files, readErr := os.ReadDir(errorsDir)
	if readErr != nil {
		t.Fatal(readErr)
	}

	for _, file := range files {
		sourceFilename := file.Name()
		if !strings.HasSuffix(sourceFilename, ".graphql") ||
			strings.HasSuffix(sourceFilename, ".schema.graphql") ||
			sourceFilename == "schema.graphql" {
			continue
		}

		baseFilename := strings.TrimSuffix(sourceFilename, filepath.Ext(sourceFilename))
		testFilename := strings.ReplaceAll(sourceFilename, ".", "/")

		// Schema is either <base>.schema.graphql, or <dir>/schema.graphql if
		// that doesn't exist.
		schemaFilename := baseFilename + ".schema.graphql"
		_, statErr := os.Stat(filepath.Join(errorsDir, schemaFilename))
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			schemaFilename = "schema.graphql"
		case statErr != nil:
			t.Fatal(statErr)
		}

		t.Run(testFilename, func(t *testing.T) {
			_, err := Generate(&Config{
				Schema:     filepath.Join(errorsDir, schemaFilename),
				Operations: []string{filepath.Join(errorsDir, sourceFilename)},
				Package:    "test",
				Generated:  os.DevNull,
				Bindings: map[string]string{
					"ValidScalar":   "string",
					"InvalidScalar": "bogus",
				},
			})
			if err == nil {
				t.Fatal("expected an error")
			}

			switch sourceFilename {
			case "ConflictingDirectiveArguments.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/ConflictingDirectiveArguments.graphql:1: query-spec does not match schema: There can be only one argument named "pointer".`))
			case "ConflictingDirectives.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingDirectives.graphql:1: conflicting values for pointer"))
			case "ConflictingEnumValues.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingEnumValues.schema.graphql:4: enum values FIRST_VALUE and first_value have conflicting Go name AnnoyingEnumFirstValue"))
			case "ConflictingSelections.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingSelections.graphql:1: operations must have operation-names"))
			case "ConflictingTypeNameAndForFieldBind.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNameAndForFieldBind.graphql:3: typename and bind may not be used together"))
			case "ConflictingTypeNameAndGlobalBind.graphql":
				want := "testdata/errors/ConflictingTypeNameAndGlobalBind.graphql:3: typename option conflicts with global binding for ValidScalar; use `bind: \"-\"` to override it"
				if got := err.Error(); got != want {
					t.Errorf("error = %q, want %q", got, want)
				}
			case "ConflictingTypeNameAndLocalBind.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNameAndLocalBind.graphql:3: typename and bind may not be used together"))
			case "ConflictingTypeNames.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNames.graphql:5: conflicting definition for the Go type T: it is generated from both the selection of GraphQL type T (at testdata/errors/ConflictingTypeNames.graphql:2) and the selection of GraphQL type T (at testdata/errors/ConflictingTypeNames.graphql:5), which select different fields (field count changed: 1 fields vs 2 fields); give one of them a distinct name with an @octoqlgen(typename:) directive so they produce separate Go types"))
			case "DefaultInputsNoOmitPointerForDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/DefaultInputsNoOmitPointerForDirective.graphql:4: pointer on non-null input field can only be used together with omitempty: InputWithDefaults.field"))
			case "EmptyForDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/EmptyForDirective.graphql:1: field must not be empty"))
			case "FlattenField.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/FlattenField.graphql:2: flatten is not yet supported for fields (only fragment spreads)"))
			case "FlattenImplementation.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/FlattenImplementation.graphql:3: flatten is not allowed for fields with fragment-spreads unless the field-type implements the fragment-type; field-type I does not implement fragment-type T"))
			case "UnknownDirectiveArgument.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/UnknownDirectiveArgument.graphql:2: query-spec does not match schema: Unknown argument "unknown" on directive "@octoqlgen".`))
			case "InvalidQuery.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/InvalidQuery.graphql:1: query-spec does not match schema: Cannot query field "g" on type "Query". Did you mean "f"?`))
			case "InvalidScalar.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`invalid type-name "bogus" (unknown type-name "bogus"); expected a builtin, path/to/package.Name, interface{}, or a slice, map, or pointer of those`))
			case "InvalidSchemaSyntax.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/InvalidSchemaSyntax.schema.graphql:4: invalid schema: Expected :, found }"))
			case "InvalidSchemaWithBuiltins.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/InvalidSchemaWithBuiltins.schema.graphql:3: invalid schema: Undefined type Bogus."))
			case "KeywordArgumentName.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/KeywordArgumentName.graphql:1: variable name must not be a go keyword"))
			case "KeywordOperationName.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/KeywordOperationName.graphql:1: operation name must not be a go keyword"))
			case "KeywordTypeName.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/KeywordTypeName.schema.graphql:1: typename option must not be a go keyword"))
			case "NoMutationType.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/NoMutationType.graphql:1: query-spec does not match schema: Schema does not support operation type "mutation"`))
			case "NoQuery.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("no queries found, looked in: testdata/errors/NoQuery.graphql (configure this in octoqlgen.yaml)"))
			case "NoQueryType.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/NoQueryType.graphql:1: query-spec does not match schema: Schema does not support operation type "query"`))
			case "OmitemptyForDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/OmitemptyForDirective.graphql:3: omitempty may only be used on optional arguments: OmitemptyInput.field"))
			case "OperationOmitemptyRequiredVariable.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/OperationOmitemptyRequiredVariable.graphql:2: omitempty may only be used on optional arguments: OperationOmitemptyRequiredVariable.input"))
			case "PartialDataErrorNameCollision.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`generated partial data error "GetUserPartialDataError" conflicts with a generated GraphQL type`))
			case "PartialDataErrorEnumValueCollision.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`generated partial data error "FooPartialDataError" conflicts with a generated enum value`))
			case "PartialDataErrorOperationNameCollision.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`generated partial data error "GetUserPartialDataError" conflicts with operation "GetUserPartialDataError"`))
			case "PointerOperationVariable.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/PointerOperationVariable.graphql:2: pointer on non-null argument can only be used together with omitempty: PointerOperationVariable.input"))
			case "StructOptionOnObject.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/StructOptionOnObject.graphql:2: struct is only applicable to interface-typed fields"))
			case "StructOptionWithFragments.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/StructOptionWithFragments.graphql:2: struct is not allowed for types with fragments"))
			case "Subscription.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/Subscription.graphql:1: subscriptions are not supported by octoql"))
			case "UnknownScalar.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/UnknownScalar.schema.graphql:3: unknown scalar UnknownScalar: please add it to "bindings" in octoqlgen.yaml
Example: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml`))
			default:
				t.Fatalf("missing inline snapshot for %s", sourceFilename)
			}
		})
	}
}

func matchGeneratedSnapshot(t *testing.T, content []byte) []byte {
	t.Helper()

	snaps.WithConfig(
		snaps.Dir(filepath.Join("testdata", "snapshots")),
		snaps.Filename(normalizeSnapshotName(t.Name())),
		snaps.Ext(".go"),
		snaps.Raw(),
	).MatchStandaloneSnapshot(t, string(content))

	// Generated Go remains external because this compiles the snapshot file that
	// was compared above.
	if testing.Short() {
		return nil
	}
	snapshot, err := os.ReadFile(filepath.Join(
		"testdata",
		"snapshots",
		normalizeSnapshotName(t.Name())+"_1.snap.go",
	))
	require.NoError(t, err)
	return snapshot
}

func normalizeSnapshotName(name string) string {
	return strings.NewReplacer("/", "_", `\`, "_").Replace(name)
}
