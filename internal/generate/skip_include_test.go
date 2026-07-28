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
// non-null elements ([Role!]!) under @skip must keep nil-ability at the
// *container* level, not the elements. A slice is already nil-able in Go, so the
// correct result is a plain slice ([]Role) — never element pointers ([]*Role),
// and no redundant outer pointer (*[]Role) that would only mask slice depth.
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
	assert.Regexp(t, `Roles\s+\[\]QListUserRolesRole`, source)
	assert.NotContains(t, source, "Roles []*QListUserRolesRole")
	assert.NotContains(t, source, "Roles *[]QListUserRolesRole")
	require.NoError(t, buildGoFile("skip_include_list", []byte(source)))
}

// skipIncludeAbstractSchema exercises the special-unmarshalling path: a list of
// an interface (abstract) type and a list of a custom-unmarshalled scalar, both
// of which generate an UnmarshalJSON that must traverse slice depth correctly
// even when the field is wrapped in a container pointer by @skip/@include.
const skipIncludeAbstractSchema = `
type Query {
  nodes: [Node!]!
  dates: [Date!]!
  singleDate: Date!
}

interface Node {
  id: ID!
}

type User implements Node {
  id: ID!
  login: String!
}

scalar Date
`

// generateSkipIncludeAbstractSource is like generateSkipIncludeSource but wires
// up the Date scalar binding needed for the custom-unmarshaller list case.
func generateSkipIncludeAbstractSource(t *testing.T, operation string) (string, error) {
	t.Helper()

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	operationPath := filepath.Join(dir, "operation.graphql")
	require.NoError(t, os.WriteFile(schemaPath, []byte(skipIncludeAbstractSchema), 0o600))
	require.NoError(t, os.WriteFile(operationPath, []byte(operation), 0o600))

	config := &Config{
		Schema:      []string{schemaPath},
		Operations:  []string{operationPath},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "skipinclude",
		ContextType: "-",
		Bindings: map[string]*TypeBinding{
			"ID":   {Type: "github.com/willabides/octoql/internal/testutil.ID"},
			"Date": {Type: "time.Time", Marshaler: "github.com/willabides/octoql/internal/testutil.MarshalDate", Unmarshaler: "github.com/willabides/octoql/internal/testutil.UnmarshalDate"},
		},
	}
	generated, err := Generate(config)
	if err != nil {
		return "", err
	}
	return string(generated[config.Generated]), nil
}

// TestSkipForcesContainerPointerOnInterfaceList covers the special-unmarshal
// path for a non-null list of an abstract (interface) type under @skip. A slice
// is already nil-able, so it stays a plain slice and the generated UnmarshalJSON
// (which traverses slice depth) still compiles.
func TestSkipForcesContainerPointerOnInterfaceList(t *testing.T) {
	source, err := generateSkipIncludeAbstractSource(t, `
query QAbstractList($hide: Boolean!) {
  nodes @skip(if: $hide) {
    id
  }
}
`)
	require.NoError(t, err)
	assert.Regexp(t, `Nodes\s+\[\]QAbstractListNodesNode`, source)
	assert.NotContains(t, source, "Nodes *[]QAbstractListNodesNode")
	require.NoError(t, buildGoFile("skip_include_iface_list", []byte(source)))
}

// TestSkipForcesContainerPointerOnScalarList covers the special-unmarshal path
// for a non-null list of a custom-unmarshalled scalar under @skip. As above, the
// slice stays a plain slice and the generated UnmarshalJSON compiles.
func TestSkipForcesContainerPointerOnScalarList(t *testing.T) {
	source, err := generateSkipIncludeAbstractSource(t, `
query QScalarList($hide: Boolean!) {
  dates @skip(if: $hide)
}
`)
	require.NoError(t, err)
	assert.Regexp(t, `Dates\s+\[\]time\.Time`, source)
	assert.NotContains(t, source, "Dates *[]time.Time")
	require.NoError(t, buildGoFile("skip_include_scalar_list", []byte(source)))
}

// TestSkipForcesPointerOnScalarField covers a non-list custom-unmarshalled
// scalar under @skip: it must become a pointer (*time.Time), and the special
// UnmarshalJSON for that pointer field must compile. This is the scalar
// counterpart to the list cases and confirms the scalar-pointer path is
// unaffected by the slice handling.
func TestSkipForcesPointerOnScalarField(t *testing.T) {
	source, err := generateSkipIncludeAbstractSource(t, `
query QScalarField($hide: Boolean!) {
  singleDate @skip(if: $hide)
}
`)
	require.NoError(t, err)
	assert.Regexp(t, `SingleDate\s+\*time\.Time`, source)
	require.NoError(t, buildGoFile("skip_include_scalar_field", []byte(source)))
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
