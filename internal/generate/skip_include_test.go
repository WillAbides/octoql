package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateSkipIncludeSource is a small helper for the @skip/@include tests: it
// writes an inline schema and operation to disk, runs generation, and returns
// the generated Go source (or an error).
func generateSkipIncludeSource(t *testing.T, schema, operation string) (string, error) {
	t.Helper()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(operation), 0o600))

	config := &Config{
		Schema:      []string{schemaPath},
		Operations:  []string{operationPath},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "skipinclude",
		ContextType: "-",
	}
	generated, err := Generate(config)
	if err != nil {
		return "", err
	}
	return string(generated[config.Generated]), nil
}

const skipIncludeSchema = `
type Query {
  user: User!
}

type User {
  login: String!
  isSuspended: Boolean!
  roles: [Role!]!
  primaryRole: Role!
  manager: User
}

type Role {
  name: String!
}
`

// TestSkipIncludeForcesPointerNonNullField reproduces the authorization-bypass
// table: a schema-non-null field carrying @skip must generate as a pointer so
// that an absent value is representable as nil rather than a silent false.
func TestSkipIncludeForcesPointerNonNullField(t *testing.T) {
	source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query Q($hide: Boolean!) {
  user {
    login
    isSuspended @skip(if: $hide)
  }
}
`)
	require.NoError(t, err)
	assert.Contains(t, source, "IsSuspended *bool")
	// login carries no directive and must stay a plain value type.
	assert.Regexp(t, `Login\s+string`, source)
	require.NoError(t, buildGoFile("skip_include_pointer", []byte(source)))
}

// TestIncludeForcesPointer verifies @include behaves the same as @skip.
func TestIncludeForcesPointer(t *testing.T) {
	source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QInclude($show: Boolean!) {
  user {
    isSuspended @include(if: $show)
  }
}
`)
	require.NoError(t, err)
	assert.Contains(t, source, "IsSuspended *bool")
}

// TestSkipConstantArgumentForcesPointer verifies a constant argument
// (@skip(if: true)) forces the pointer just like a variable argument.
func TestSkipConstantArgumentForcesPointer(t *testing.T) {
	source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QConst {
  user {
    isSuspended @skip(if: true)
  }
}
`)
	require.NoError(t, err)
	assert.Contains(t, source, "IsSuspended *bool")
}

// TestSkipForcesPointerNestedObject verifies a non-null nested object under
// @skip becomes a pointer to the generated struct.
func TestSkipForcesPointerNestedObject(t *testing.T) {
	source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QNested($hide: Boolean!) {
  user {
    primaryRole @skip(if: $hide) {
      name
    }
  }
}
`)
	require.NoError(t, err)
	assert.Contains(t, source, "PrimaryRole *QNestedUserPrimaryRole")
	require.NoError(t, buildGoFile("skip_include_nested", []byte(source)))
}

// TestSkipForcesContainerPointerOnList covers trap 2: a non-null list of
// non-null elements ([Role!]!) under @skip must make the *container* nilable
// (*[]Role), not the elements ([]*Role).
func TestSkipForcesContainerPointerOnList(t *testing.T) {
	source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QList($hide: Boolean!) {
  user {
    roles @skip(if: $hide) {
      name
    }
  }
}
`)
	require.NoError(t, err)
	assert.Contains(t, source, "Roles *[]QListUserRolesRole")
	assert.NotContains(t, source, "Roles []*QListUserRolesRole")
	assert.NotContains(t, source, "Roles []QListUserRolesRole")
	require.NoError(t, buildGoFile("skip_include_list", []byte(source)))
}

// TestSkipPointerFalseConflict covers trap 1: an explicit @octoqlgen(pointer:
// false) on a conditionally-skipped field is unrepresentable and must be a hard
// generation error naming the field.
func TestSkipPointerFalseConflict(t *testing.T) {
	_, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QConflict($hide: Boolean!) {
  user {
    # @octoqlgen(pointer: false)
    isSuspended @skip(if: $hide)
  }
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "isSuspended")
	assert.Contains(t, err.Error(), "pointer: false")
}

// TestBaselineNoDirectiveKeepsValueType proves we do not over-apply: a non-null
// field with no @skip/@include stays a value type.
func TestBaselineNoDirectiveKeepsValueType(t *testing.T) {
	source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QBaseline {
  user {
    login
    isSuspended
  }
}
`)
	require.NoError(t, err)
	assert.Contains(t, source, "IsSuspended bool")
	assert.NotContains(t, source, "IsSuspended *bool")
}

// TestSkipOnFragmentSpreadRejected covers trap 3: @skip/@include on a fragment
// spread is rejected rather than silently generating value types for fields
// that can vanish.
func TestSkipOnFragmentSpreadRejected(t *testing.T) {
	_, err := generateSkipIncludeSource(t, skipIncludeSchema, `
fragment UserFields on User {
  login
}

query QFragSpread($hide: Boolean!) {
  user {
    ...UserFields @skip(if: $hide)
  }
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "@skip")
}

// TestSkipOnInlineFragmentRejected covers trap 3 for inline fragments.
func TestSkipOnInlineFragmentRejected(t *testing.T) {
	_, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query QInline($hide: Boolean!) {
  user {
    ... on User @skip(if: $hide) {
      login
    }
  }
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "@skip")
}
