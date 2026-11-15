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
	return generateTestSource(t, &testGenerateSpec{
		schema:     splitSchema,
		operations: operation,
	})
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
			source, err := generateTestSource(t, &testGenerateSpec{
				schema: baseSchema + declaration,
				operations: `
query Viewer @octoqlgen(typename: "Resp") {
  viewer {
    isSuspended @octoqlgen(pointer: false)
  }
}
`,
			})
			require.NoError(t, err)
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
			_, err := generateTestSource(t, &testGenerateSpec{
				schema:     schema,
				operations: test.operation,
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

func TestDefaultsSeparatesScopeFromNodeOptions(t *testing.T) {
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
	if schemaErr != nil {
		t.Fatalf("unexpected schema error: %v", schemaErr)
	}

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
		if parseErr != nil {
			t.Fatalf("unexpected parse error: %v", parseErr)
		}

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
		"internal/generatefeatures/clientruntime/" + internal.DirectiveFilename,
		"internal/handlertest/" + internal.DirectiveFilename,
		"internal/integration/" + internal.DirectiveFilename,
	} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		require.NoError(t, err)
		assert.Equal(t, internal.DirectiveFileContents, string(data),
			"%s is out of date, copy internal.DirectiveFileContents over it", path)
	}
}
