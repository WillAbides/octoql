package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/willabides/octoql/internal/directive"
)

// assertDeclares checks that source declares the given struct field, ignoring
// the column alignment gofmt applies.
func assertDeclares(t *testing.T, source, declaration string) {
	t.Helper()
	assert.Contains(t,
		strings.Join(strings.Fields(source), " "),
		strings.Join(strings.Fields(declaration), " "))
}

// testDirective parses a single @octoqlgen directive the way the query parser
// does, so the unit tests below exercise the same ast.Directive values
// collectDirectives works with.
func testDirective(t *testing.T, args string) *ast.Directive {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{
		Input: "query Q @octoqlgen" + args + " { field }",
	})
	require.NoError(t, err)
	return doc.Operations[0].Directives[0]
}

func TestOctoqlgenDirectiveAddMergesRepeatedForDirectives(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	err := directive.add(
		testDirective(t, `(for: "IssueFilter.assignee", pointer: true)`), nil)
	require.NoError(t, err)

	err = directive.add(
		testDirective(t, `(for: "IssueFilter.assignee", omitempty: true)`), nil)
	require.NoError(t, err)

	fieldDirective := directive.FieldDirectives["IssueFilter"]["assignee"]
	require.NotNil(t, fieldDirective)
	assert.True(t, fieldDirective.GetPointer())
	assert.True(t, fieldDirective.GetOmitempty())
}

func TestOctoqlgenDirectiveAddRejectsConflictingRepeatedForDirectives(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	err := directive.add(
		testDirective(t, `(for: "IssueFilter.assignee", pointer: true)`), nil)
	require.NoError(t, err)

	err = directive.add(
		testDirective(t, `(for: "IssueFilter.assignee", pointer: false)`), nil)

	assert.EqualError(t, err, "conflicting values for pointer")
}

func TestOctoqlgenDirectiveAddRejectsEmptyFor(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	err := directive.add(testDirective(t, `(for: "", pointer: false)`), nil)

	assert.EqualError(t, err, `for must not be empty`)
}

func TestOctoqlgenDirectiveAddRejectsEmptyStringOptions(t *testing.T) {
	for _, option := range []string{"alias", "bind", "typename"} {
		t.Run(option, func(t *testing.T) {
			directive := newOctoqlgenDirective(nil)

			err := directive.add(testDirective(t, `(`+option+`: "")`), nil)

			assert.EqualError(t, err, option+" must not be empty")
		})
	}
}

func TestOctoqlgenDirectiveAddAllowsBindOptOut(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	err := directive.add(testDirective(t, `(bind: "-")`), nil)

	require.NoError(t, err)
	assert.Equal(t, "-", directive.Bind)
}

// TestDirectiveAppliesOnlyToItsOwnNode checks that an option never reaches a
// node it was not written on, including for nodes that share a source line.
//
// The motivating failure was `pointer: false` written for one field silently
// removing the pointer from a neighbouring field: a null in the response then
// decoded as the Go zero value, so a boolean like isSuspended read as false.
// Attaching options to AST nodes rather than to source lines makes that
// impossible to express.
func TestDirectiveAppliesOnlyToItsOwnNode(t *testing.T) {
	var unsafeResponse struct {
		IsSuspended bool `json:"isSuspended"`
	}
	err := json.Unmarshal([]byte(`{"isSuspended":null}`), &unsafeResponse)
	require.NoError(t, err)
	assert.False(t, unsafeResponse.IsSuspended)

	const schema = `
type Query {
  viewer: User!
  first(note: String): String
  second: String
  outer(arg: Input): Inner
  sibling: String
}

type User {
  isSuspended: Boolean
  name: String
}

type Inner {
  child: String
}

input Input {
  text: String
}
`
	for _, test := range []struct {
		name      string
		operation string
		want      []string
	}{
		{
			name: "sibling fields on one line",
			operation: `
query Siblings {
  first @octoqlgen(pointer: false) second
}
`,
			want: []string{"First string", "Second *string"},
		},
		{
			name: "non-ASCII argument before sibling",
			operation: `
query NonASCII {
  outer(arg: {text: "é"}) @octoqlgen(pointer: false) { child } sibling
}
`,
			want: []string{"Sibling *string"},
		},
		{
			name: "multi-byte argument before sibling",
			operation: `
query MultiByteInput {
  outer(arg: {text: "😀😀😀😀😀😀😀😀"}) @octoqlgen(pointer: false) { child } sibling
}
`,
			want: []string{"Sibling *string"},
		},
		{
			name: "escaped block string quote before sibling",
			operation: `
query EscapedBlockString {
  first(note: """text \""" { still text""") @octoqlgen(pointer: false) second
}
`,
			want: []string{"First string", "Second *string"},
		},
		{
			name: "directive on a field does not reach its selections",
			operation: `
query Viewer {
  viewer @octoqlgen(pointer: false) { isSuspended name }
}
`,
			want: []string{"IsSuspended *bool", "Name *string"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			schemaPath := filepath.Join(dir, "schema.graphql")
			operationPath := filepath.Join(dir, "operation.graphql")
			err = os.WriteFile(schemaPath, []byte(schema), 0o600)
			require.NoError(t, err)
			err = os.WriteFile(operationPath, []byte(test.operation), 0o600)
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
			for _, want := range test.want {
				assertDeclares(t, source, want)
			}
		})
	}
}

// TestDirectiveNeverReachesTheServer checks that @octoqlgen is removed from
// the operation octoqlgen sends.  The directive is octoqlgen's own and is not
// declared by the server's schema, so leaving it in would make every request
// fail.
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

	// The options were applied...
	assert.Contains(t, source, "type ViewerResp struct")
	assert.Contains(t, source, "type ViewerUser struct")
	// ...but do not appear anywhere in the generated file, and in particular
	// not in the operation body.  Unrelated directives are left alone.
	assert.NotContains(t, source, "@octoqlgen")
	assert.Contains(t, source, `@skip(if: $skipName)`)
}

func TestOctoqlgenDirectiveAllowsOperationWithMultipleArguments(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	err := os.WriteFile(schemaPath, []byte(`
type Query {
  repository(owner: String!, name: String!): Repository!
}

type Repository {
  name: String
}
`), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(operationPath, []byte(`
query Repository($owner: String!, $name: String!) @octoqlgen(typename: "RepoResp") {
  repository(owner: $owner, name: $name) {
    name
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
	assert.Contains(t, string(generated[filepath.Join(dir, "generated.go")]),
		"type RepoResp struct")
}

func TestOctoqlgenDirectiveAllowsOperationOmitemptyWithVariableDefault(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	err := os.WriteFile(schemaPath, []byte(`
type Query {
  value(input: String!): String!
}
`), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(operationPath, []byte(`
query Value($input: String! = "default") @octoqlgen(pointer: true, omitempty: true) {
  value(input: $input)
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
	assertDeclares(t, string(generated[filepath.Join(dir, "generated.go")]), `Input *string `+"`json:\"input,omitempty\"`")
}

func TestOctoqlgenDirectiveAllowsLocalOmitemptyWithVariableDefault(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	err := os.WriteFile(schemaPath, []byte(`
type Query {
  value(input: String!): String!
}
`), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(operationPath, []byte(`
query Value(
  $input: String! = "default" @octoqlgen(pointer: true, omitempty: true)
) {
  value(input: $input)
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
	assertDeclares(t, string(generated[filepath.Join(dir, "generated.go")]), `Input *string `+"`json:\"input,omitempty\"`")
}

func TestOctoqlgenDirectiveAllowsLocalOmitemptyDisableForRequiredVariable(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	err := os.WriteFile(schemaPath, []byte(`
type Query {
  value(input: String!): String!
}
`), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(operationPath, []byte(`
query Value(
  $input: String! @octoqlgen(omitempty: false)
) @octoqlgen(omitempty: true) {
  value(input: $input)
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
	assertDeclares(t, string(generated[filepath.Join(dir, "generated.go")]), `Input string `+"`json:\"input\"`")
}

func TestOctoqlgenDirectiveAllowsOperationFlatten(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	err := os.WriteFile(schemaPath, []byte(`
type Query {
  viewer: User!
}

type User {
  name: String
}
`), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(operationPath, []byte(`
	fragment ViewerFields on Query {
	  viewer {
	    name
	  }
	}

	query Viewer @octoqlgen(flatten: true) {
	  ...ViewerFields
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
	assert.Contains(t, source, "type ViewerFields struct")
	assert.NotContains(t, source, "type ViewerResponse struct")
}

// TestTypeReuseComparesDirectiveOptions checks that two selections sharing a
// user-specified type name are only allowed to share a generated type when
// they also generate the same Go.
//
// The options used to live in comments, which gqlparser discards, so the
// selection comparison that guards type reuse could not see them: two
// selections that requested the same fields with different options compared
// equal, and the first type generated was silently reused for both.
func TestTypeReuseComparesDirectiveOptions(t *testing.T) {
	const schema = `
type Query {
  viewer: User!
}

type User {
  login: String!
  isSuspended: Boolean
}
`
	operation := func(firstOptions, secondOptions string) string {
		return `
query A {
  viewer @octoqlgen(typename: "SharedUser") {
    login
    isSuspended ` + firstOptions + `
  }
}

query B {
  viewer @octoqlgen(typename: "SharedUser") {
    login
    isSuspended ` + secondOptions + `
  }
}
`
	}

	generate := func(t *testing.T, operationText string) (string, error) {
		t.Helper()
		dir := t.TempDir()
		schemaPath := filepath.Join(dir, "schema.graphql")
		operationPath := filepath.Join(dir, "operation.graphql")
		require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))
		require.NoError(t, os.WriteFile(operationPath, []byte(operationText), 0o600))

		generated, err := Generate(&Config{
			Schema:      []string{schemaPath},
			Operations:  []string{operationPath},
			Generated:   filepath.Join(dir, "generated.go"),
			Package:     "client",
			ContextType: "-",
		})
		if err != nil {
			return "", err
		}
		return string(generated[filepath.Join(dir, "generated.go")]), nil
	}

	t.Run("differing options are rejected", func(t *testing.T) {
		_, err := generate(t, operation("", "@octoqlgen(pointer: false)"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflicting definition for the Go type SharedUser")
		assert.Contains(t, err.Error(),
			"to have the same @octoqlgen options in both places")
	})

	t.Run("matching options share one type", func(t *testing.T) {
		source, err := generate(t,
			operation("@octoqlgen(pointer: false)", "@octoqlgen(pointer: false)"))

		require.NoError(t, err)
		assertDeclares(t, source, "IsSuspended bool")
		assert.Equal(t, 1, strings.Count(source, "type SharedUser struct"))
	})

	t.Run("no options at all still shares one type", func(t *testing.T) {
		source, err := generate(t, operation("", ""))

		require.NoError(t, err)
		assertDeclares(t, source, "IsSuspended *bool")
		assert.Equal(t, 1, strings.Count(source, "type SharedUser struct"))
	})
}

// TestGeneratorOwnsTheDirectiveDefinition checks that a declaration already in
// the schema does not change how octoqlgen interprets its own directive.
//
// Schema files octoqlgen writes carry a copy of the declaration so editors can
// resolve @octoqlgen. That copy may have been written by a different version
// of octoqlgen, or edited by hand, so the generator must use its own.
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
		"current declaration": "\n" + directive.SDL,
		// Missing the argument and location this operation needs, so
		// generation only succeeds if the generator ignores it.
		"stale declaration": "\ndirective @octoqlgen(alias: String) on FIELD\n",
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
