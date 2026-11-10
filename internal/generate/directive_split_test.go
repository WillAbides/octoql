package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	for name, operation := range map[string]string{
		"identical declarations": `
query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }
query QB($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { b(filter: $f) { name } }
`,
		"one operation declares nothing": `
query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }
query QB($f: Filter) { b(filter: $f) { name } }
`,
	} {
		t.Run(name, func(t *testing.T) {
			source, err := generateSplit(t, operation)

			require.NoError(t, err)
			assertDeclares(t, source, "Label string")
		})
	}
}

func TestForAllowsSplitAgreeingDeclarations(t *testing.T) {
	source, err := generateSplit(t, `
query QA($f: Filter)
  @octoqlgenFor(field: "Filter.label", pointer: false)
  @octoqlgenFor(field: "Filter.label", bind: "-")
{ a(filter: $f) { name } }
query QB($f: Filter)
  @octoqlgenFor(field: "Filter.label", pointer: false)
  @octoqlgenFor(field: "Filter.label", bind: "-")
{ b(filter: $f) { name } }
`)

	require.NoError(t, err)
	assertDeclares(t, source, "Label string")
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

func TestSharedInputTypeDoesNotDependOnOperationOrder(t *testing.T) {
	declaring := `query QA($f: Filter) @octoqlgenFor(field: "Filter.label", pointer: false) { a(filter: $f) { name } }`
	silent := `query QB($f: Filter) { b(filter: $f) { name } }`

	for name, operation := range map[string]string{
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

func TestDefaultsCannotCarryNamingOptions(t *testing.T) {
	for _, option := range []string{`alias: "Renamed"`, `typename: "Renamed"`, `bind: "string"`} {
		t.Run(option, func(t *testing.T) {
			_, err := generateSplit(t, `
query Q @octoqlgenDefaults(`+option+`) { a { name } }
`)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "Unknown argument")
		})
	}
}
