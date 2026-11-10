package directive

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func loadCompanion(t *testing.T) *ast.Schema {
	schema, err := gqlparser.LoadSchema(
		&ast.Source{Name: FileName, Input: FileContents},
		&ast.Source{Name: "schema.graphql", Input: "type Query {\n  placeholder: String\n}\n"},
	)
	require.Nil(t, err)
	return schema
}

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

func TestScopedDirectivesNarrowTheirOptions(t *testing.T) {
	schema := loadCompanion(t)

	argumentNames := func(name string) []string {
		var names []string
		for _, argument := range schema.Directives[name].Arguments {
			names = append(names, argument.Name)
		}
		return names
	}

	assert.NotContains(t, argumentNames(DefaultsName), "alias")
	assert.NotContains(t, argumentNames(DefaultsName), "typename")
	assert.NotContains(t, argumentNames(DefaultsName), "flatten")
	assert.NotContains(t, argumentNames(DefaultsName), "bind")

	assert.NotContains(t, argumentNames(ForName), "struct")
	assert.NotContains(t, argumentNames(ForName), "flatten")

	field := schema.Directives[ForName].Arguments.ForName("field")
	require.NotNil(t, field)
	assert.True(t, field.Type.NonNull)
}

func TestEveryArgumentIsDocumented(t *testing.T) {
	schema := loadCompanion(t)

	checked := 0
	for _, name := range []string{Name, DefaultsName, ForName} {
		for _, argument := range schema.Directives[name].Arguments {
			assert.NotEmpty(t, strings.TrimSpace(argument.Description),
				"@%s(%s:) needs a description for editor hover text",
				name, argument.Name)
			checked++
		}
	}
	assert.Positive(t, checked)
}
