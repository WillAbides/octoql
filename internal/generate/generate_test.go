package generate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dataDir   = "testdata/queries"
	errorsDir = "testdata/errors"
)

func testBindings() map[string]*TypeBinding {
	return map[string]*TypeBinding{
		"ID":              {Type: "github.com/willabides/octoql/internal/testutil.ID"},
		"DateTime":        {Type: "time.Time"},
		"PreciseDateTime": {Type: "time.Time"},
		"URI":             {Type: "string"},
		"GitObjectID":     {Type: "string"},
		"BigInt":          {Type: "int64"},
		"Date":            {Type: "time.Time", Marshaler: "github.com/willabides/octoql/internal/testutil.MarshalDate", Unmarshaler: "github.com/willabides/octoql/internal/testutil.UnmarshalDate"},
		"JSON":            {Type: "interface{}"},
		"ComplexJSON":     {Type: "[]map[string]*[]*map[string]interface{}"},
		"Account":         {Type: "github.com/willabides/octoql/internal/testutil.Account"},
	}
}

func addTestScalarBindings(bindings map[string]*TypeBinding) map[string]*TypeBinding {
	if bindings == nil {
		bindings = make(map[string]*TypeBinding)
	}
	for name, binding := range testBindings() {
		if name == "Account" {
			continue
		}
		if _, ok := bindings[name]; !ok {
			bindings[name] = binding
		}
	}
	return bindings
}

// buildGoFile returns an error if the given Go code is not valid.
//
// namePrefix is used for the temp-file, and is just for debugging.
func buildGoFile(namePrefix string, content []byte) error {
	// We need to put this within the current module, rather than in
	// /tmp, so that it can access internal/testutil.
	f, err := os.CreateTemp("./testdata/tmp", namePrefix+"_*.go")
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	_, err = f.Write(content)
	if err != nil {
		return err
	}

	cmd := exec.Command("go", "build", f.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("generated code does not compile: %w", err)
	}
	return nil
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
	for _, sourceFilename := range []string{
		"GraphShapes.graphql",
		"Naming.graphql",
		"PredeclaredOperationNames.graphql",
	} {
		goFilename := sourceFilename + ".go"
		queriesFilename := sourceFilename + ".json"

		t.Run(sourceFilename, func(t *testing.T) {
			generated, err := Generate(&Config{
				Schema:           []string{filepath.Join(dataDir, "schema.graphql")},
				Operations:       []string{filepath.Join(dataDir, sourceFilename)},
				Package:          "test",
				Generated:        goFilename,
				ExportOperations: queriesFilename,
				ContextType:      "-",
				Bindings:         testBindings(),
			})
			if err != nil {
				t.Fatal(err)
			}

			for filename, content := range generated {
				t.Run(filename, func(t *testing.T) {
					if filepath.Ext(filename) == ".json" && sourceFilename != "GraphShapes.graphql" {
						return
					}
					matchGeneratedSnapshot(t, filename, content)
				})
			}
		})
	}
}

func TestGenerateDeterministic(t *testing.T) {
	config := &Config{
		Schema:      []string{filepath.Join(dataDir, "schema.graphql")},
		Operations:  []string{filepath.Join(dataDir, "GraphShapes.graphql")},
		Package:     "test",
		Generated:   "generated.go",
		ContextType: "-",
		Bindings:    testBindings(),
	}
	first, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first[config.Generated], second[config.Generated]) {
		t.Fatal("generation output is not deterministic")
	}
}

func TestGenerateInlinesExecutionWithOperationVariableNames(t *testing.T) {
	dir := t.TempDir()
	schema := `
type Query {
  value(
    ctx: String!
    client: String!
    err: String!
    vars: String!
    response: String!
    hasData: String!
  ): String!
}
`
	operation := `
query Value(
  $ctx: String!
  $client: String!
  $err: String!
  $vars: String!
  $response: String!
  $hasData: String!
) {
  value(
    ctx: $ctx
    client: $client
    err: $err
    vars: $vars
    response: $response
    hasData: $hasData
  )
}
`
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(operation), 0o600))

	config := &Config{
		Schema:      []string{schemaPath},
		Operations:  []string{operationPath},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "collision",
		ContextType: "-",
	}
	generated, err := Generate(config)
	require.NoError(t, err)

	source := string(generated[config.Generated])
	assert.NotContains(t, source, "func __octoqlDo")
	assert.NotContains(t, source, "__octoqlPartialDataError")
	assert.Contains(t, source, "type ValueVariables struct")
	assert.Contains(t, source, "vars ValueVariables,")
	assert.Contains(t, source, "Variables:     &vars,")
	assert.NotContains(t, source, "variables_2 := ValueVariables")
	assert.Contains(t, source, "var response ValueResponse")
	assert.Contains(t, source, "hasData, err := client.Execute(")
	assert.Contains(t, source, "type ValuePartialDataError struct {\n\tdata *ValueResponse\n\terr  error\n}")
	assert.Contains(t, source, "func (e *ValuePartialDataError) Error() string")
	assert.Contains(t, source, "func (e *ValuePartialDataError) Unwrap() error")
	assert.Contains(t, source, "func (e *ValuePartialDataError) PartialData() *ValueResponse")
	require.NoError(t, buildGoFile("inline_execution_collision", []byte(source)))
}

func TestGenerateQuotesOperationText(t *testing.T) {
	dir := t.TempDir()
	schema := `
type Query {
  echo(value: String!): String!
}
`
	operation := "query Backtick {\n  echo(value: \"contains ` backtick\")\n}\n"
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(operation), 0o600))

	config := &Config{
		Schema:           []string{schemaPath},
		Operations:       []string{operationPath},
		Generated:        filepath.Join(dir, "generated.go"),
		ExportOperations: filepath.Join(dir, "operations.json"),
		Package:          "backtick",
		ContextType:      "-",
	}
	generated, err := Generate(config)
	require.NoError(t, err)

	var exported exportedOperations
	require.NoError(t, json.Unmarshal(generated[config.ExportOperations], &exported))
	require.Len(t, exported.Operations, 1)
	assert.Contains(t, exported.Operations[0].Body, "`")

	source := string(generated[config.Generated])
	quotedBody := strconv.Quote(exported.Operations[0].Body)
	assert.Contains(t, source, "const Backtick_Operation = "+quotedBody)
	assert.NotContains(t, source, "const Backtick_Operation = `")
	require.NoError(t, buildGoFile("quoted_operation", []byte(source)))
}

func TestGenerateWithTestHandlerUsesOnePlan(t *testing.T) {
	for _, strategy := range []TestHandlerTypeStrategy{
		TestHandlerTypesClient,
		TestHandlerTypesLocal,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			testGenerateWithTestHandlerUsesOnePlan(t, strategy)
		})
	}
}

func testGenerateWithTestHandlerUsesOnePlan(
	t *testing.T,
	strategy TestHandlerTypeStrategy,
) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "test-handler-plan-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err = os.RemoveAll(tempDir)
		if err != nil {
			t.Errorf("removing temporary generation directory: %v", err)
		}
	})

	config := &Config{
		Schema:               []string{filepath.Join(dataDir, "schema.graphql")},
		Operations:           []string{filepath.Join(dataDir, "GraphShapes.graphql")},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		TestHandlerTypes:     strategy,
		ContextType:          "-",
		Bindings:             testBindings(),
	}
	err = config.ValidateAndFillDefaults("")
	if err != nil {
		t.Fatal(err)
	}

	planCount := 0
	outputs, err := generateWith(
		config,
		func(config *Config) (*generationPlan, error) {
			planCount++
			return buildGenerationPlan(config)
		},
		renderClient,
		renderTestHandler,
	)
	if err != nil {
		t.Fatal(err)
	}
	if planCount != 1 {
		t.Fatalf("plan builds = %d, want 1", planCount)
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

	compileGeneratedOutputs(t, tempDir, outputs)
}

func TestGenerateLocalTestHandlerBindings(t *testing.T) {
	newConfig := func(
		t *testing.T,
		schema string,
		operation string,
	) (*Config, string) {
		tempRoot := filepath.Join("testdata", "tmp")
		err := os.MkdirAll(tempRoot, 0o755)
		require.NoError(t, err)
		tempDir, err := os.MkdirTemp(tempRoot, "test-handler-binding-")
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, os.RemoveAll(tempDir))
		})
		schemaPath := filepath.Join(tempDir, "schema.graphql")
		err = os.WriteFile(schemaPath, []byte(schema), 0o600)
		require.NoError(t, err)
		operationPath := filepath.Join(tempDir, "operation.graphql")
		err = os.WriteFile(operationPath, []byte(operation), 0o600)
		require.NoError(t, err)
		config := &Config{
			Schema:               []string{schemaPath},
			Operations:           []string{operationPath},
			Generated:            filepath.Join(tempDir, "client", "generated.go"),
			TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
			TestHandlerTypes:     TestHandlerTypesLocal,
			Package:              "client",
			ContextType:          "-",
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
		config.Bindings = map[string]*TypeBinding{
			"Bound": {Type: config.pkgPath + ".ClientScalar"},
		}

		outputs, err := Generate(config)

		require.Error(t, err)
		assert.Nil(t, outputs)
		assert.Contains(t, err.Error(), "test_handler.types local cannot use")
		assert.Contains(t, err.Error(), "generated client package")
		assert.Contains(t, err.Error(), "ClientScalar")
	})

	t.Run("reachable client unmarshaler rejected", func(t *testing.T) {
		config, _ := newConfig(
			t,
			"scalar Bound\ntype Query { value(input: Bound): Boolean! }\n",
			"query Value($input: Bound) { value(input: $input) }\n",
		)
		config.Bindings = map[string]*TypeBinding{
			"Bound": {
				Type:        "string",
				Unmarshaler: config.pkgPath + ".UnmarshalBound",
			},
		}

		outputs, err := Generate(config)

		require.Error(t, err)
		assert.Nil(t, outputs)
		assert.Contains(t, err.Error(), "test_handler.types local cannot use")
		assert.Contains(t, err.Error(), "generated client package")
		assert.Contains(t, err.Error(), "UnmarshalBound")
	})

	t.Run("reachable client marshaler rejected", func(t *testing.T) {
		config, _ := newConfig(
			t,
			"scalar Bound\ntype Query { value: Bound }\n",
			"query Value { value }\n",
		)
		config.Bindings = map[string]*TypeBinding{
			"Bound": {
				Type:      "string",
				Marshaler: config.pkgPath + ".MarshalBound",
			},
		}

		outputs, err := Generate(config)

		require.Error(t, err)
		assert.Nil(t, outputs)
		assert.Contains(t, err.Error(), "test_handler.types local cannot use")
		assert.Contains(t, err.Error(), "generated client package")
		assert.Contains(t, err.Error(), "MarshalBound")
	})

	t.Run("handler binding resolves without self import", func(t *testing.T) {
		// This temporary fixture deliberately exports its binding because the
		// generated client imports it from the generated handler package.
		config, tempDir := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]*TypeBinding{
			"Bound": {
				Type:        config.testHandlerPkgPath + ".HandlerScalar",
				Marshaler:   config.testHandlerPkgPath + ".MarshalHandlerScalar",
				Unmarshaler: config.testHandlerPkgPath + ".UnmarshalHandlerScalar",
			},
		}

		outputs, err := Generate(config)

		require.NoError(t, err)
		handlerSource := string(outputs[config.TestHandlerGenerated])
		assert.Contains(t, handlerSource, "HandlerScalar")
		assert.Contains(t, handlerSource, "MarshalHandlerScalar")
		assert.Contains(t, handlerSource, "UnmarshalHandlerScalar")
		assert.NotContains(t, handlerSource, `"`+config.testHandlerPkgPath+`"`)
		outputs[filepath.Join(tempDir, "githubapitest", "support.go")] = []byte(
			`package githubapitest

import "encoding/json"

type HandlerScalar string

func MarshalHandlerScalar(value *HandlerScalar) ([]byte, error) {
	return json.Marshal(value)
}

func UnmarshalHandlerScalar(data []byte, value *HandlerScalar) error {
	return json.Unmarshal(data, value)
}
`,
		)
		compileGeneratedOutputs(t, tempDir, outputs)
	})

	t.Run("external binding and marshalers compile", func(t *testing.T) {
		config, tempDir := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]*TypeBinding{
			"Bound": {
				Type:        "time.Time",
				Marshaler:   "github.com/willabides/octoql/internal/testutil.MarshalDate",
				Unmarshaler: "github.com/willabides/octoql/internal/testutil.UnmarshalDate",
			},
		}

		outputs, err := Generate(config)

		require.NoError(t, err)
		handlerSource := string(outputs[config.TestHandlerGenerated])
		assert.Contains(t, handlerSource, `"time"`)
		assert.Contains(
			t,
			handlerSource,
			`"github.com/willabides/octoql/internal/testutil"`,
		)
		compileGeneratedOutputs(t, tempDir, outputs)
	})

	t.Run("unused client binding does not block", func(t *testing.T) {
		config, tempDir := newConfig(t, scalarSchema, scalarOperation)
		config.Bindings = map[string]*TypeBinding{
			"Bound":  {Type: "string"},
			"Unused": {Type: config.pkgPath + ".ClientScalar"},
		}

		outputs, err := Generate(config)

		require.NoError(t, err)
		assert.NotContains(
			t,
			string(outputs[config.TestHandlerGenerated]),
			`"`+config.pkgPath+`"`,
		)
		compileGeneratedOutputs(t, tempDir, outputs)
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
			err := os.MkdirAll(directory, 0o755)
			require.NoError(t, err)
			err = os.WriteFile(
				filepath.Join(directory, "value.go"),
				[]byte("package shared\n\ntype Value string\n"),
				0o600,
			)
			require.NoError(t, err)
		}
		firstAbsolute, err := filepath.Abs(firstDirectory)
		require.NoError(t, err)
		secondAbsolute, err := filepath.Abs(secondDirectory)
		require.NoError(t, err)
		firstPackage, err := packagePathFromModule(firstAbsolute)
		require.NoError(t, err)
		secondPackage, err := packagePathFromModule(secondAbsolute)
		require.NoError(t, err)
		config.Bindings = map[string]*TypeBinding{
			"First":  {Type: firstPackage + ".Value"},
			"Second": {Type: secondPackage + ".Value"},
		}

		first, err := Generate(config)
		require.NoError(t, err)
		second, err := Generate(config)
		require.NoError(t, err)

		assert.Equal(t, first[config.TestHandlerGenerated], second[config.TestHandlerGenerated])
		handlerSource := string(first[config.TestHandlerGenerated])
		assert.Contains(t, handlerSource, `"`+firstPackage+`"`)
		assert.Contains(t, handlerSource, `shared2 "`+secondPackage+`"`)
		compileGeneratedOutputs(t, tempDir, first)
	})
}

func TestGeneratedHandlerOptionalModeWireParity(t *testing.T) {
	tests := []struct {
		name                string
		optional            string
		optionalGenericType string
	}{
		{name: "value", optional: "value"},
		{name: "pointer", optional: "pointer"},
		{name: "pointer omitempty", optional: "pointer_omitempty"},
		{
			name:                "generic",
			optional:            "generic",
			optionalGenericType: "github.com/willabides/octoql/internal/testutil.Option",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tempRoot := filepath.Join("testdata", "tmp")
			err := os.MkdirAll(tempRoot, 0o755)
			require.NoError(t, err)
			tempDir, err := os.MkdirTemp(tempRoot, "test-handler-optional-wire-")
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, os.RemoveAll(tempDir))
			})

			schemaPath := filepath.Join(tempDir, "schema.graphql")
			err = os.WriteFile(schemaPath, []byte(`
input OptionalInput {
  value: String
  items: [String]
}

type OptionalResult {
  value: String
  items: [String]
}

type Query {
  optional(input: OptionalInput): OptionalResult
}
`), 0o600)
			require.NoError(t, err)
			operationPath := filepath.Join(tempDir, "operation.graphql")
			err = os.WriteFile(operationPath, []byte(`
query Optional($input: OptionalInput) {
  result: optional(input: $input) {
    value
    items
  }
}
`), 0o600)
			require.NoError(t, err)

			clientConfig := optionalModeParityConfig(
				tempDir,
				"client",
				"clienthandler",
				TestHandlerTypesClient,
				test.optional,
				test.optionalGenericType,
			)
			localConfig := optionalModeParityConfig(
				tempDir,
				"localclient",
				"localhandler",
				TestHandlerTypesLocal,
				test.optional,
				test.optionalGenericType,
			)
			err = clientConfig.ValidateAndFillDefaults("")
			require.NoError(t, err)
			err = localConfig.ValidateAndFillDefaults("")
			require.NoError(t, err)
			clientOutputs, err := Generate(clientConfig)
			require.NoError(t, err)
			localOutputs, err := Generate(localConfig)
			require.NoError(t, err)

			outputs := map[string][]byte{
				filepath.Join(tempDir, "doc.go"): []byte("package parity\n"),
				filepath.Join(tempDir, "parity_test.go"): optionalModeParityTestSource(
					clientConfig.testHandlerPkgPath,
					localConfig.testHandlerPkgPath,
					clientConfig.pkgPath,
				),
			}
			for filename, content := range clientOutputs {
				outputs[filename] = content
			}
			for filename, content := range localOutputs {
				outputs[filename] = content
			}
			compileGeneratedOutputs(t, tempDir, outputs)
		})
	}
}

func optionalModeParityConfig(
	tempDir string,
	clientPackage string,
	handlerPackage string,
	strategy TestHandlerTypeStrategy,
	optional string,
	optionalGenericType string,
) *Config {
	config := &Config{
		Schema:               []string{filepath.Join(tempDir, "schema.graphql")},
		Operations:           []string{filepath.Join(tempDir, "operation.graphql")},
		Generated:            filepath.Join(tempDir, clientPackage, "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, handlerPackage, "generated.go"),
		TestHandlerTypes:     strategy,
		Package:              clientPackage,
		ContextType:          "-",
		Optional:             optional,
		OptionalGenericType:  optionalGenericType,
	}
	return config
}

func optionalModeParityTestSource(
	clientHandlerPath string,
	localHandlerPath string,
	generatedClientPath string,
) []byte {
	source := `package parity_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	clienthandler "CLIENT_HANDLER_PATH"
	generatedclient "GENERATED_CLIENT_PATH"
	localhandler "LOCAL_HANDLER_PATH"
)

func TestOptionalModeWireParity(t *testing.T) {
	tests := []struct {
		name      string
		variables string
		response  string
	}{
		{name: "omitted", variables: "{}", response: "{\"result\":null}"},
		{name: "explicit null", variables: "{\"input\":null}", response: "{\"result\":null}"},
		{
			name:      "populated with null list element",
			variables: "{\"input\":{\"value\":\"x\",\"items\":[null,\"y\"]}}",
			response:  "{\"result\":{\"value\":null,\"items\":[null,\"y\"]}}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var clientVariables clienthandler.OptionalVariables
			if err := json.Unmarshal([]byte(test.variables), &clientVariables); err != nil {
				t.Fatal(err)
			}
			var localVariables localhandler.OptionalVariables
			if err := json.Unmarshal([]byte(test.variables), &localVariables); err != nil {
				t.Fatal(err)
			}
			clientVariablesJSON, err := json.Marshal(clientVariables)
			if err != nil {
				t.Fatal(err)
			}
			localVariablesJSON, err := json.Marshal(localVariables)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, clientVariablesJSON, localVariablesJSON)

			var clientResponse clienthandler.OptionalResponse
			if err = json.Unmarshal([]byte(test.response), &clientResponse); err != nil {
				t.Fatal(err)
			}
			var localResponse localhandler.OptionalResponse
			if err = json.Unmarshal([]byte(test.response), &localResponse); err != nil {
				t.Fatal(err)
			}

			clientHandler := clienthandler.NewTestHandler(t)
			clientHandler.DefaultOptional().Respond(clientResponse)
			localHandler := localhandler.NewTestHandler(t)
			localHandler.DefaultOptional().Respond(localResponse)

			originalVariables := json.RawMessage(test.variables)
			clientResult := postOptional(t, clientHandler, originalVariables)
			localResult := postOptional(t, localHandler, originalVariables)
			if clientResult.Code != localResult.Code {
				t.Fatalf("status codes differ: client=%d local=%d", clientResult.Code, localResult.Code)
			}
			if !reflect.DeepEqual(clientResult.Header(), localResult.Header()) {
				t.Fatalf("headers differ: client=%v local=%v", clientResult.Header(), localResult.Header())
			}
			assertJSONEqual(t, clientResult.Body.Bytes(), localResult.Body.Bytes())
		})
	}
}

func postOptional(
	t *testing.T,
	handler http.Handler,
	variables json.RawMessage,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"operationName": "Optional",
		"query":         generatedclient.Optional_Operation,
		"variables":     variables,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://api.github.example/graphql",
		bytes.NewReader(body),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertJSONEqual(t *testing.T, left, right []byte) {
	t.Helper()
	var leftValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatal(err)
	}
	var rightValue any
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leftValue, rightValue) {
		t.Fatalf("JSON differs: left=%s right=%s", left, right)
	}
}
`
	return []byte(strings.NewReplacer(
		"CLIENT_HANDLER_PATH", clientHandlerPath,
		"GENERATED_CLIENT_PATH", generatedClientPath,
		"LOCAL_HANDLER_PATH", localHandlerPath,
	).Replace(source))
}

func TestGenerateWithTestHandlerRendererFailureReturnsNoOutputs(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "test-handler-error-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err = os.RemoveAll(tempDir)
		if err != nil {
			t.Errorf("removing temporary generation directory: %v", err)
		}
	})

	config := &Config{
		Schema:               []string{filepath.Join(dataDir, "schema.graphql")},
		Operations:           []string{filepath.Join(dataDir, "Repository.graphql")},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		ContextType:          "-",
		Bindings:             testBindings(),
	}
	err = config.ValidateAndFillDefaults("")
	if err != nil {
		t.Fatal(err)
	}

	renderErr := errors.New("handler renderer failed")
	outputs, err := generateWith(
		config,
		buildGenerationPlan,
		renderClient,
		func(*generationPlan) ([]byte, error) {
			return nil, renderErr
		},
	)

	if !errors.Is(err, renderErr) {
		t.Fatalf("error = %v, want %v", err, renderErr)
	}
	if outputs != nil {
		t.Fatalf("outputs = %#v, want nil", outputs)
	}
}

func TestGenerateWithoutTestHandlerOnlyGeneratesClient(t *testing.T) {
	config := &Config{
		Schema:      []string{filepath.Join(dataDir, "schema.graphql")},
		Operations:  []string{filepath.Join(dataDir, "Repository.graphql")},
		Generated:   "generated.go",
		Package:     "client",
		ContextType: "-",
		Bindings:    testBindings(),
	}
	outputs, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs = %d, want 1", len(outputs))
	}
	if outputs[config.Generated] == nil {
		t.Fatalf("client output %q is missing", config.Generated)
	}
}

func TestTestHandlerConfigurationErrors(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "test-handler-config-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err = os.RemoveAll(tempDir)
		if err != nil {
			t.Errorf("removing temporary generation directory: %v", err)
		}
	})
	absoluteClientOutput, err := filepath.Abs(
		filepath.Join(tempDir, "client", "generated.go"),
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name             string
		handlerGenerated string
		exportOperations string
		packageName      string
		wantError        string
	}{
		{
			name:             "same package",
			handlerGenerated: filepath.Join(tempDir, "client", "handler.go"),
			wantError:        "separate package",
		},
		{
			name:             "invalid package directory",
			handlerGenerated: filepath.Join(tempDir, "bad-package-name", "handler.go"),
			wantError:        "unable to identify test handler package",
		},
		{
			name:             "client output collision",
			handlerGenerated: filepath.Join(tempDir, "client", "generated.go"),
			wantError:        "output paths must be different",
		},
		{
			name:             "absolute client output collision",
			handlerGenerated: absoluteClientOutput,
			wantError:        "output paths must be different",
		},
		{
			name:             "operation manifest collision",
			handlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
			exportOperations: filepath.Join(tempDir, "githubapitest", "generated.go"),
			wantError:        "output paths must be different",
		},
		{
			name:             "main client package",
			handlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
			packageName:      "main",
			wantError:        "cannot import a generated client in package main",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packageName := test.packageName
			if packageName == "" {
				packageName = "client"
			}
			config := &Config{
				Schema:               []string{filepath.Join(dataDir, "schema.graphql")},
				Operations:           []string{filepath.Join(dataDir, "Repository.graphql")},
				Generated:            filepath.Join(tempDir, "client", "generated.go"),
				TestHandlerGenerated: test.handlerGenerated,
				ExportOperations:     test.exportOperations,
				Package:              packageName,
				ContextType:          "-",
				Bindings:             testBindings(),
			}

			err := config.ValidateAndFillDefaults("")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantError)
			}
		})
	}

	t.Run("local types do not import main client", func(t *testing.T) {
		config := &Config{
			Schema:               []string{filepath.Join(dataDir, "schema.graphql")},
			Operations:           []string{filepath.Join(dataDir, "Repository.graphql")},
			Generated:            filepath.Join(tempDir, "client", "generated.go"),
			TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
			TestHandlerTypes:     TestHandlerTypesLocal,
			Package:              "main",
			ContextType:          "-",
			Bindings:             testBindings(),
		}

		err := config.ValidateAndFillDefaults("")
		require.NoError(t, err)
		outputs, err := Generate(config)
		require.NoError(t, err)
		assert.NotContains(
			t,
			string(outputs[config.TestHandlerGenerated]),
			config.pkgPath+`"`,
		)
	})
}

func TestGenerateTestHandlerNameCollision(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "test-handler-collision-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err = os.RemoveAll(tempDir)
		if err != nil {
			t.Errorf("removing temporary generation directory: %v", err)
		}
	})

	schemaPath := filepath.Join(tempDir, "schema.graphql")
	operationPath := filepath.Join(tempDir, "operation.graphql")
	err = os.WriteFile(schemaPath, []byte("type Query { value: String! }\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(
		operationPath,
		[]byte("# @octoqlgen(typename: \"TestHandler\")\nquery Value { value }\n"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	config := &Config{
		Schema:               []string{schemaPath},
		Operations:           []string{operationPath},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		Package:              "client",
		ContextType:          "-",
	}
	err = config.ValidateAndFillDefaults("")
	if err != nil {
		t.Fatal(err)
	}

	_, err = Generate(config)
	if err == nil || !strings.Contains(err.Error(), "conflicting definition for TestHandler") {
		t.Fatalf("error = %v, want test handler name collision", err)
	}
}

func TestGenerateTestHandlerIdentifierValidation(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}

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
			tempDir, err := os.MkdirTemp(tempRoot, "test-handler-identifiers-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				err = os.RemoveAll(tempDir)
				if err != nil {
					t.Errorf("removing temporary generation directory: %v", err)
				}
			})

			schemaPath := filepath.Join(tempDir, "schema.graphql")
			operationPath := filepath.Join(tempDir, "operation.graphql")
			err = os.WriteFile(schemaPath, []byte(test.schema), 0o600)
			if err != nil {
				t.Fatal(err)
			}
			err = os.WriteFile(operationPath, []byte(test.operation), 0o600)
			if err != nil {
				t.Fatal(err)
			}
			config := &Config{
				Schema:               []string{schemaPath},
				Operations:           []string{operationPath},
				Generated:            filepath.Join(tempDir, "client", "generated.go"),
				TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
				Package:              "client",
				ContextType:          "-",
			}
			err = config.ValidateAndFillDefaults("")
			if err != nil {
				t.Fatal(err)
			}

			_, err = Generate(config)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestGenerateVariablelessHandlerWithVariablesResponseName(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "test-handler-variableless-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err = os.RemoveAll(tempDir)
		if err != nil {
			t.Errorf("removing temporary generation directory: %v", err)
		}
	})

	schemaPath := filepath.Join(tempDir, "schema.graphql")
	operationPath := filepath.Join(tempDir, "operation.graphql")
	err = os.WriteFile(
		schemaPath,
		[]byte("type Viewer { login: String! }\ntype Query { viewer: Viewer! }\n"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(
		operationPath,
		[]byte("fragment ViewerVariables on Viewer { login }\nquery Viewer { viewer { ...ViewerVariables } }\n"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	config := &Config{
		Schema:               []string{schemaPath},
		Operations:           []string{operationPath},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		Package:              "client",
		ContextType:          "-",
	}
	err = config.ValidateAndFillDefaults("")
	if err != nil {
		t.Fatal(err)
	}

	outputs, err := Generate(config)
	require.NoError(t, err)
	handlerSource := string(outputs[config.TestHandlerGenerated])
	assert.NotContains(t, handlerSource, "type ViewerVariables struct")
	assert.Contains(t, handlerSource, "func (s *expectationSet[V]) expect(")
	assert.Contains(t, handlerSource, "func (h *TestHandler) ExpectViewer(")
	assert.Contains(t, handlerSource, "func (b *ViewerExpectation) WithOptions(")
	compileGeneratedOutputs(t, tempDir, outputs)
}

func TestGenerateTestHandlerClientImportAliasAvoidsGeneratedNames(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	for _, packageName := range []string{
		"StatusReady",
		"StatusQueryResponse",
		"ExpectStatusQuery",
		"DefaultStatusQuery",
		"ResetStatusQuery",
	} {
		t.Run(packageName, func(t *testing.T) {
			tempDir, err := os.MkdirTemp(tempRoot, "test-handler-import-alias-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				err = os.RemoveAll(tempDir)
				if err != nil {
					t.Errorf("removing temporary generation directory: %v", err)
				}
			})

			schemaPath := filepath.Join(tempDir, "schema.graphql")
			operationPath := filepath.Join(tempDir, "operation.graphql")
			err = os.WriteFile(
				schemaPath,
				[]byte("enum Status { READY }\ntype Query { status: Status! }\n"),
				0o600,
			)
			if err != nil {
				t.Fatal(err)
			}
			err = os.WriteFile(
				operationPath,
				[]byte("query StatusQuery { status }\n"),
				0o600,
			)
			if err != nil {
				t.Fatal(err)
			}
			config := &Config{
				Schema:               []string{schemaPath},
				Operations:           []string{operationPath},
				Generated:            filepath.Join(tempDir, "client", "generated.go"),
				TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
				Package:              packageName,
				ContextType:          "-",
			}
			err = config.ValidateAndFillDefaults("")
			if err != nil {
				t.Fatal(err)
			}

			outputs, err := Generate(config)
			require.NoError(t, err)
			handlerSource := string(outputs[config.TestHandlerGenerated])
			expectedImport := packageName + `2 "`
			assert.Contains(t, handlerSource, expectedImport)
			compileGeneratedOutputs(t, tempDir, outputs)
		})
	}
}

func compileGeneratedOutputs(
	t *testing.T,
	directory string,
	outputs map[string][]byte,
) {
	t.Helper()

	for filename, content := range outputs {
		err := os.MkdirAll(filepath.Dir(filename), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(filename, content, 0o600)
		require.NoError(t, err)
	}

	command := exec.Command("go", "test", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "generated packages do not compile:\n%s", output)
}

func TestGenerateTestHandlerRejectsSubscription(t *testing.T) {
	tempRoot := filepath.Join("testdata", "tmp")
	err := os.MkdirAll(tempRoot, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "test-handler-subscription-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err = os.RemoveAll(tempDir)
		if err != nil {
			t.Errorf("removing temporary generation directory: %v", err)
		}
	})

	schemaPath := filepath.Join(tempDir, "schema.graphql")
	operationPath := filepath.Join(tempDir, "operation.graphql")
	err = os.WriteFile(
		schemaPath,
		[]byte("type Query { value: String! }\ntype Subscription { value: String! }\n"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(
		operationPath,
		[]byte("subscription Value { value }\n"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	config := &Config{
		Schema:               []string{schemaPath},
		Operations:           []string{operationPath},
		Generated:            filepath.Join(tempDir, "client", "generated.go"),
		TestHandlerGenerated: filepath.Join(tempDir, "githubapitest", "generated.go"),
		Package:              "client",
		ContextType:          "-",
	}
	err = config.ValidateAndFillDefaults("")
	if err != nil {
		t.Fatal(err)
	}

	_, err = Generate(config)
	if err == nil || !strings.Contains(err.Error(), "subscriptions are not supported by octoql") {
		t.Fatalf("error = %v, want subscription rejection", err)
	}
}

func TestGenerateWithConfig(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *Config, map[string][]byte)
		config     *Config
		operations []string
		name       string
	}{
		{
			name:   "export operations",
			config: &Config{ExportOperations: "operations.json"},
			check: func(t *testing.T, config *Config, generated map[string][]byte) {
				t.Helper()
				require.Len(t, generated, 2)
				manifest := generated[config.ExportOperations]
				assert.True(t, json.Valid(manifest))
				assert.Contains(t, string(manifest), `"operationName": "GetRepository"`)
			},
		},
		{
			name: "custom context",
			config: &Config{
				ContextType: "github.com/willabides/octoql/internal/testutil.MyContext",
			},
			check: func(t *testing.T, config *Config, generated map[string][]byte) {
				t.Helper()
				source := string(generated[config.Generated])
				assert.Contains(t, source, `"github.com/willabides/octoql/internal/testutil"`)
				assert.Contains(t, source, "ctx testutil.MyContext")
			},
		},
		{
			name:       "struct references and optional pointer",
			operations: []string{"Inputs.graphql"},
			config: &Config{
				StructReferences: true,
				Optional:         "pointer",
				Bindings:         testBindings(),
			},
			check: func(t *testing.T, config *Config, generated map[string][]byte) {
				t.Helper()
				source := string(generated[config.Generated])
				assert.NotContains(t, source, "func (v *__GitHubInputsInput) Get")
				matchGeneratedSnapshot(t, config.Generated, generated[config.Generated])
			},
		},
		{
			name:       "package binding",
			operations: []string{"Bindings.graphql"},
			config: &Config{
				PackageBindings: []*PackageBinding{
					{Package: "github.com/willabides/octoql/internal/testutil"},
				},
			},
			check: func(t *testing.T, config *Config, generated map[string][]byte) {
				t.Helper()
				assert.Contains(t, string(generated[config.Generated]), "Account testutil.Account")
			},
		},
		{
			name:       "exact fields binding",
			operations: []string{"Bindings.graphql"},
			config: &Config{
				Bindings: map[string]*TypeBinding{
					"Account": {
						Type:              "github.com/willabides/octoql/internal/testutil.Account",
						ExpectExactFields: "{ id login }",
					},
				},
			},
			check: func(t *testing.T, config *Config, generated map[string][]byte) {
				t.Helper()
				assert.Contains(t, string(generated[config.Generated]), "Account testutil.Account")
			},
		},
		{
			name:       "raw enum casing",
			operations: []string{"OptionalModes.graphql"},
			config: &Config{
				Bindings: testBindings(),
				Casing: Casing{
					Enums: map[string]CasingAlgorithm{"IssueState": CasingRaw},
				},
			},
			check: func(t *testing.T, config *Config, generated map[string][]byte) {
				t.Helper()
				source := string(generated[config.Generated])
				assert.Contains(t, source, "IssueState_OPEN")
				assert.NotContains(t, source, "IssueStateOpen")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.config
			err := config.ValidateAndFillDefaults(dataDir)
			config.Schema = []string{filepath.Join(dataDir, "schema.graphql")}
			if test.name != "package binding" && test.name != "exact fields binding" {
				config.Bindings = addTestScalarBindings(config.Bindings)
			}
			require.NoError(t, err)
			operations := test.operations
			if operations == nil {
				operations = []string{"Repository.graphql"}
			}
			config.Operations = []string{filepath.Join(dataDir, operations[0])}
			generated, err := Generate(config)
			require.NoError(t, err)
			test.check(t, config, generated)
			require.NoError(t, buildGoFile(test.name, generated[config.Generated]))
		})
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
	files, err := os.ReadDir(errorsDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		sourceFilename := file.Name()
		if !strings.HasSuffix(sourceFilename, ".graphql") &&
			!strings.HasSuffix(sourceFilename, ".go") ||
			strings.HasSuffix(sourceFilename, ".schema.graphql") ||
			sourceFilename == "schema.graphql" {
			continue
		}

		baseFilename := strings.TrimSuffix(sourceFilename, filepath.Ext(sourceFilename))
		testFilename := strings.ReplaceAll(sourceFilename, ".", "/")

		// Schema is either <base>.schema.graphql, or <dir>/schema.graphql if
		// that doesn't exist.
		schemaFilename := baseFilename + ".schema.graphql"
		if _, err := os.Stat(filepath.Join(errorsDir, schemaFilename)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				schemaFilename = "schema.graphql"
			} else {
				t.Fatal(err)
			}
		}

		t.Run(testFilename, func(t *testing.T) {
			_, err := Generate(&Config{
				Schema:      []string{filepath.Join(errorsDir, schemaFilename)},
				Operations:  []string{filepath.Join(errorsDir, sourceFilename)},
				Package:     "test",
				Generated:   os.DevNull,
				ContextType: "context.Context",
				Bindings: map[string]*TypeBinding{
					"ValidScalar":   {Type: "string"},
					"InvalidScalar": {Type: "bogus"},
					"Account": {
						Type:              "github.com/willabides/octoql/internal/testutil.Account",
						ExpectExactFields: "{ id login }",
					},
				},
			})
			if err == nil {
				t.Fatal("expected an error")
			}

			switch sourceFilename {
			case "BindingWithIncorrectSelection.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("invalid selection for type-binding Account: testdata/errors/BindingWithIncorrectSelection.schema.graphql:2: expected 2 fields, got 1"))
			case "BindingWithIncorrectSelection.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("invalid selection for type-binding Account: testdata/errors/BindingWithIncorrectSelection.graphql:2: expected field 1 to be login, got id"))
			case "ConflictingDirectiveArguments.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingDirectiveArguments.graphql:2: conflicting values for pointer"))
			case "ConflictingDirectives.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingDirectives.graphql:3: conflicting values for pointer"))
			case "ConflictingEnumValues.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingEnumValues.schema.graphql:4: enum values FIRST_VALUE and first_value have conflicting Go name AnnoyingEnumFirstValue; add 'all_enums: raw' or 'enums: AnnoyingEnum: raw' to 'casing' in octoqlgen.yaml to fix"))
			case "ConflictingSelections.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingSelections.go:4: operations must have operation-names"))
			case "ConflictingSelections.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingSelections.graphql:1: operations must have operation-names"))
			case "ConflictingTypeNameAndForFieldBind.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNameAndForFieldBind.graphql:5: typename and bind may not be used together"))
			case "ConflictingTypeNameAndGlobalBind.graphql":
				want := "testdata/errors/ConflictingTypeNameAndGlobalBind.graphql:4: typename option conflicts with global binding for ValidScalar; use `bind: \"-\"` to override it"
				if got := err.Error(); got != want {
					t.Errorf("error = %q, want %q", got, want)
				}
			case "ConflictingTypeNameAndLocalBind.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNameAndLocalBind.graphql:4: typename and bind may not be used together"))
			case "ConflictingTypeNames.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("invalid Go file testdata/errors/ConflictingTypeNames.go: testdata/errors/ConflictingTypeNames.go:3:1: expected declaration, found _"))
			case "ConflictingTypeNames.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNames.schema.graphql:2: conflicting definition for T; this can indicate either an octoqlgen internal error, a conflict between user-specified type-names, or some very tricksy GraphQL field/type names: expected 2 fields, got 1"))
			case "DefaultInputsNoOmitPointer.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/DefaultInputsNoOmitPointer.graphql:4: pointer on non-null input field can only be used together with omitempty: InputWithDefaults.field"))
			case "DefaultInputsNoOmitPointerForDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/DefaultInputsNoOmitPointerForDirective.graphql:5: pointer on non-null input field can only be used together with omitempty: InputWithDefaults.field"))
			case "FlattenField.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/FlattenField.graphql:3: flatten is not yet supported for fields (only fragment spreads)"))
			case "FlattenImplementation.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/FlattenImplementation.graphql:4: flatten is not allowed for fields with fragment-spreads unless the field-type implements the fragment-type; field-type I does not implement fragment-type T"))
			case "InvalidQuery.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/InvalidQuery.go:4: query-spec does not match schema: Cannot query field "g" on type "Query". Did you mean "f"?`))
			case "InvalidQuery.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/InvalidQuery.graphql:1: query-spec does not match schema: Cannot query field "g" on type "Query". Did you mean "f"?`))
			case "InvalidScalar.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`invalid type-name "bogus" (unknown type-name "bogus"); expected a builtin, path/to/package.Name, interface{}, or a slice, map, or pointer of those`))
			case "InvalidScalar.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`invalid type-name "bogus" (unknown type-name "bogus"); expected a builtin, path/to/package.Name, interface{}, or a slice, map, or pointer of those`))
			case "InvalidSchemaSyntax.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/InvalidSchemaSyntax.schema.graphql:4: invalid schema: Expected :, found }"))
			case "InvalidSchemaSyntax.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/InvalidSchemaSyntax.schema.graphql:4: invalid schema: Expected :, found }"))
			case "InvalidSchemaWithBuiltins.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/InvalidSchemaWithBuiltins.schema.graphql:3: invalid schema: Undefined type Bogus."))
			case "InvalidSchemaWithoutBuiltins.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/InvalidSchemaWithoutBuiltins.schema.graphql:3: invalid schema: Undefined type Bogus."))
			case "KeywordArgumentName.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/KeywordArgumentName.graphql:1: variable name must not be a go keyword"))
			case "KeywordOperationName.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/KeywordOperationName.graphql:1: operation name must not be a go keyword"))
			case "KeywordTypeName.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/KeywordTypeName.schema.graphql:1: typename option must not be a go keyword"))
			case "NoMutationType.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/NoMutationType.graphql:1: query-spec does not match schema: Schema does not support operation type "mutation"`))
			case "NoQuery.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("no queries found, looked in: testdata/errors/NoQuery.go (configure this in octoqlgen.yaml)"))
			case "NoQuery.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("no queries found, looked in: testdata/errors/NoQuery.graphql (configure this in octoqlgen.yaml)"))
			case "NoQueryType.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/NoQueryType.graphql:1: query-spec does not match schema: Schema does not support operation type "query"`))
			case "OmitemptyDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/OmitemptyDirective.graphql:4: omitempty may only be used on optional arguments: OmitemptyInput.field"))
			case "OmitemptyForDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/OmitemptyForDirective.graphql:4: omitempty may only be used on optional arguments: OmitemptyInput.field"))
			case "PartialDataErrorNameCollision.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`generated partial data error "GetUserPartialDataError" conflicts with a generated GraphQL type`))
			case "PartialDataErrorEnumValueCollision.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`generated partial data error "FooPartialDataError" conflicts with a generated enum value`))
			case "PartialDataErrorOperationNameCollision.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`generated partial data error "GetUserPartialDataError" conflicts with operation "GetUserPartialDataError"`))
			case "StructOptionOnObject.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/StructOptionOnObject.graphql:3: struct is only applicable to interface-typed fields"))
			case "StructOptionWithFragments.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/StructOptionWithFragments.graphql:3: struct is not allowed for types with fragments"))
			case "UnknownScalar.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/UnknownScalar.schema.graphql:3: unknown scalar UnknownScalar: please add it to "bindings" in octoqlgen.yaml
Example: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml`))
			case "UnknownScalar.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/UnknownScalar.schema.graphql:3: unknown scalar UnknownScalar: please add it to "bindings" in octoqlgen.yaml
Example: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml`))
			default:
				t.Fatalf("missing inline snapshot for %s", sourceFilename)
			}
		})
	}
}

func matchGeneratedSnapshot(t *testing.T, filename string, content []byte) {
	t.Helper()

	extension := filepath.Ext(filename)
	snaps.WithConfig(
		snaps.Dir(filepath.Join("testdata", "snapshots")),
		snaps.Filename(normalizeSnapshotName(t.Name())),
		snaps.Ext(extension),
		snaps.Raw(),
	).MatchStandaloneSnapshot(t, string(content))

	// Generated Go remains external because this compiles the snapshot file that
	// was compared above. JSON output stays alongside the generated Go artifact.
	if extension != ".go" || testing.Short() {
		return
	}
	snapshot, err := os.ReadFile(standaloneSnapshotFilename(t, extension))
	if err != nil {
		t.Fatal(err)
	}
	if err := buildGoFile(normalizeSnapshotName(t.Name()), snapshot); err != nil {
		t.Error(err)
	}
}

func standaloneSnapshotFilename(t *testing.T, extension string) string {
	return filepath.Join(
		"testdata",
		"snapshots",
		normalizeSnapshotName(t.Name())+"_1.snap"+extension,
	)
}

func normalizeSnapshotName(name string) string {
	return strings.NewReplacer("/", "_", `\`, "_").Replace(name)
}
