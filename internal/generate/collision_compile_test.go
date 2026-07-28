package generate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateCollisionCompileCorpus pins the generator's behavior on inputs
// whose Go identifiers would otherwise collide.  Every collision case must fail
// at generation time with an error that names the offending identifier, rather
// than emitting Go source that fails to compile (pointing at code the user
// never wrote) or that silently drops a requested field.  The remaining cases
// must generate and compile cleanly, guarding against over-rejection.
func TestGenerateCollisionCompileCorpus(t *testing.T) {
	tests := []struct {
		name                string
		schema              string
		operations          string
		casing              Casing
		keepImplementations bool
		// wantGenerationErr, when set, is a substring the generation error must
		// contain.  Each collision case names its offending identifier so an
		// unrelated failure cannot satisfy it.  When empty, generation must
		// succeed and the output must compile against only the standard library.
		wantGenerationErr string
		// wantGenerationErrAlso lists additional substrings the error must
		// contain, for cases where the offending constructs are named in
		// non-contiguous parts of the message (e.g. separated by a volatile
		// source position).
		wantGenerationErrAlso []string
	}{
		{
			name: "baseline",
			schema: `
type Query {
  viewer: Viewer!
}
type Viewer {
  login: String!
}
`,
			operations: `
query Viewer {
  viewer {
    login
  }
}
`,
		},
		{
			// Two GraphQL fields that differ only by case both normalize to the
			// Go identifier Foo, which would declare the same struct field
			// twice.
			name: "case-distinct fields",
			schema: `
type Query {
  foo: String!
  Foo: String!
}
`,
			operations: `
query Fields {
  foo
  Foo
}
`,
			wantGenerationErr: "Go identifier Foo for both field Foo (GraphQL foo) and field Foo (GraphQL Foo)",
		},
		{
			// Fragment A is spread as a sibling of field a; the embedded
			// fragment type and the field both occupy the Go identifier A.
			name: "fragment and sibling field",
			schema: `
type Query {
  a: String!
  b: String!
}
`,
			operations: `
query Fields {
  a
  ...A
}
fragment A on Query {
  b
}
`,
			wantGenerationErr: "Go identifier A for both field A (GraphQL a) and embedded fragment A",
		},
		{
			// Field getFoo collides with the GetFoo getter octoqlgen generates
			// for field foo.
			name: "field and generated getter",
			schema: `
type Query {
  foo: String!
  getFoo: String!
}
`,
			operations: `
query Fields {
  foo
  getFoo
}
`,
			wantGenerationErr: "Go identifier GetFoo for both field GetFoo (GraphQL getFoo) and getter GetFoo (for field Foo)",
		},
		{
			// The named fragment's derived implementation type has the same Go
			// name as the directly-selected Impl type.  The direct selection
			// (id) and the fragment selection (login) differ, so routing the
			// fragment implementation through the type-collision guard rejects
			// it instead of silently overwriting the direct type and dropping
			// its id field.
			name: "fragment replaces derived type",
			schema: `
type Query {
  impl: Impl!
  i: I!
}
interface I {
  login: String!
}
type Impl implements I {
  id: String!
  login: String!
}
`,
			operations: `
query F {
  impl {
    id
  }
  i {
    ...F
  }
}
fragment F on I {
  login
}
`,
			keepImplementations: true,
			wantGenerationErr: "conflicting definition for the Go type FImpl: " +
				"it is generated from both the selection of GraphQL type Impl",
			wantGenerationErrAlso: []string{
				"the fragment F on GraphQL type Impl",
				"give one of them a distinct name with an @octoqlgen(typename:) directive",
			},
		},
		{
			// With auto-camel casing, foo_bar and fooBar both normalize to the
			// Go identifier FooBar.
			name: "underscore and camel fields",
			schema: `
type Query {
  foo_bar: String!
  fooBar: String!
}
`,
			operations: `
query Fields {
  foo_bar
  fooBar
}
`,
			casing:            Casing{Default: CasingAutoCamelCase},
			wantGenerationErr: "Go identifier FooBar for both field FooBar (GraphQL foo_bar) and field FooBar (GraphQL fooBar)",
		},
		{
			// A fragment named the same as a GraphQL type is fine as long as it
			// does not collide with a derived type; this must keep generating.
			name: "fragment matching GraphQL type",
			schema: `
type Query {
  foo: Foo!
}
type Foo {
  id: String!
}
`,
			operations: `
query Q {
  foo {
    ...Foo
  }
}
fragment Foo on Foo {
  id
}
`,
		},
		{
			// Fields named marshalJSON/unmarshalJSON collide with the
			// MarshalJSON/UnmarshalJSON methods octoqlgen emits (here forced by
			// the embedded fragment, which requires custom marshaling).
			name: "field and JSON methods",
			schema: `
type Query {
  marshalJSON: String!
  unmarshalJSON: String!
  value: String!
}
`,
			operations: `
query Methods {
  marshalJSON
  unmarshalJSON
  ...Fields
}
fragment Fields on Query {
  value
}
`,
			wantGenerationErr: "Go identifier MarshalJSON for both field MarshalJSON (GraphQL marshalJSON) and MarshalJSON method",
		},
		{
			// Two enum values normalize to the same Go name; caught by the
			// pre-existing enum-value guard, whose error shape this file models.
			name: "enum values with same Go name",
			schema: `
enum State {
  FIRST_VALUE
  first_value
}
type Query {
  state: State!
}
`,
			operations: `
query State {
  state
}
`,
			wantGenerationErr: "have conflicting Go name",
		},
		{
			// An input type and an operation both named Variables use distinct
			// derived Go names, so this must keep generating.
			name: "input fields and operation variables",
			schema: `
input Variables {
  value: String!
}
type Query {
  value(input: Variables!): String!
}
`,
			operations: `
query Variables($input: Variables!) {
  value(input: $input)
}
`,
		},
		{
			// A fragment shares a Go name with an input type.  The operation
			// uses the input as a variable, so the input type is registered
			// under the shared name before the fragment spread is resolved
			// (variables convert before the selection set).  Reading the type
			// map directly would find the input type and emit it in place of
			// the fragment -- compiling cleanly but silently dropping every
			// field the fragment selected.  Validating the stored entry's
			// GraphQL type and selection rejects the mismatch instead.  The
			// substring pins the input-first order that produced the silent
			// wrong type, not the fragment-first order that already errored at
			// the registration site.
			name: "fragment sharing name with input type",
			schema: `
input Profile {
  id: ID
}
type User {
  login: String
}
type Query {
  user(p: Profile): User
}
`,
			operations: `
fragment Profile on User {
  login
}
query Q($p: Profile) {
  user(p: $p) {
    ...Profile
  }
}
`,
			wantGenerationErr: "expected GraphQL type Profile, got User",
			wantGenerationErrAlso: []string{
				"conflicting definition for the Go type Profile",
				"input GraphQL type Profile",
				"the fragment Profile on GraphQL type User",
				"give one of them a distinct name with an @octoqlgen(typename:) directive",
			},
		},
		{
			// Two interface fields differ only in case and normalize to the
			// same Go name, so the generated interface would declare Get<Name>
			// twice.  With no concrete implementations there is no struct in
			// the type map, so this is only caught by the interface pass.
			name:                "interface fields with same getter",
			keepImplementations: true,
			schema: `
interface HasName {
  name: String!
  Name: String!
}
type Query {
  named: HasName
}
`,
			operations: `
query Q {
  named {
    name
    Name
  }
}
`,
			wantGenerationErr: "Go identifier GetName for both getter GetName " +
				"(for field Name, GraphQL name) and getter GetName " +
				"(for field Name, GraphQL Name)",
		},
		{
			// Two operation variables differ only in case and normalize to the
			// same Go field name.  Variables cannot be aliased, so the remedy
			// must not suggest a field alias.
			name: "operation variables with same Go name",
			schema: `
type Query {
  value(a: String, b: String): String
}
`,
			operations: `
query Q($foo: String, $Foo: String) {
  value(a: $foo, b: $Foo)
}
`,
			wantGenerationErr: "generated type QVariables would emit the Go identifier Foo " +
				"for both field Foo (GraphQL foo) and field Foo (GraphQL Foo); " +
				"rename the variable or input field, or change the casing configuration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := probeGenerated(t, test.schema, test.operations, test.casing, test.keepImplementations)
			if test.wantGenerationErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantGenerationErr)
				for _, also := range test.wantGenerationErrAlso {
					assert.Contains(t, err.Error(), also)
				}
				return
			}
			require.NoError(t, err)

			output, compileErr := probeCompile(t, source)
			require.NoError(t, compileErr, output)
		})
	}
}

// probeGenerated intentionally does not configure bindings: compile probes must
// only import the standard library, since user bindings are unavailable outside
// their source module.
func probeGenerated(
	t *testing.T,
	schema, operations string,
	casing Casing,
	keepImplementations bool,
) ([]byte, error) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.26.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generated.go"), []byte("package probe\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "operations.graphql"), []byte(operations), 0o600))

	var omitUnreferencedImplementations *bool
	if keepImplementations {
		omit := false
		omitUnreferencedImplementations = &omit
	}
	config := &Config{
		Schema:                          StringList{"schema.graphql"},
		Operations:                      StringList{"operations.graphql"},
		Generated:                       "generated.go",
		Package:                         "probe",
		ContextType:                     "-",
		Casing:                          casing,
		OmitUnreferencedImplementations: omitUnreferencedImplementations,
	}
	err := config.ValidateAndFillDefaults(dir)
	if err != nil {
		return nil, err
	}
	outputs, err := Generate(config)
	if err != nil {
		return nil, err
	}
	return outputs[config.Generated], nil
}

func probeCompile(t *testing.T, source []byte) (string, error) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.26.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generated.go"), source, 0o600))

	cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}
