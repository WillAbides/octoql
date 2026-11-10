package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateSkipIncludeSource(
	t *testing.T,
	schema string,
	operation string,
	bindings map[string]*TypeBinding,
) (string, error) {
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
		Bindings:    bindings,
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

const skipIncludeBoundLeafSchema = `
type Query {
  thing: Thing!
}

type Thing {
  tags: [String!]!
  name: String!
  attrs: String!
  raw: String!
  anyValue: String!
  fixed: [String!]!
}
`

// TestSkipIncludeTypes verifies how @skip and @include affect generated Go field
// types across scalars, nested objects, lists, interfaces, custom marshalers,
// and bound leaf types.
func TestSkipIncludeTypes(t *testing.T) {
	t.Parallel()

	filesToBuild := make(map[string][]byte)

	t.Run("fields nested objects and lists", func(t *testing.T) {
		source, err := generateSkipIncludeSource(t, skipIncludeSchema, `
query Q($hide: Boolean!, $show: Boolean!) {
  user {
    login
    isSuspended @skip(if: $hide)
    included: isSuspended @include(if: $show)
    constSkipped: isSuspended @skip(if: true)
    plain: isSuspended
    primaryRole @skip(if: $hide) {
      name
    }
    roles @skip(if: $hide) {
      name
    }
  }
}
`, nil)
		require.NoError(t, err)
		assertDeclares(t, source, "Login string")
		assertDeclares(t, source, "IsSuspended *bool")
		assertDeclares(t, source, "Included *bool")
		assertDeclares(t, source, "ConstSkipped *bool")
		assertDeclares(t, source, "Plain bool")
		assertDeclares(t, source, "PrimaryRole *QUserPrimaryRole")
		assertDeclares(t, source, "Roles []QUserRolesRole")
		assert.NotContains(t, source, "[]*QUserRolesRole")
		assert.NotContains(t, source, "*[]QUserRolesRole")
		filesToBuild["fields_and_lists"] = []byte(source)
	})

	t.Run("abstracts and custom marshaled scalars", func(t *testing.T) {
		bindings := map[string]*TypeBinding{
			"ID": {
				Type: "github.com/willabides/octoql/internal/testutil.ID",
			},
			"Date": {
				Type:        "time.Time",
				Marshaler:   "github.com/willabides/octoql/internal/testutil.MarshalDate",
				Unmarshaler: "github.com/willabides/octoql/internal/testutil.UnmarshalDate",
			},
		}
		source, err := generateSkipIncludeSource(t, skipIncludeAbstractSchema, `
query QAbstract($hide: Boolean!) {
  nodes @skip(if: $hide) {
    id
  }
  dates @skip(if: $hide)
  singleDate @skip(if: $hide)
}

query QAbstractTypename($hide: Boolean!) {
  nodes {
    __typename @skip(if: $hide)
    id
  }
}
`, bindings)
		require.NoError(t, err)
		assert.Regexp(t, `Nodes\s+\[\]QAbstractNodesNode`, source)
		assert.NotContains(t, source, "Nodes *[]QAbstractNodesNode")
		assert.Regexp(t, `Dates\s+\[\]time\.Time`, source)
		assert.NotContains(t, source, "Dates *[]time.Time")
		assert.Regexp(t, `SingleDate\s+\*time\.Time`, source)
		filesToBuild["abstracts_and_scalars"] = []byte(source)
	})

	t.Run("bound leaves arrays and any normalization", func(t *testing.T) {
		source, err := generateSkipIncludeSource(t, skipIncludeBoundLeafSchema, `
query QBound($hide: Boolean!) {
  thing {
    tags @skip(if: $hide) @octoqlgen(bind: "[]string")
    name @skip(if: $hide) @octoqlgen(bind: "*string")
    attrs @skip(if: $hide) @octoqlgen(bind: "map[string]string")
    raw @skip(if: $hide) @octoqlgen(bind: "interface{}")
    anyValue @skip(if: $hide) @octoqlgen(bind: "any")
    fixed @skip(if: $hide) @octoqlgen(bind: "[4]string")
  }
}
`, nil)
		require.NoError(t, err)
		assert.Regexp(t, `Tags\s+\[\]string`, source)
		assert.NotContains(t, source, "*[]string")
		assert.Regexp(t, `Name\s+\*string`, source)
		assert.NotContains(t, source, "**string")
		assert.Regexp(t, `Attrs\s+map\[string\]string`, source)
		assert.NotContains(t, source, "*map[string]string")
		assert.Regexp(t, `Raw\s+interface\{\}`, source)
		assert.Regexp(t, `AnyValue\s+interface\{\}`, source)
		assert.NotRegexp(t, `AnyValue\s+any\b`, source)
		assert.NotContains(t, source, "*interface{}")
		assert.Regexp(t, `Fixed\s+\*\[4\]string`, source)
		filesToBuild["bound_leaves"] = []byte(source)
	})

	require.NoError(t, buildGoFiles(filesToBuild))
}

func TestSkipIncludeRejections(t *testing.T) {
	t.Parallel()

	const boundSchema = `
type Query {
  user: User!
}

type User {
  isSuspended: Boolean!
}
`

	tests := []struct {
		bindings  map[string]*TypeBinding
		name      string
		schema    string
		operation string
		wantError []string
	}{
		{
			name:   "pointer false conflict",
			schema: skipIncludeSchema,
			operation: `
query QConflict($hide: Boolean!) {
  user {
    isSuspended @skip(if: $hide) @octoqlgen(pointer: false)
  }
}
`,
			wantError: []string{"isSuspended", "pointer: false"},
		},
		{
			name:   "fragment spread",
			schema: skipIncludeSchema,
			operation: `
fragment UserFields on User {
  login
}

query QFragSpread($hide: Boolean!) {
  user {
    ...UserFields @skip(if: $hide)
  }
}
`,
			wantError: []string{"@skip"},
		},
		{
			name:   "inline fragment",
			schema: skipIncludeSchema,
			operation: `
query QInline($hide: Boolean!) {
  user {
    ... on User @skip(if: $hide) {
      login
    }
  }
}
`,
			wantError: []string{"@skip"},
		},
		{
			name:   "globally bound composite",
			schema: boundSchema,
			bindings: map[string]*TypeBinding{
				"User": {Type: "github.com/willabides/octoql/internal/testutil.Account"},
			},
			operation: `
query Q($hide: Boolean!) {
  user {
    isSuspended @skip(if: $hide)
  }
}
`,
			wantError: []string{"@skip"},
		},
		{
			name:   "locally bound composite",
			schema: boundSchema,
			operation: `
query Q($hide: Boolean!) {
  user @octoqlgen(bind: "github.com/willabides/octoql/internal/testutil.Account") {
    isSuspended @skip(if: $hide)
  }
}
`,
			wantError: []string{"@skip"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := generateSkipIncludeSource(t, test.schema, test.operation, test.bindings)
			require.Error(t, err)
			for _, want := range test.wantError {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
