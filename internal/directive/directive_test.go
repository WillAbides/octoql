package directive

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// loadCompanion loads the companion file the way an editor would, alongside a
// minimal schema.
func loadCompanion(t *testing.T) *ast.Schema {
	t.Helper()
	schema, err := gqlparser.LoadSchema(
		&ast.Source{Name: FileName, Input: FileContents},
		&ast.Source{Name: "schema.graphql", Input: "type Query {\n  placeholder: String\n}\n"},
	)
	require.Nil(t, err)
	return schema
}

// TestCompanionDeclaresEveryDirective guards the file octoqlgen writes beside a
// schema.  A malformed declaration would not break generation, which uses its
// own copy, so it would surface only as a broken editor.
func TestCompanionDeclaresEveryDirective(t *testing.T) {
	schema := loadCompanion(t)

	for name, locations := range map[string][]ast.DirectiveLocation{
		Name: {
			ast.LocationQuery,
			ast.LocationMutation,
			ast.LocationField,
			ast.LocationFragmentDefinition,
			ast.LocationVariableDefinition,
		},
		DefaultsName: {
			ast.LocationQuery,
			ast.LocationMutation,
			ast.LocationFragmentDefinition,
		},
		ForName: {
			ast.LocationQuery,
			ast.LocationMutation,
			ast.LocationFragmentDefinition,
		},
	} {
		t.Run(name, func(t *testing.T) {
			definition := schema.Directives[name]
			require.NotNil(t, definition)
			assert.True(t, definition.IsRepeatable)
			assert.ElementsMatch(t, locations, definition.Locations)
		})
	}
}

// TestScopedDirectivesNarrowTheirOptions checks the property the split exists
// for: an option that only makes sense in one scope is not an argument of the
// others, so GraphQL rejects it instead of octoqlgen having to.
func TestScopedDirectivesNarrowTheirOptions(t *testing.T) {
	schema := loadCompanion(t)

	argumentNames := func(name string) []string {
		var names []string
		for _, argument := range schema.Directives[name].Arguments {
			names = append(names, argument.Name)
		}
		return names
	}

	// alias and typename name one generated construct, so as a default they
	// would ask for one name to be used many times.  flatten applies only where
	// a selection is a single fragment spread.
	assert.NotContains(t, argumentNames(DefaultsName), "alias")
	assert.NotContains(t, argumentNames(DefaultsName), "typename")
	assert.NotContains(t, argumentNames(DefaultsName), "flatten")
	assert.NotContains(t, argumentNames(DefaultsName), "bind")

	// struct and flatten both depend on a particular selection, which a
	// type-wide declaration does not have.
	assert.NotContains(t, argumentNames(ForName), "struct")
	assert.NotContains(t, argumentNames(ForName), "flatten")

	// The target is required, so GraphQL rejects a declaration without one.
	field := schema.Directives[ForName].Arguments.ForName("field")
	require.NotNil(t, field)
	assert.True(t, field.Type.NonNull)
}

// TestEveryArgumentIsDocumented keeps the declarations self-describing.
//
// The descriptions are what an editor shows on hover, and the SDL is the only
// place they can live, so an argument added without one is invisible to the
// tooling this file exists to serve.
func TestEveryArgumentIsDocumented(t *testing.T) {
	schema := loadCompanion(t)

	checked := 0
	for _, name := range []string{Name, DefaultsName, ForName} {
		for _, argument := range schema.Directives[name].Arguments {
			assert.NotEmpty(t, strings.TrimSpace(argument.Description),
				"@%s(%s:) needs a description; it is what an editor shows on hover",
				name, argument.Name)
			checked++
		}
	}
	assert.Positive(t, checked)
}
