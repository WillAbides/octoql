package generate

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestErrorf(t *testing.T) {
	t.Parallel()

	gqlErr := &gqlerror.Error{
		Message: "unknown field",
		Locations: []gqlerror.Location{
			{
				Line:   7,
				Column: 3,
			},
		},
		Extensions: map[string]any{
			"file": "schema.graphql",
		},
	}
	generatorErr := &octoqlgenError{
		pos: &errorPos{
			filename: "operations.graphql",
			line:     4,
		},
		msg:     "invalid selection",
		wrapped: gqlErr,
	}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "graphql error",
			err:  gqlErr,
			want: "schema.graphql:7: invalid Widget: unknown field",
		},
		{
			name: "generator error",
			err:  generatorErr,
			want: "operations.graphql:4: invalid Widget: invalid selection",
		},
		{
			name: "standard error",
			err:  errors.New("invalid value"),
			want: "invalid Widget: invalid value",
		},
		{
			name: "graphql error list",
			err:  gqlerror.List{gqlErr},
			want: "schema.graphql:7: invalid Widget: unknown field",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := errorf(
				nil,
				"invalid %s: %w",
				"Widget",
				tt.err,
			)
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
			assert.NotContains(t, err.Error(), "%!w")
			assert.Equal(t, tt.err, errors.Unwrap(err))

			var generator *octoqlgenError
			require.ErrorAs(t, err, &generator)
			graphql, isGraphQL := errors.AsType[*gqlerror.Error](tt.err)
			if isGraphQL {
				require.ErrorIs(t, err, graphql)
				var unwrappedGraphQL *gqlerror.Error
				require.ErrorAs(t, err, &unwrappedGraphQL)
				assert.Same(t, graphql, unwrappedGraphQL)
				return
			}
			assert.ErrorIs(t, err, tt.err)
		})
	}
}

func TestErrorfPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pos  *ast.Position
		want string
	}{
		{
			name: "explicit position",
			pos: &ast.Position{
				Src: &ast.Source{
					Name: "query.graphql",
				},
				Line: 9,
			},
			want: "query.graphql:9: failed",
		},
		{
			name: "position without source",
			pos: &ast.Position{
				Line: 9,
			},
			want: "failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := errorf(tt.pos, "failed")
			require.EqualError(t, err, tt.want)
		})
	}
}

func TestGoSourceError(t *testing.T) {
	t.Parallel()

	first := &scanner.Error{
		Pos: token.Position{
			Filename: "generated.go",
			Line:     2,
			Column:   1,
		},
		Msg: "expected declaration",
	}
	last := &scanner.Error{
		Pos: token.Position{
			Filename: "generated.go",
			Line:     4,
			Column:   1,
		},
		Msg: "expected operand",
	}
	tests := []struct {
		name   string
		err    error
		errors []*scanner.Error
		source string
	}{
		{
			name:   "error list",
			err:    fmt.Errorf("formatting: %w", scanner.ErrorList{first, last}),
			errors: []*scanner.Error{first, last},
			source: "  1 | package generated\n> 2 | bad declaration\n  3 | // valid line\n> 4 | bad operand",
		},
		{
			name:   "single error",
			err:    last,
			errors: []*scanner.Error{last},
			source: "  1 | package generated\n  2 | bad declaration\n  3 | // valid line\n> 4 | bad operand",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := []byte("package generated\nbad declaration\n// valid line\nbad operand")
			err := goSourceError("format", source, tt.err)
			require.Error(t, err)
			text := err.Error()
			for _, scanErr := range tt.errors {
				assert.Equal(t, 1, strings.Count(text, scanErr.Error()))
			}
			_, rendered, found := strings.Cut(text, "---source code---\n")
			require.True(t, found)
			assert.Equal(t, tt.source, rendered)
		})
	}
}
