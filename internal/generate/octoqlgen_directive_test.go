package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestOctoqlgenDirectiveAddMergesRepeatedForDirectives(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	pointerDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", pointer: true)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(pointerDirective, nil)
	require.NoError(t, err)

	omitemptyDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", omitempty: true)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(omitemptyDirective, nil)
	require.NoError(t, err)

	fieldDirective := directive.FieldDirectives["IssueFilter"]["assignee"]
	require.NotNil(t, fieldDirective)
	assert.True(t, fieldDirective.GetPointer())
	assert.True(t, fieldDirective.GetOmitempty())
}

func TestOctoqlgenDirectiveAddRejectsConflictingRepeatedForDirectives(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	pointerDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", pointer: true)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(pointerDirective, nil)
	require.NoError(t, err)

	conflictingDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", pointer: false)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(conflictingDirective, nil)

	assert.EqualError(t, err, "conflicting values for pointer")
}

func TestOctoqlgenDirectiveAddRejectsEmptyFor(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	graphQLDirective, err := parseDirective(
		`@octoqlgen(for: "", pointer: false)`,
		nil,
	)
	require.NoError(t, err)

	err = directive.add(graphQLDirective, nil)

	assert.EqualError(t, err, `for must not be empty`)
}

func TestOctoqlgenDirectiveAddRejectsEmptyStringOptions(t *testing.T) {
	for _, option := range []string{"alias", "bind", "typename"} {
		t.Run(option, func(t *testing.T) {
			directive := newOctoqlgenDirective(nil)
			graphQLDirective, err := parseDirective(
				`@octoqlgen(`+option+`: "")`,
				nil,
			)
			require.NoError(t, err)

			err = directive.add(graphQLDirective, nil)

			assert.EqualError(t, err, option+" must not be empty")
		})
	}
}

func TestOctoqlgenDirectiveAddAllowsBindOptOut(t *testing.T) {
	directive := newOctoqlgenDirective(nil)
	graphQLDirective, err := parseDirective(`@octoqlgen(bind: "-")`, nil)
	require.NoError(t, err)

	err = directive.add(graphQLDirective, nil)

	require.NoError(t, err)
	assert.Equal(t, "-", directive.Bind)
}

func TestOctoqlgenDirectiveAttachmentRejectsMultipleNodes(t *testing.T) {
	var unsafeResponse struct {
		IsSuspended bool `json:"isSuspended"`
	}
	err := json.Unmarshal([]byte(`{"isSuspended":null}`), &unsafeResponse)
	require.NoError(t, err)
	assert.False(t, unsafeResponse.IsSuspended)

	const schema = `
type Query {
  viewer: User!
  first: String
  second: String
  a: String
  b: String
  outer(arg: String): Inner
  sibling: String
}

type User {
  isSuspended: Boolean
  name: String
}

type Inner {
  child: String
}
`
	for _, test := range []struct {
		name      string
		operation string
		wantError bool
	}{
		{
			name: "sibling fields",
			operation: `
query Siblings {
  # @octoqlgen(pointer: false)
  first second
}
`,
			wantError: true,
		},
		{
			name: "field and fragment spread",
			operation: `
query FieldAndFragmentSpread {
  # @octoqlgen(pointer: false)
  a ...Fields
}

fragment Fields on Query {
  b
}
`,
			wantError: true,
		},
		{
			name: "non-ASCII before sibling",
			operation: `
query NonASCII {
  # @octoqlgen(pointer: false)
  outer(arg:"é"){child}sibling
}
`,
			wantError: true,
		},
		{
			name: "nested selection",
			operation: `
query Viewer {
  # @octoqlgen(pointer: false)
  viewer { isSuspended name }
}
`,
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

			if test.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "cannot apply to multiple peer nodes on one line")
				assert.Contains(t, err.Error(), "put each peer node on its own line")
				return
			}

			require.NoError(t, err)
			source := string(generated[filepath.Join(dir, "generated.go")])
			assert.Contains(t, source, "IsSuspended *bool")
		})
	}
}

func TestBraceDepthAtPositionUsesRuneColumns(t *testing.T) {
	line := `outer(arg:"é"){child}sibling`
	position := &ast.Position{
		Src:    &ast.Source{Input: line},
		Line:   1,
		Column: utf8.RuneCountInString(`outer(arg:"é"){child}`) + 1,
	}

	assert.Zero(t, braceDepthAtPosition(position))
}

func TestOctoqlgenDirectiveAttachmentAllowsOperationWithMultipleArguments(t *testing.T) {
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
# @octoqlgen
query Repository($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    name
  }
}
`), 0o600)
	require.NoError(t, err)

	_, err = Generate(&Config{
		Schema:      []string{schemaPath},
		Operations:  []string{operationPath},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "client",
		ContextType: "-",
	})

	require.NoError(t, err)
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
# @octoqlgen(pointer: true, omitempty: true)
query Value($input: String! = "default") {
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
	assert.Contains(t, string(generated[filepath.Join(dir, "generated.go")]), `Input *string `+"`json:\"input,omitempty\"`")
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
  # @octoqlgen(pointer: true, omitempty: true)
  $input: String! = "default"
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
	assert.Contains(t, string(generated[filepath.Join(dir, "generated.go")]), `Input *string `+"`json:\"input,omitempty\"`")
}

func TestOctoqlgenDirectiveAttachmentAllowsOperationFlatten(t *testing.T) {
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

	# @octoqlgen(flatten: true)
	query Viewer {
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
