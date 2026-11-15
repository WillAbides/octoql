package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skipIncludeSchema = `
type Query {
  user: User!
}

type User {
  login: String!
  isSuspended: Boolean!
}
`

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
		bindings  map[string]string
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
			bindings: map[string]string{
				"User": "github.com/willabides/octoql/internal/testutil.Account",
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

			_, err := generateTestSource(t, &testGenerateSpec{
				schema:      test.schema,
				operations:  test.operation,
				packageName: "skipinclude",
				bindings:    test.bindings,
			})
			require.Error(t, err)
			for _, want := range test.wantError {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
