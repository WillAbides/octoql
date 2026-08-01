package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestSharedInputTypeDoesNotDependOnOperationOrder covers the half of the
// order dependence a conflict check alone does not reach.
//
// An operation that declares nothing is not disagreeing, so it is accepted.  If
// the declaration only applied inside the operation that wrote it, the silent
// operation and the declaring one would generate different shapes for the one
// Go type they share, and whichever converted first would win.  The declaration
// applies wherever the field is generated, so both orders agree.
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

// TestDefaultsOnSharedInputTypeMustAgree covers the same hazard for defaults,
// which cannot be resolved the same way.
//
// @octoqlgenDefaults is scoped to one operation and two operations may
// legitimately want different things, but an input type is named by the schema
// and generated once, so they cannot both be satisfied.  Rejecting is the only
// answer that does not depend on which operation converted first.
//
// The comparison is the same one every other generated type gets, on the
// fields each operation would emit, so it covers list elements, bindings, and
// struct references without knowing anything about them.
func TestDefaultsOnSharedInputTypeMustAgree(t *testing.T) {
	pointerFalse := `query QA($f: Filter) @octoqlgenDefaults(pointer: false) { a(filter: $f) { name } }`
	pointerTrue := `query QB($f: Filter) @octoqlgenDefaults(pointer: true) { b(filter: $f) { name } }`
	silent := `query QC($f: Filter) { b(filter: $f) { name } }`

	for name, operation := range map[string]string{
		"conflicting":          pointerFalse + "\n" + pointerTrue,
		"conflicting, swapped": pointerTrue + "\n" + pointerFalse,
		"one operation silent": pointerFalse + "\n" + silent,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := generateSplit(t, operation)

			require.Error(t, err)
			assert.Contains(t, err.Error(),
				"conflicting definitions for the input type Filter")
			assert.Regexp(t, `field 0 \(Label\) Go type: \*?string vs \*?string`, err.Error())
			assert.Contains(t, err.Error(), "@octoqlgenFor")
		})
	}

	// An option that asks for what the field already is changes nothing, so it
	// is not a disagreement.  The check compares the shape each field would be
	// generated with rather than the options requesting it, which is the only
	// way to tell those apart.
	t.Run("options that change nothing are not disagreements", func(t *testing.T) {
		const nonNullSchema = `
type Query {
  a(filter: Required): String
  b(filter: Required): String
}

input Required {
  value: String!
}
`
		for name, test := range map[string]struct {
			schema    string
			operation string
		}{
			// value is already non-null, so pointer: false asks for nothing.
			"pointer false on a non-null field": {
				schema: nonNullSchema,
				operation: `
query QA($f: Required) @octoqlgenDefaults(pointer: false) { a(filter: $f) }
query QB($f: Required) { b(filter: $f) }
`,
			},
			// Nothing is bound, so opting out of a binding asks for nothing.
			"bind opt-out with no binding configured": {
				schema: splitSchema,
				operation: `
query QA($f: Filter) @octoqlgenFor(field: "Filter.label", bind: "-") { a(filter: $f) { name } }
query QB($f: Filter) { b(filter: $f) { name } }
`,
			},
		} {
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				schemaPath := filepath.Join(dir, "schema.graphql")
				operationPath := filepath.Join(dir, "operation.graphql")
				require.NoError(t, os.WriteFile(schemaPath, []byte(test.schema), 0o600))
				require.NoError(t, os.WriteFile(operationPath, []byte(test.operation), 0o600))

				_, err := Generate(&Config{
					Schema:      []string{schemaPath},
					Operations:  []string{operationPath},
					Generated:   filepath.Join(dir, "generated.go"),
					Package:     "client",
					ContextType: "-",
				})

				assert.NoError(t, err)
			})
		}
	})

	t.Run("agreeing defaults are fine", func(t *testing.T) {
		source, err := generateSplit(t,
			pointerFalse+"\n"+`query QB($f: Filter) @octoqlgenDefaults(pointer: false) { b(filter: $f) { name } }`)

		require.NoError(t, err)
		assertDeclares(t, source, "Label string")
	})
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

// TestInputFieldShapesAgreeIndependentOfOperationOrder pins the shared-input
// check against every type shape an input field can have.
//
// The check compares the fields each operation would generate, so it walks the
// type the way conversion does.  A model of that walk applied the pointer rule
// once, to the field's outer type, while conversion applies it at each level it
// descends through: a nullable element under a non-null list compared equal and
// generated differently, and whichever operation converted first silently
// decided the shared type.  Sharing a predicate is only structural if it is
// called at the same point in the recursion.
//
// Each shape is generated in both operation orders and has to reach the same
// answer, which is the property the model broke.
func TestInputFieldShapesAgreeIndependentOfOperationOrder(t *testing.T) {
	const shapedSchema = `
type Query {
  a(filter: Shaped): String
  b(filter: Shaped): String
}

enum Color {
  RED
}

input Shaped {
  value: %s
}
`
	// One operation asks for non-pointer fields and the other asks for
	// nothing.  Silence is not disagreement, so these agree exactly when
	// pointer: false would not change what is emitted.
	declaring := `query QA($f: Shaped) @octoqlgenDefaults(pointer: false) { a(filter: $f) }`
	silent := `query QB($f: Shaped) { b(filter: $f) }`

	for name, test := range map[string]struct {
		fieldType string
		// declares is the field the shared type is generated with, or empty
		// when the two operations would generate it differently.
		declares string
	}{
		"nullable scalar":           {fieldType: "String"},
		"non-null scalar":           {fieldType: "String!", declares: "Value string"},
		"nullable enum":             {fieldType: "Color"},
		"non-null enum":             {fieldType: "Color!", declares: "Value Color"},
		"list of nullable":          {fieldType: "[String]"},
		"non-null list of nullable": {fieldType: "[String]!"},
		"list of non-null":          {fieldType: "[String!]", declares: "Value []string"},
		"non-null list of non-null": {fieldType: "[String!]!", declares: "Value []string"},
		"nested list of nullable":   {fieldType: "[[String]]"},
		"nested list of non-null":   {fieldType: "[[String!]!]!", declares: "Value [][]string"},
	} {
		t.Run(name, func(t *testing.T) {
			schema := fmt.Sprintf(shapedSchema, test.fieldType)

			for order, operation := range map[string]string{
				"declaration first": declaring + "\n" + silent,
				"silence first":     silent + "\n" + declaring,
			} {
				t.Run(order, func(t *testing.T) {
					source, err := generateShaped(t, schema, operation, false)

					if test.declares == "" {
						require.Error(t, err)
						assert.Contains(t, err.Error(),
							"conflicting definitions for the input type Shaped")
						return
					}
					require.NoError(t, err)
					assertDeclares(t, source, test.declares)
				})
			}
		})
	}

	// use_struct_references reaches a branch of its own, which makes a non-null
	// input field a pointer and turns omitempty on.  pointer: false skips that
	// branch, so the two operations emit different fields for the type they
	// share.
	t.Run("struct references", func(t *testing.T) {
		const schema = `
type Query {
  a(filter: Parent): String
  b(filter: Parent): String
}

input Parent {
  child: Child!
}

input Child {
  name: String!
}
`
		for order, operation := range map[string]string{
			"declaration first": declaring + "\n" + silent,
			"silence first":     silent + "\n" + declaring,
		} {
			t.Run(order, func(t *testing.T) {
				_, err := generateShaped(t,
					schema,
					strings.ReplaceAll(operation, "Shaped", "Parent"),
					true)

				require.Error(t, err)
				assert.Contains(t, err.Error(), "conflicting definitions for the input type")
			})
		}
	})
}

func generateShaped(t *testing.T, schema, operation string, structReferences bool) (string, error) {
	t.Helper()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	generatedPath := filepath.Join(dir, "generated.go")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(operation), 0o600))

	generated, err := Generate(&Config{
		Schema:           []string{schemaPath},
		Operations:       []string{operationPath},
		Generated:        generatedPath,
		Package:          "client",
		ContextType:      "-",
		StructReferences: structReferences,
	})
	if err != nil {
		return "", err
	}
	return string(generated[generatedPath]), nil
}
