package generate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"

	"github.com/willabides/octoql/internal/testutil"
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
// asserts that running genqlient on that query produces the generated code in
// the snapshot-file TestName.graphql.go.
//
// To update the snapshots (if the code-generator has changed), run the test
// with `UPDATE_SNAPSHOTS=1`; it will fail the tests and print any diffs, but
// update the snapshots.  Make sure to check that the output is sensible; the
// snapshots don't even get compiled!
func TestGenerate(t *testing.T) {
	files, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		sourceFilename := file.Name()
		if sourceFilename == "schema.graphql" || !strings.HasSuffix(sourceFilename, ".graphql") {
			continue
		}
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
					testutil.Cupaloy.SnapshotT(t, string(content))
				})
			}

			t.Run("Build", func(t *testing.T) {
				if testing.Short() {
					t.Skip("skipping build due to -short")
				}

				err := buildGoFile(sourceFilename, generated[goFilename])
				if err != nil {
					t.Error(err)
				}
			})
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

func getDefaultConfig(t *testing.T) *Config {
	// Parse the config that `genqlient --init` generates, to make sure that
	// works.
	var config Config
	b, err := os.ReadFile("default_genqlient.yaml")
	if err != nil {
		t.Fatal(err)
	}

	err = yaml.UnmarshalStrict(b, &config)
	if err != nil {
		t.Fatal(err)
	}
	return &config
}

func TestGenerateWithConfig(t *testing.T) {
	tests := []struct {
		name       string
		operations []string
		config     *Config
	}{
		{"DefaultConfig", nil, getDefaultConfig(t)},
		{"Subpackage", nil, &Config{
			Generated: "mypkg/myfile.go",
		}},
		{"PackageName", nil, &Config{
			Generated: "myfile.go",
			Package:   "mypkg",
		}},
		{"ExportOperations", nil, &Config{
			ExportOperations: "operations.json",
		}},
		{"CustomContext", nil, &Config{
			ContextType: "github.com/willabides/octoql/internal/testutil.MyContext",
		}},
		{"CustomContextWithAlias", nil, &Config{
			ContextType: "github.com/willabides/octoql/internal/testutil/junk---fun.name.MyContext",
		}},
		{"StructReferences", []string{"Inputs.graphql"}, &Config{
			StructReferences: true,
			Bindings:         testBindings(),
		}},
		{"StructReferencesAndOptionalPointer", []string{"Inputs.graphql"}, &Config{
			StructReferences: true,
			Optional:         "pointer",
			Bindings:         testBindings(),
		}},
		{"PackageBindings", nil, &Config{
			PackageBindings: []*PackageBinding{
				{Package: "github.com/willabides/octoql/internal/testutil"},
			},
		}},
		{"ExactFieldsBinding", nil, &Config{
			Bindings: map[string]*TypeBinding{
				"Account": {
					Type:              "github.com/willabides/octoql/internal/testutil.Account",
					ExpectExactFields: "{ id login }",
				},
			},
		}},
		{"NoContext", nil, &Config{
			ContextType: "-",
		}},
		{"ClientGetter", nil, &Config{
			ClientGetter: "github.com/willabides/octoql/internal/testutil.GetClientFromContext",
		}},
		{"ClientGetterCustomContext", nil, &Config{
			ClientGetter: "github.com/willabides/octoql/internal/testutil.GetClientFromMyContext",
			ContextType:  "github.com/willabides/octoql/internal/testutil.MyContext",
		}},
		{"ClientGetterNoContext", nil, &Config{
			ClientGetter: "github.com/willabides/octoql/internal/testutil.GetClientFromNowhere",
			ContextType:  "-",
		}},
		{"Extensions", nil, &Config{
			Extensions: true,
		}},
		{"VariableNameCollisionsDefault", []string{"OptionalModes.graphql"}, &Config{Bindings: testBindings()}},
		{"VariableNameCollisionsNoContext", []string{"OptionalModes.graphql"}, &Config{
			ContextType: "-",
			Bindings:    testBindings(),
		}},
		{"VariableNameCollisionsClientGetter", []string{"OptionalModes.graphql"}, &Config{
			ClientGetter: "github.com/willabides/octoql/internal/testutil.GetClientFromContext",
			Bindings:     testBindings(),
		}},
		{"OptionalValue", []string{"OptionalModes.graphql"}, &Config{
			Optional: "value",
			Bindings: testBindings(),
		}},
		{"OptionalPointer", []string{"OptionalModes.graphql"}, &Config{
			Optional: "pointer",
			Bindings: testBindings(),
		}},
		{"OptionalGeneric", []string{"OptionalModes.graphql"}, &Config{
			Optional:            "generic",
			OptionalGenericType: "github.com/willabides/octoql/internal/testutil.Option",
			Bindings:            testBindings(),
		}},
		{"EnumRawCasingAll", []string{"OptionalModes.graphql"}, &Config{
			Bindings: testBindings(),
			Casing: Casing{
				AllEnums: CasingRaw,
			},
		}},
		{"EnumRawCasingSpecific", []string{"OptionalModes.graphql"}, &Config{
			Bindings: testBindings(),
			Casing: Casing{
				Enums: map[string]CasingAlgorithm{"IssueState": CasingRaw},
			},
		}},
		{"OptionalPointerOmitEmpty", []string{"Inputs.graphql"}, &Config{
			Optional: "pointer_omitempty",
			Bindings: testBindings(),
		}},
		{"AutoCamelCase", []string{"Naming.graphql"}, &Config{
			Casing: Casing{
				Default: CasingAutoCamelCase,
			},
		}},
	}

	for _, test := range tests {
		config := test.config
		t.Run(test.name, func(t *testing.T) {
			err := config.ValidateAndFillDefaults(dataDir)
			config.Schema = []string{filepath.Join(dataDir, "schema.graphql")}
			config.Bindings = addTestScalarBindings(config.Bindings)
			operationFiles := test.operations
			if operationFiles == nil {
				operationFiles = []string{"Repository.graphql"}
			}

			// Since we often reuse types across test cases, run generation
			// separately for each to avoid conflicts.
			for _, operationFile := range operationFiles {
				t.Run(operationFile, func(t *testing.T) {
					config.Operations = []string{filepath.Join(dataDir, operationFile)}
					if err != nil {
						t.Fatal(err)
					}
					generated, err := Generate(config)
					if err != nil {
						t.Fatal(err)
					}

					for filename, content := range generated {
						t.Run(filename, func(t *testing.T) {
							testutil.Cupaloy.SnapshotT(t, string(content))
						})
					}

					t.Run("Build", func(t *testing.T) {
						if testing.Short() {
							t.Skip("skipping build due to -short")
						}

						err := buildGoFile(operationFile,
							generated[config.Generated])
						if err != nil {
							t.Error(err)
						}
					})
				})
			}
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

			testutil.Cupaloy.SnapshotT(t, err.Error())
		})
	}
}
