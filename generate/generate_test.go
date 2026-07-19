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

	"github.com/gkampitakis/go-snaps/snaps"
	"gopkg.in/yaml.v2"
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
		{"PackageBindings", []string{"Bindings.graphql"}, &Config{
			PackageBindings: []*PackageBinding{
				{Package: "github.com/willabides/octoql/internal/testutil"},
			},
		}},
		{"ExactFieldsBinding", []string{"Bindings.graphql"}, &Config{
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
			if test.name != "PackageBindings" && test.name != "ExactFieldsBinding" {
				config.Bindings = addTestScalarBindings(config.Bindings)
			}
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
							matchGeneratedSnapshot(t, filename, content)
						})
					}
				})
			}
		})
	}
}

func TestGenerateWithSubdirectoryConfig(t *testing.T) {
	configDir := filepath.Join(dataDir, "subpackage")
	var config Config
	content, err := os.ReadFile(filepath.Join(configDir, "genqlient.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	err = yaml.UnmarshalStrict(content, &config)
	if err != nil {
		t.Fatal(err)
	}
	err = config.ValidateAndFillDefaults(configDir)
	if err != nil {
		t.Fatal(err)
	}
	config.Bindings = addTestScalarBindings(config.Bindings)

	wantGenerated := filepath.Join(configDir, "generated.go")
	if config.Generated != wantGenerated {
		t.Fatalf("generated path = %q, want %q", config.Generated, wantGenerated)
	}
	if config.Package != "subpackage" {
		t.Fatalf("package = %q, want %q", config.Package, "subpackage")
	}

	generated, err := Generate(&config)
	if err != nil {
		t.Fatal(err)
	}
	matchGeneratedSnapshot(t, config.Generated, generated[config.Generated])
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
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingEnumValues.schema.graphql:4: enum values FIRST_VALUE and first_value have conflicting Go name AnnoyingEnumFirstValue; add 'all_enums: raw' or 'enums: AnnoyingEnum: raw' to 'casing' in genqlient.yaml to fix"))
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
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/ConflictingTypeNames.schema.graphql:2: conflicting definition for T; this can indicate either a genqlient internal error, a conflict between user-specified type-names, or some very tricksy GraphQL field/type names: expected 2 fields, got 1"))
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
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("no queries found, looked in: testdata/errors/NoQuery.go (configure this in genqlient.yaml)"))
			case "NoQuery.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("no queries found, looked in: testdata/errors/NoQuery.graphql (configure this in genqlient.yaml)"))
			case "NoQueryType.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/NoQueryType.graphql:1: query-spec does not match schema: Schema does not support operation type "query"`))
			case "OmitemptyDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/OmitemptyDirective.graphql:4: omitempty may only be used on optional arguments: OmitemptyInput.field"))
			case "OmitemptyForDirective.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/OmitemptyForDirective.graphql:4: omitempty may only be used on optional arguments: OmitemptyInput.field"))
			case "StructOptionOnObject.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/StructOptionOnObject.graphql:3: struct is only applicable to interface-typed fields"))
			case "StructOptionWithFragments.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("testdata/errors/StructOptionWithFragments.graphql:3: struct is not allowed for types with fragments"))
			case "UnknownScalar.go":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/UnknownScalar.schema.graphql:3: unknown scalar UnknownScalar: please add it to "bindings" in genqlient.yaml
Example: https://github.com/willabides/octoql/blob/main/example/genqlient.yaml#L12`))
			case "UnknownScalar.graphql":
				snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline(`testdata/errors/UnknownScalar.schema.graphql:3: unknown scalar UnknownScalar: please add it to "bindings" in genqlient.yaml
Example: https://github.com/willabides/octoql/blob/main/example/genqlient.yaml#L12`))
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
