package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newInterfaceGetterField builds a shared field that members() renders as a
// Get<goName> getter returning the given Go type reference.
func newInterfaceGetterField(goName, graphQLName, goRef string) *goStructField {
	return &goStructField{
		GoName:      goName,
		GraphQLName: graphQLName,
		GoType:      &goOpaqueType{GoRef: goRef},
	}
}

// TestCheckInterfaceIdentifiersAliasSignatures verifies that the interface
// method-set check compares getter result types by Go's identity rather than by
// rendered spelling.  Two overlapping getters promoted from distinct interfaces
// whose result types are the same Go type spelled through a predeclared alias
// (byte vs uint8, rune vs int32, any vs interface{}, including those aliases
// nested inside a slice) legally collapse and must not be rejected, while
// genuinely different result types are still rejected.
//
// This over-rejection cannot be isolated through the full
// generate-and-compile pipeline: any concrete implementer of such an interface
// must provide both same-named getters, which is itself a legitimate
// struct-getter collision that fails generation first.  The interface method
// set is valid Go only in isolation, so the check is exercised directly here.
func TestCheckInterfaceIdentifiersAliasSignatures(t *testing.T) {
	tests := []struct {
		name         string
		parentRef    string
		embeddedRef  string
		wantConflict bool
	}{
		{name: "byte and uint8 agree", parentRef: "byte", embeddedRef: "uint8"},
		{name: "rune and int32 agree", parentRef: "rune", embeddedRef: "int32"},
		{name: "any and interface literal agree", parentRef: "any", embeddedRef: "interface{}"},
		{name: "slice of byte and slice of uint8 agree", parentRef: "[]byte", embeddedRef: "[]uint8"},
		{name: "byte and string conflict", parentRef: "byte", embeddedRef: "string", wantConflict: true},
		{name: "slice of byte and slice of string conflict", parentRef: "[]byte", embeddedRef: "[]string", wantConflict: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			embedded := &goInterfaceType{
				GoName:       "Frag",
				SharedFields: []*goStructField{newInterfaceGetterField("Foo", "Foo", test.embeddedRef)},
			}
			parent := &goInterfaceType{
				GoName: "Parent",
				SharedFields: []*goStructField{
					newInterfaceGetterField("Foo", "foo", test.parentRef),
					{GoType: embedded}, // embedded interface element (GoName == "")
				},
			}

			err := (&generator{}).checkInterfaceIdentifiers(parent)
			if test.wantConflict {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "two methods named GetFoo that do not agree")
				return
			}
			require.NoError(t, err)
		})
	}
}
