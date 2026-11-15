package generate

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/validator"
)

const dataDir = "testdata/queries"

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

// TestGenerate is a snapshot-based test of code generation using the shared
// schema and operation files in testdata/queries.
//
// To update the snapshots (if the code generator has changed), run the test
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
			source := string(generated[goFilename])
			assert.NotContains(t, source, "func (v *GitHubInputsVariables) Get")
			assert.NotContains(t, source, "Response) Get")
			assert.NotContains(t, source, "_Operation =")
			assert.NotContains(t, source, "var All")
			assert.NotContains(t, source, "PartialDataValue()")
			assert.NotContains(t, source, "HTTPStatusCode()")

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
		"client/gen.go":              "//go:build ignore\n\npackage main\n",
		"client/_ignored.go":         "package scratch\n",
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
	assert.Contains(t, source, `json2 "encoding/json/v2"`)
	assert.Contains(t, source, "Input json.Value")
	assert.Contains(t, source, "json2.Marshal")
	compileModulePackages(t, map[string][]byte{"runtime_import_alias_collision": []byte(source)})
}

func TestGenerateReservesDeclarationsAndAliasesMismatchedPackageImports(t *testing.T) {
	t.Parallel()

	rootDir := newModuleTempDir(t)
	mismatchDir := filepath.Join(rootDir, "pkgdir")
	versionedDir := filepath.Join(rootDir, "modpkg", "v2")
	firstDir := filepath.Join(rootDir, "first", "shared")
	secondDir := filepath.Join(rootDir, "second", "shared")
	for _, directory := range []string{mismatchDir, versionedDir, firstDir, secondDir} {
		require.NoError(t, os.MkdirAll(directory, 0o755))
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(mismatchDir, "value.go"),
		[]byte("package actualpkg\n\ntype Value string\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(versionedDir, "value.go"),
		[]byte("package modpkg\n\ntype Value string\n"),
		0o600,
	))
	for _, directory := range []string{firstDir, secondDir} {
		require.NoError(t, os.WriteFile(
			filepath.Join(directory, "value.go"),
			[]byte("package shared\n\ntype Value string\n"),
			0o600,
		))
	}
	mismatchAbs, err := filepath.Abs(mismatchDir)
	require.NoError(t, err)
	versionedAbs, err := filepath.Abs(versionedDir)
	require.NoError(t, err)
	firstAbs, err := filepath.Abs(firstDir)
	require.NoError(t, err)
	secondAbs, err := filepath.Abs(secondDir)
	require.NoError(t, err)
	mismatchPkg, err := packagePathFromModule(mismatchAbs)
	require.NoError(t, err)
	versionedPkg, err := packagePathFromModule(versionedAbs)
	require.NoError(t, err)
	firstPkg, err := packagePathFromModule(firstAbs)
	require.NoError(t, err)
	secondPkg, err := packagePathFromModule(secondAbs)
	require.NoError(t, err)

	schemaPath := filepath.Join(rootDir, "schema.graphql")
	operationPath := filepath.Join(rootDir, "operation.graphql")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`
scalar Mismatched
scalar Versioned
scalar StdV2
scalar First
scalar Second
type Box {
  id: ID!
}
type Query {
  box: Box!
  mismatched: Mismatched!
  versioned: Versioned!
  stdV2: StdV2!
  first: First!
  second: Second!
}
`), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(`
query Inspect {
  box @octoqlgen(typename: "json") {
    id
  }
  mismatched
  versioned
  stdV2
  first
  second
}
`), 0o600))

	config := &Config{
		Schema:               schemaPath,
		Operations:           []string{operationPath},
		Generated:            filepath.Join(rootDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(rootDir, "githubapitest", "generated.go"),
		Package:              "client",
		Bindings: map[string]string{
			"Mismatched": mismatchPkg + ".Value",
			"Versioned":  versionedPkg + ".Value",
			"StdV2":      "math/rand/v2.PCG",
			"First":      firstPkg + ".Value",
			"Second":     secondPkg + ".Value",
		},
	}
	outputs, err := Generate(config)
	require.NoError(t, err)
	secondOutputs, err := Generate(config)
	require.NoError(t, err)
	assert.Equal(t, outputs, secondOutputs)
	clientSource := string(outputs[config.Generated])
	assert.Contains(t, clientSource, `"`+firstPkg+`"`)
	assert.Contains(t, clientSource, `shared2 "`+secondPkg+`"`)
	compileGeneratedOutputs(t, rootDir, outputs)
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
			name:      "partial data error type collision",
			schema:    "type Query {\n  user: User\n}\ntype User {\n  id: ID!\n}\n",
			operation: "query GetUser {\n  user {\n    ...GetUserPartialDataError\n  }\n}\nfragment GetUserPartialDataError on User {\n  id\n}\n",
			wantError: `generated partial data error "GetUserPartialDataError" conflicts with a generated GraphQL type`,
		},
		{
			name:      "partial data error enum value collision",
			schema:    "enum FooPartial {\n  DATA_ERROR\n}\ntype Query {\n  status: FooPartial!\n}\n",
			operation: "query Foo {\n  status\n}\n",
			wantError: `generated partial data error "FooPartialDataError" conflicts with a generated enum value`,
		},
		{
			name:      "partial data error operation name collision",
			schema:    "type Query {\n  value: String!\n}\n",
			operation: "query GetUser {\n  value\n}\nquery GetUserPartialDataError {\n  value\n}\n",
			wantError: `generated partial data error "GetUserPartialDataError" conflicts with operation "GetUserPartialDataError"`,
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

	tempDir := newModuleTempDir(t)
	config := &Config{
		Schema:               filepath.Join(dataDir, "schema.graphql"),
		Operations:           []string{filepath.Join(dataDir, "GraphShapes.graphql")},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		Bindings:             testBindings(),
	}
	require.NoError(t, config.ValidateAndFillDefaults(""))

	outputs, err := Generate(config)
	require.NoError(t, err)
	require.Len(t, outputs, 2)
	assert.Equal(t, "githubapitest", config.testHandlerPackage)

	secondOutputs, err := Generate(config)
	require.NoError(t, err)
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
	assert.Contains(t, handlerSource, config.pkgPath+`"`)
	assert.NotContains(t, handlerSource, "type Error =")
	assert.NotContains(t, handlerSource, "type SearchRepositoriesResponse =")

	compileGeneratedOutputs(t, tempDir, outputs)
}

func TestGenerateTestHandlerIdentifierValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		schema    string
		operation string
		wantError string
	}{
		{
			name:      "lowercase operation",
			schema:    "type Query { viewer: String! }\n",
			operation: "query getViewer { viewer }\n",
			wantError: `test handler operation "getViewer" must begin with an uppercase letter`,
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
			name: "variables alias and enum value",
			schema: `enum Foo {
  VARIABLES
}
type Query {
  viewer(value: Foo!): String!
}
`,
			operation: `query Foo($value: Foo!) {
  viewer(value: $value)
}
`,
			wantError: `generated variables type "FooVariables" conflicts with a generated enum value`,
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
		"StatusQueryExpectation",
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

func TestGenerateErrors(t *testing.T) {
	t.Parallel()

	const defaultSchema = `type Query {
  f: String
  user: User
  default(input: InputWithDefaults! = {field: "input omitted"}): Boolean
  omitempty(input: OmitemptyInput): Boolean
}

type User {
  id: ID!
  name: ValidScalar!
}

scalar ValidScalar

input InputWithDefaults {
  field: String! = "input field omitted"
  nullableField: String = "nullable input field omitted"
}

input OmitemptyInput {
  field: String!
  nullableField: String
}
`

	tests := []struct {
		name      string
		schema    string
		operation string
		want      string
	}{
		{
			name:      "conflicting directive arguments",
			operation: "query ConflictingDirectiveArguments @octoqlgen(pointer: true, pointer: false) { f }\n",
			want:      `operation.graphql:1: query-spec does not match schema: There can be only one argument named "pointer".`,
		},
		{
			name:      "conflicting directives",
			operation: "query ConflictingDirectives @octoqlgenDefaults(pointer: true) @octoqlgenDefaults(pointer: false) { f }\n",
			want:      "operation.graphql:1: conflicting values for pointer",
		},
		{
			name:      "conflicting enum values",
			schema:    "enum AnnoyingEnum {\n  first_value\n  second_value\n  FIRST_VALUE\n}\n\ntype Query {\n  f: AnnoyingEnum\n}\n",
			operation: "query ConflictingEnumValues { f }\n",
			want:      "schema.graphql:4: enum values FIRST_VALUE and first_value have conflicting Go name AnnoyingEnumFirstValue",
		},
		{
			name:      "unnamed operation",
			schema:    "type Query {\n  myField: String\n}\n",
			operation: "query {\n  myField\n}\n",
			want:      "operation.graphql:1: operations must have operation-names",
		},
		{
			name:      "conflicting typename and for-field bind",
			operation: "query ConflictingTypeNameAndGlobalBind @octoqlgenFor(field: \"User.name\", bind: \"[]byte\") {\n  user {\n    name @octoqlgen(typename: \"MyID\")\n  }\n}\n",
			want:      "operation.graphql:3: typename and bind may not be used together",
		},
		{
			name:      "conflicting typename and global bind",
			operation: "query ConflictingTypeNameAndGlobalBind {\n  user {\n    name @octoqlgen(typename: \"OtherScalar\")\n  }\n}\n",
			want:      "operation.graphql:3: typename option conflicts with global binding for ValidScalar; use `bind: \"-\"` to override it",
		},
		{
			name:      "conflicting typename and local bind",
			operation: "query ConflictingTypeNameAndGlobalBind {\n  user {\n    name @octoqlgen(bind: \"[]byte\", typename: \"MyID\")\n  }\n}\n",
			want:      "operation.graphql:3: typename and bind may not be used together",
		},
		{
			name:      "conflicting type names",
			schema:    "type Query {\n  f: T\n}\n\ntype T {\n  g: String!\n  h: String!\n}\n",
			operation: "query ConflictingTypeNames {\n  f @octoqlgen(typename: \"T\") {\n    g\n  }\n  otherF: f @octoqlgen(typename: \"T\") {\n    g\n    h\n  }\n}\n",
			want:      "operation.graphql:5: conflicting definition for the Go type T: it is generated from both the selection of GraphQL type T (at operation.graphql:2) and the selection of GraphQL type T (at operation.graphql:5), which select different fields (field count changed: 1 fields vs 2 fields); give one of them a distinct name with an @octoqlgen(typename:) directive so they produce separate Go types",
		},
		{
			name:      "default input pointer without omitempty",
			operation: "# Non-nullable input field with default cannot be pointer without omitempty.\nquery DefaultInputs(\n  $input: InputWithDefaults!\n) @octoqlgenFor(field: \"InputWithDefaults.field\", pointer: true) {\n  default(input: $input)\n}\n",
			want:      "operation.graphql:3: pointer on non-null input field can only be used together with omitempty: InputWithDefaults.field",
		},
		{
			name:      "empty for directive field",
			operation: "query EmptyForDirective @octoqlgenFor(field: \"\", pointer: false) {\n  f\n}\n",
			want:      "operation.graphql:1: field must not be empty",
		},
		{
			name:      "flatten field without spread",
			schema:    "type Query { t: T }\ntype T { f: String }\n",
			operation: "query FlattenField {\n  t @octoqlgen(flatten: true) {\n    f\n  }\n}\n",
			want:      "operation.graphql:2: flatten is not yet supported for fields (only fragment spreads)",
		},
		{
			name:      "flatten implementation mismatch",
			schema:    "type Query { i: I }\ninterface I { f: String }\ntype T implements I { f: String }\n",
			operation: "fragment F on T { f }\nquery FlattenImplementation {\n  i @octoqlgen(flatten: true) {\n    ...F\n  }\n}\n",
			want:      "operation.graphql:3: flatten is not allowed for fields with fragment-spreads unless the field-type implements the fragment-type; field-type I does not implement fragment-type T",
		},
		{
			name:      "unknown directive argument",
			schema:    "type Query { t: T }\ntype T { f: String }\n",
			operation: "query UnknownDirectiveArgument {\n  t @octoqlgen(unknown: true) {\n    f\n  }\n}\n",
			want:      `operation.graphql:2: query-spec does not match schema: Unknown argument "unknown" on directive "@octoqlgen".`,
		},
		{
			name:      "invalid query field",
			operation: "query MyQuery { g }\n",
			want:      `operation.graphql:1: query-spec does not match schema: Cannot query field "g" on type "Query". Did you mean "f"?`,
		},
		{
			name:      "invalid scalar binding",
			schema:    "scalar InvalidScalar\n\ntype Query { f: InvalidScalar }\n",
			operation: "query InvalidScalar { f }\n",
			want:      `invalid type-name "bogus" (unknown type-name "bogus"); expected a builtin, path/to/package.Name, interface{}, or a slice, map, or pointer of those`,
		},
		{
			name:      "invalid schema syntax",
			schema:    "type Query {\n  f: String!\n  bogus\n}\n",
			operation: "query InvalidSchema { f }\n",
			want:      "schema.graphql:4: invalid schema: Expected :, found }",
		},
		{
			name:      "invalid schema with builtins",
			schema:    "type Query {\n  f: String\n  g: Bogus\n}\n\n" + validator.Prelude.Input,
			operation: "query MyQuery { f g }\n",
			want:      "schema.graphql:3: invalid schema: Undefined type Bogus.",
		},
		{
			name:      "keyword argument name",
			schema:    "type Query { f(type: String): String }\n",
			operation: "query KeywordArgumentName($type: String) {\n  f(type: $type)\n}\n",
			want:      "operation.graphql:1: variable name must not be a go keyword",
		},
		{
			name:      "keyword operation name",
			schema:    "type Query { f: String }\n",
			operation: "query type { f }\n",
			want:      "operation.graphql:1: operation name must not be a go keyword",
		},
		{
			name:      "keyword typename",
			schema:    "type Query { f: T }\ntype T { g: String }\n",
			operation: "query KeywordTypeName {\n  f @octoqlgen(typename: \"type\") {\n    g\n  }\n}\n",
			want:      "schema.graphql:1: typename option must not be a go keyword",
		},
		{
			name:      "no mutation type",
			schema:    "type Query { f: String }\n",
			operation: "mutation M { g }\n",
			want:      `operation.graphql:1: query-spec does not match schema: Schema does not support operation type "mutation"`,
		},
		{
			name:      "no query in file",
			operation: "",
			want:      "no queries found, looked in: operation.graphql (configure this in octoqlgen.yaml)",
		},
		{
			name:      "no query type",
			schema:    "type T { f: String }\n",
			operation: "query Q { f }\n",
			want:      `operation.graphql:1: query-spec does not match schema: Schema does not support operation type "query"`,
		},
		{
			name:      "omitempty for required input field",
			operation: "# Non-nullable input field cannot omit.\nquery OmitemptyDirective(\n  $input: OmitemptyInput\n) @octoqlgenFor(field: \"OmitemptyInput.field\", omitempty: true) {\n  omitempty(input: $input)\n}\n",
			want:      "operation.graphql:3: omitempty may only be used on optional arguments: OmitemptyInput.field",
		},
		{
			name:      "omitempty on required variable with default",
			operation: "query OperationOmitemptyRequiredVariable(\n  $input: InputWithDefaults!\n) @octoqlgenDefaults(pointer: true, omitempty: true) {\n  default(input: $input)\n}\n",
			want:      "operation.graphql:2: omitempty may only be used on optional arguments: OperationOmitemptyRequiredVariable.input",
		},
		{
			name:      "pointer on required variable",
			operation: "query PointerOperationVariable(\n  $input: InputWithDefaults! @octoqlgen(pointer: true)\n) {\n  default(input: $input)\n}\n",
			want:      "operation.graphql:2: pointer on non-null argument can only be used together with omitempty: PointerOperationVariable.input",
		},
		{
			name:      "struct option on object",
			schema:    "type Query {\n  myObject: MyObject\n}\n\ntype MyObject {\n  f: String!\n}\n",
			operation: "query StructOptionOnObject {\n  myObject @octoqlgen(struct: true) {\n    f\n  }\n}\n",
			want:      "operation.graphql:2: struct is only applicable to interface-typed fields",
		},
		{
			name:      "struct option with fragments",
			schema:    "type Query {\n  myInterface: MyInterface\n}\n\ninterface MyInterface {\n  f: String!\n}\n\ntype MyObject implements MyInterface {\n  f: String!\n  g: String!\n}\n\ntype OtherObject implements MyInterface {\n  f: String!\n}\n",
			operation: "query StructOptionOnObject {\n  myInterface @octoqlgen(struct: true) {\n    f\n    ... on MyObject {\n      g\n    }\n  }\n}\n",
			want:      "operation.graphql:2: struct is not allowed for types with fragments",
		},
		{
			name:      "subscription rejected",
			schema:    "type Query {\n  value: String!\n}\n\ntype Subscription {\n  value: String!\n}\n",
			operation: "subscription Value {\n  value\n}\n",
			want:      "operation.graphql:1: subscriptions are not supported by octoql",
		},
		{
			name:      "unknown scalar",
			schema:    "scalar UnknownScalar\n\ntype Query { f: UnknownScalar }\n",
			operation: "query UnknownScalar { f }\n",
			want:      "schema.graphql:3: unknown scalar UnknownScalar: please add it to \"bindings\" in octoqlgen.yaml\nExample: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			schema := test.schema
			if schema == "" {
				schema = defaultSchema
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(schema), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "operation.graphql"), []byte(test.operation), 0o600))

			config := &Config{
				Schema:     "schema.graphql",
				Operations: []string{"operation.graphql"},
				Package:    "test",
				Generated:  os.DevNull,
				Bindings: map[string]string{
					"ValidScalar":   "string",
					"InvalidScalar": "bogus",
				},
			}
			require.NoError(t, config.ValidateAndFillDefaults(dir))

			_, err := Generate(config)
			require.Error(t, err)

			got := strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), "")
			got = strings.ReplaceAll(got, filepath.ToSlash(dir)+"/", "")
			assert.Equal(t, test.want, got)
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
