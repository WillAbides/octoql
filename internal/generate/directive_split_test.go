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

type Thing {
  name: String
  size: Int
}

input Filter {
  label: String
}
`

func generateSplit(t *testing.T, operation string) (string, error) {
	t.Helper()
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

// TestForRejectsConflictingDeclarations covers the defect the split exists to
// close: a named type generates one Go type, so two operations that disagree
// about one of its fields cannot both be satisfied.  Before this check the
// first operation converted won and the other silently got something else,
// which also made the output depend on the order of the operations.
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

// TestForAllowsAgreeingDeclarations guards the check above against
// over-reaching.  Declarations only have to agree with each other, so an
// operation that says nothing is not disagreeing and does not have to repeat
// the declaration.
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

// TestDefaultsSeparatesScopeFromNodeOptions checks that an option describing
// the fields inside an operation is written as a default, and that writing it
// as a node option is rejected rather than quietly treated as one.
func TestDefaultsSeparatesScopeFromNodeOptions(t *testing.T) {
	t.Run("defaults reach the fields", func(t *testing.T) {
		source, err := generateSplit(t, `
query Q @octoqlgenDefaults(pointer: false) { a { name size } }
`)

		require.NoError(t, err)
		assertDeclares(t, source, "Name string")
		assertDeclares(t, source, "Size int")
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

// TestDefaultsCannotCarryNamingOptions covers the pathology that motivated
// separating the two scopes: alias as a default asks for every field in the
// operation to have one name.  It is now not an argument of the defaults
// directive at all, so GraphQL rejects it and an editor shows it.
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
