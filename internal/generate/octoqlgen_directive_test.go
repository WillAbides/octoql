package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}

type User {
  isSuspended: Boolean
  name: String
}
`
	for _, test := range []struct {
		name      string
		operation string
	}{
		{
			name: "sibling fields",
			operation: `
query Siblings {
  # @octoqlgen(pointer: false)
  first second
}
`,
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

			_, err = Generate(&Config{
				Schema:      []string{schemaPath},
				Operations:  []string{operationPath},
				Generated:   filepath.Join(dir, "generated.go"),
				Package:     "client",
				ContextType: "-",
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "cannot apply to multiple nodes on one line")
			assert.Contains(t, err.Error(), "put each node on its own line")
		})
	}
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
