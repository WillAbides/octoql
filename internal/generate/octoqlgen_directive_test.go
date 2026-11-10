package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/willabides/octoql/internal"
)

const splitSchema = `
type Query {
  a(filter: Filter): Thing
  b(filter: Filter): Thing
}

interface Node {
  id: ID!
}

type Item implements Node {
  id: ID!
}

type Thing {
  name: String
  size: Int
  node: Node
}

input Filter {
  label: String
}
`

func assertDeclares(t *testing.T, source, declaration string) {
	t.Helper()
	assert.Contains(t,
		strings.Join(strings.Fields(source), " "),
		strings.Join(strings.Fields(declaration), " "))
}

func generateSplit(t *testing.T, operation string) (string, error) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	generatedPath := filepath.Join(dir, "generated.go")
	require.NoError(t, os.WriteFile(schemaPath, []byte(splitSchema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(operation), 0o600))

	generated, err := Generate(&Config{
		Schema:      []string{schemaPath},
		Operations:  []string{operationPath},
		Generated:   generatedPath,
		Package:     "client",
		ContextType: "-",
	})
	if err != nil {
		return "", err
	}
	return string(generated[generatedPath]), nil
}

func TestDirectiveNeverReachesTheServer(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	err := os.WriteFile(schemaPath, []byte(`
type Query {
  viewer: User!
}

type User {
  name: String
  isSuspended: Boolean
}
`), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(operationPath, []byte(`
query Viewer($skipName: Boolean!) @octoqlgen(typename: "ViewerResp") {
  viewer @octoqlgen(typename: "ViewerUser") {
    name @skip(if: $skipName)
    isSuspended @octoqlgen(pointer: false)
  }
}
`), 0o600)
	require.NoError(t, err)

	generated, err := Generate(&Config{
		Schema:      []string{schemaPath},
		Operations:  []string{operationPath},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "client",
		ContextType: "-",
	})

	require.NoError(t, err)
	source := string(generated[filepath.Join(dir, "generated.go")])

	assert.Contains(t, source, "type ViewerResp struct")
	assert.Contains(t, source, "type ViewerUser struct")
	assert.NotContains(t, source, "@octoqlgen")
	assert.Contains(t, source, `@skip(if: $skipName)`)
}

func TestOctoqlgenDirectiveVariableOmitempty(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		wantField string
	}{
		{
			name: "operation defaults omitempty with variable default",
			operation: `
query Value($input: String! = "default") @octoqlgenDefaults(pointer: true, omitempty: true) {
  value(input: $input)
}
`,
			wantField: `Input *string ` + "`json:\"input,omitempty\"`",
		},
		{
			name: "local omitempty with variable default",
			operation: `
query Value($input: String! = "default" @octoqlgen(pointer: true, omitempty: true)) {
  value(input: $input)
}
`,
			wantField: `Input *string ` + "`json:\"input,omitempty\"`",
		},
		{
			name: "local omitempty disable for required variable",
			operation: `
query Value($input: String! @octoqlgen(omitempty: false)) @octoqlgenDefaults(omitempty: true) {
  value(input: $input)
}
`,
			wantField: `Input string ` + "`json:\"input\"`",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			schemaPath := filepath.Join(dir, "schema.graphql")
			operationPath := filepath.Join(dir, "operation.graphql")
			require.NoError(t, os.WriteFile(schemaPath, []byte("type Query {\n  value(input: String!): String!\n}\n"), 0o600))
			require.NoError(t, os.WriteFile(operationPath, []byte(test.operation), 0o600))

			generated, err := Generate(&Config{
				Schema:      []string{schemaPath},
				Operations:  []string{operationPath},
				Generated:   filepath.Join(dir, "generated.go"),
				Package:     "client",
				ContextType: "-",
			})

			require.NoError(t, err)
			assertDeclares(t, string(generated[filepath.Join(dir, "generated.go")]), test.wantField)
		})
	}
}

func TestGeneratorOwnsTheDirectiveDefinition(t *testing.T) {
	const baseSchema = `
type Query {
  viewer: User!
}

type User {
  isSuspended: Boolean
}
`
	for name, declaration := range map[string]string{
		"no declaration":      "",
		"current declaration": "\n" + internal.DirectiveFileContents,
		"stale declaration":   "\ndirective @octoqlgen(alias: String) on FIELD\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			schemaPath := filepath.Join(dir, "schema.graphql")
			operationPath := filepath.Join(dir, "operation.graphql")
			require.NoError(t, os.WriteFile(
				schemaPath, []byte(baseSchema+declaration), 0o600))
			require.NoError(t, os.WriteFile(operationPath, []byte(`
query Viewer @octoqlgen(typename: "Resp") {
  viewer {
    isSuspended @octoqlgen(pointer: false)
  }
}
`), 0o600))

			generated, err := Generate(&Config{
				Schema:      []string{schemaPath},
				Operations:  []string{operationPath},
				Generated:   filepath.Join(dir, "generated.go"),
				Package:     "client",
				ContextType: "-",
			})

			require.NoError(t, err)
			source := string(generated[filepath.Join(dir, "generated.go")])
			assert.Contains(t, source, "type Resp struct")
			assertDeclares(t, source, "IsSuspended bool")
			assert.NotContains(t, source, "@octoqlgen")
		})
	}
}

func TestDirectiveRejectsOptionsThatWouldBeIgnored(t *testing.T) {
	const schema = `
type Query {
  thing(filter: Filter): Thing
}

type Thing {
  name: String
}

input Filter {
  label: String
}
`
	for name, test := range map[string]struct {
		operation string
		wantError string
	}{
		"alias on a variable": {
			operation: `
query Q($f: Filter @octoqlgen(alias: "Renamed")) {
  thing(filter: $f) { name }
}
`,
			wantError: "alias is only applicable to selected fields",
		},
		"omitempty for an output field": {
			operation: `
query Q @octoqlgenFor(field: "Thing.name", omitempty: true) {
  thing { name }
}
`,
			wantError: "omitempty is only applicable to input-type fields",
		},
		"alias on an operation": {
			operation: `
query Q @octoqlgen(alias: "Renamed") {
  thing { name }
}
`,
			wantError: "alias is only applicable to selected fields, not operations",
		},
		"alias on a fragment": {
			operation: `
query Q { thing { ...F } }
fragment F on Thing @octoqlgen(alias: "Renamed") { name }
`,
			wantError: "alias is only applicable to selected fields, not fragment-definitions",
		},
		"typename on a fragment": {
			operation: `
query Q { thing { ...F } }
fragment F on Thing @octoqlgen(typename: "Renamed") { name }
`,
			wantError: "the generated type is named after the fragment",
		},
		"alias for an input-type field": {
			operation: `
query Q($f: Filter) @octoqlgenFor(field: "Filter.label", alias: "Renamed") {
  thing(filter: $f) { name }
}
`,
			wantError: "alias is only applicable to selected fields",
		},
		"empty alias": {
			operation: `
query Q {
  thing { name @octoqlgen(alias: "") }
}
`,
			wantError: "alias must not be empty",
		},
		"empty bind": {
			operation: `
query Q {
  thing { name @octoqlgen(bind: "") }
}
`,
			wantError: "bind must not be empty",
		},
		"empty typename": {
			operation: `
query Q @octoqlgen(typename: "") {
  thing { name }
}
`,
			wantError: "typename must not be empty",
		},
		"variable boolean argument": {
			operation: `
query Q($p: Boolean) {
  thing { name @octoqlgen(pointer: $p) }
}
`,
			wantError: "expected boolean value for pointer",
		},
		"null string argument": {
			operation: `
query Q @octoqlgen(typename: null) {
  thing { name }
}
`,
			wantError: "expected string value for typename",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			schemaPath := filepath.Join(dir, "schema.graphql")
			operationPath := filepath.Join(dir, "operation.graphql")
			require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))
			require.NoError(t, os.WriteFile(operationPath, []byte(test.operation), 0o600))

			_, err := Generate(&Config{
				Schema:      []string{schemaPath},
				Operations:  []string{operationPath},
				Generated:   filepath.Join(dir, "generated.go"),
				Package:     "client",
				ContextType: "-",
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
}

func TestForRejectsConflictingDeclarations(t *testing.T) {
	for name, operation := range map[string]string{
		"input type": `
query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }
query QB($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: true) { b(filter: $f) { name } }
`,
		"input type, declared in the other order": `
query QB($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: true) { b(filter: $f) { name } }
query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }
`,
		"response type shared by typename": `
query QA @octoqlgenFor(field: "Thing.name", pointer: false) { a @octoqlgen(typename: "Shared") { name } }
query QB @octoqlgenFor(field: "Thing.name", pointer: true) { b @octoqlgen(typename: "Shared") { name } }
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := generateSplit(t, operation)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "conflicting @octoqlgenFor declarations for")
			assert.Contains(t, err.Error(), "must agree")
		})
	}
}

func TestForAllowsAgreeingDeclarations(t *testing.T) {
	declaring := `query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }`
	silent := `query QB($f: Filter) { b(filter: $f) { name } }`

	for name, operation := range map[string]string{
		"identical declarations": `
query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }
query QB($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { b(filter: $f) { name } }
`,
		"split agreeing declarations": `
query QA($f: Filter)
  @octoqlgenFor(field: "Filter.label", pointer: false)
  @octoqlgenFor(field: "Filter.label", bind: "-")
{ a(filter: $f) { name } }
query QB($f: Filter)
  @octoqlgenFor(field: "Filter.label", pointer: false)
  @octoqlgenFor(field: "Filter.label", bind: "-")
{ b(filter: $f) { name } }
`,
		"declaration first": declaring + "\n" + silent,
		"silence first":     silent + "\n" + declaring,
	} {
		t.Run(name, func(t *testing.T) {
			source, err := generateSplit(t, operation)

			require.NoError(t, err)
			assertDeclares(t, source, "Label string")
		})
	}
}

func TestDefaultsStructAppliesInsideFragment(t *testing.T) {
	source, err := generateSplit(t, `
query Q { a { ...ThingFields } }
fragment ThingFields on Thing @octoqlgenDefaults(struct: true) {
  node { id }
}
`)

	require.NoError(t, err)
	assert.Contains(t, source, "type ThingFieldsNode struct")
	assert.NotContains(t, source, "type ThingFieldsNode interface")
}

func TestDefaultsSeparatesScopeFromNodeOptions(t *testing.T) {
	t.Run("defaults reach the fields", func(t *testing.T) {
		source, err := generateSplit(t, `
query Q @octoqlgenDefaults(pointer: false) { a { name size } }
`)

		require.NoError(t, err)
		assertDeclares(t, source, "Name string")
		assertDeclares(t, source, "Size int")
	})

	t.Run("defaults do not mutate shared input object fields", func(t *testing.T) {
		source, err := generateSplit(t, `
query QA($f: Filter) @octoqlgenDefaults(pointer: false) { a(filter: $f) { name } }
query QB($f: Filter) { b(filter: $f) { name } }
`)

		require.NoError(t, err)
		assertDeclares(t, source, "Label *string")
	})

	t.Run("the node directive rejects a default", func(t *testing.T) {
		_, err := generateSplit(t, `
query Q @octoqlgen(pointer: false) { a { name } }
`)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not describe an operation or fragment")
		assert.Contains(t, err.Error(), "@octoqlgenDefaults(pointer:)")
	})

	t.Run("the node directive still names the response type", func(t *testing.T) {
		source, err := generateSplit(t, `
query Q @octoqlgen(typename: "Resp") { a { name } }
`)

		require.NoError(t, err)
		assert.Contains(t, source, "type Resp struct")
	})
}

func TestOperationsResolveAgainstOnDiskSchema(t *testing.T) {
	schemaFilename := filepath.Join(dataDir, "schema.graphql")
	directiveFilename := filepath.Join(dataDir, internal.DirectiveFilename)

	sources := make([]*ast.Source, 0, 2)
	for _, filename := range []string{schemaFilename, directiveFilename} {
		text, err := os.ReadFile(filename)
		require.NoError(t, err)
		sources = append(sources, &ast.Source{Name: filename, Input: string(text)})
	}

	schema, schemaErr := gqlparser.LoadSchema(sources...)
	require.Nil(t, schemaErr)

	operationFilenames, err := filepath.Glob(filepath.Join(dataDir, "*.graphql"))
	require.NoError(t, err)

	checked := 0
	for _, operationFilename := range operationFilenames {
		if operationFilename == schemaFilename || operationFilename == directiveFilename {
			continue
		}
		text, readErr := os.ReadFile(operationFilename)
		require.NoError(t, readErr)
		document, parseErr := parser.ParseQuery(&ast.Source{
			Name:  operationFilename,
			Input: string(text),
		})
		require.Nil(t, parseErr)

		t.Run(filepath.Base(operationFilename), func(t *testing.T) {
			for _, diagnostic := range validator.ValidateWithRules(schema, document, nil) {
				t.Errorf("an editor would report: %s", diagnostic.Message)
			}
		})
		checked++
	}
	assert.Positive(t, checked)
}

func TestDirectiveFilesAreCurrent(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")

	for _, path := range []string{
		"cmd/octoqlgen/internal/schema/githubapi/" + internal.DirectiveFilename,
		"example/" + internal.DirectiveFilename,
		"internal/generate/testdata/queries/" + internal.DirectiveFilename,
		"internal/generatefeatures/githubdefaults/" + internal.DirectiveFilename,
		"internal/generatefeatures/nocontext/" + internal.DirectiveFilename,
		"internal/handlertest/" + internal.DirectiveFilename,
		"internal/integration/" + internal.DirectiveFilename,
	} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		require.NoError(t, err)
		assert.Equal(t, internal.DirectiveFileContents, string(data),
			"%s is out of date, copy directive.FileContents over it", path)
	}
}
