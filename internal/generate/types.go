package generate

// This file defines the data structures from which octoqlgen generates types,
// and the code to write them out as actual Go code.

import (
	"fmt"
	"io"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// goType represents a Go type in the generation plan.
type goType interface {
	WriteDefinition(io.Writer, *renderer) error

	// QualifiedRef returns the canonical spelling of this type, such as
	// []*MyStruct or []github.com/pkg.Type.
	QualifiedRef() string

	// renderRef formats this type relative to scope, registering any external
	// imports needed by bound types.
	renderRef(*importScope) string

	// GraphQLTypeName returns the name of the GraphQL type to which this Go type
	// corresponds.
	GraphQLTypeName() string

	// Unwrap removes slice/pointer wrappers and returns the underlying type.
	Unwrap() goType

	// SliceDepth counts the outer slice wrappers on this type.
	SliceDepth() int
}

var (
	_ goType = (*goOpaqueType)(nil)
	_ goType = (*goTypenameForBuiltinType)(nil)
	_ goType = (*goSliceType)(nil)
	_ goType = (*goPointerType)(nil)
	_ goType = (*goEnumType)(nil)
	_ goType = (*goStructType)(nil)
	_ goType = (*goInterfaceType)(nil)
)

type (
	// goOpaqueType represents a user-defined or builtin type bound to a GraphQL type.
	goOpaqueType struct {
		Ref         goRef
		GraphQLName string
	}
	// goTypenameForBuiltinType represents a builtin type given a distinct Go
	// type declaration via @octoqlgen(typename: ...).
	goTypenameForBuiltinType struct {
		GoTypeName    string
		GoBuiltinName string
		GraphQLName   string
	}
	// goSliceType represents []Elem for a GraphQL list type.
	goSliceType struct {
		Elem goType
	}
	// goPointerType represents *Elem.
	goPointerType struct {
		Elem goType
	}
)

func (t *goOpaqueType) WriteDefinition(io.Writer, *renderer) error { return nil }

func (t *goTypenameForBuiltinType) WriteDefinition(w io.Writer, _ *renderer) error {
	fmt.Fprintf(w, "type %s %s", t.GoTypeName, t.GoBuiltinName)
	return nil
}

func (t *goSliceType) WriteDefinition(io.Writer, *renderer) error   { return nil }
func (t *goPointerType) WriteDefinition(io.Writer, *renderer) error { return nil }

func (t *goOpaqueType) QualifiedRef() string             { return t.Ref.qualified() }
func (t *goTypenameForBuiltinType) QualifiedRef() string { return t.GoTypeName }
func (t *goSliceType) QualifiedRef() string              { return "[]" + t.Elem.QualifiedRef() }
func (t *goPointerType) QualifiedRef() string            { return "*" + t.Elem.QualifiedRef() }

func (t *goOpaqueType) renderRef(scope *importScope) string {
	return t.Ref.render(scope)
}

func (t *goTypenameForBuiltinType) renderRef(*importScope) string {
	return t.GoTypeName
}

func (t *goSliceType) renderRef(scope *importScope) string {
	return "[]" + t.Elem.renderRef(scope)
}

func (t *goPointerType) renderRef(scope *importScope) string {
	return "*" + t.Elem.renderRef(scope)
}

func (t *goOpaqueType) GraphQLTypeName() string             { return t.GraphQLName }
func (t *goTypenameForBuiltinType) GraphQLTypeName() string { return t.GraphQLName }
func (t *goSliceType) GraphQLTypeName() string              { return t.Elem.GraphQLTypeName() }
func (t *goPointerType) GraphQLTypeName() string            { return t.Elem.GraphQLTypeName() }

// goEnumType represents a Go named-string type for a GraphQL enum.
type goEnumType struct {
	GoName      string
	GraphQLName string
	Description string
	Values      []goEnumValue
}

type goEnumValue struct {
	GoName      string
	GraphQLName string
	Description string
}

func (t *goEnumType) WriteDefinition(w io.Writer, _ *renderer) error {
	writeDescription(w, t.Description)
	fmt.Fprintf(w, "type %s string\n", t.GoName)
	fmt.Fprintf(w, "const (\n")
	for _, val := range t.Values {
		writeDescription(w, val.Description)
		fmt.Fprintf(w, "%s %s = %q\n", val.GoName, t.GoName, val.GraphQLName)
	}
	fmt.Fprintf(w, ")\n")
	return nil
}

func (t *goEnumType) QualifiedRef() string          { return t.GoName }
func (t *goEnumType) renderRef(*importScope) string { return t.GoName }
func (t *goEnumType) GraphQLTypeName() string       { return t.GraphQLName }

// goStructType represents a Go struct type for a GraphQL object or input-object.
type goStructType struct {
	GoName           string
	Fields           []*goStructField
	InterfaceGetters map[string]bool
	IsInput          bool
	descriptionInfo
}

func (t *goStructType) addInterfaceGetters(fieldNames []string) {
	if len(fieldNames) == 0 {
		return
	}
	if t.InterfaceGetters == nil {
		t.InterfaceGetters = make(map[string]bool, len(fieldNames))
	}
	for _, name := range fieldNames {
		t.InterfaceGetters[name] = true
	}
}

type goStructField struct {
	GoName      string
	GoType      goType
	JSONName    string
	GraphQLName string
	Omitempty   bool
	Description string
	Position    *ast.Position
}

func (f *goStructField) IsEmbedded() bool {
	return f.GoName == ""
}

func (f *goStructField) Selector() string {
	if f.GoName != "" {
		return f.GoName
	}
	return f.GoType.Unwrap().QualifiedRef()
}

// NeedsMarshaling reports whether this field requires custom JSON
// marshaling/unmarshaling (embedded fragments or abstract interface fields).
func (f *goStructField) NeedsMarshaling() bool {
	if f.IsEmbedded() {
		return true
	}
	_, isInterface := f.GoType.Unwrap().(*goInterfaceType)
	return isInterface
}

func (t *goStructType) NeedsMarshaling() bool {
	for _, f := range t.Fields {
		if f.NeedsMarshaling() {
			return true
		}
	}
	return false
}

type selector struct {
	*goStructField
	Selector string
}

// FlattenedFields returns the breadth-first deduplicated fields of this struct
// and its embedded fragment structs, picking the shallowest (then earliest)
// field for each JSON key.
func (t *goStructType) FlattenedFields() ([]*selector, error) {
	seenJSONNames := map[string]bool{}
	retval := make([]*selector, 0, len(t.Fields))

	queue := make([]*selector, len(t.Fields))
	for i, field := range t.Fields {
		queue[i] = &selector{
			goStructField: field,
			Selector:      field.Selector(),
		}
	}

	for len(queue) > 0 {
		field := queue[0]
		queue = queue[1:]
		if field.IsEmbedded() {
			structField, ok := field.GoType.(*goStructType)
			if !ok {
				return nil, errorf(nil,
					"octoqlgen internal error: embedded field %s.%s was not a struct",
					t.GoName, field.GoName)
			}

			for _, subField := range structField.Fields {
				queue = append(queue, &selector{
					goStructField: subField,
					Selector:      field.Selector + "." + subField.Selector(),
				})
			}
			continue
		}

		if seenJSONNames[field.JSONName] {
			continue
		}

		seenJSONNames[field.JSONName] = true
		retval = append(retval, field)
	}
	return retval, nil
}

func (t *goStructType) WriteDefinition(w io.Writer, r *renderer) error {
	writeDescription(w, structDescription(t))

	fmt.Fprintf(w, "type %s struct {\n", t.GoName)
	for _, field := range t.Fields {
		writeDescription(w, field.Description)
		jsonTag := `"` + field.JSONName
		if field.Omitempty {
			jsonTag += ",omitempty"
		}
		jsonTag += `"`
		if field.NeedsMarshaling() {
			jsonTag = `"-"`
		}
		fmt.Fprintf(w, "\t%s %s `json:%s`\n", field.GoName, r.typeRef(field.GoType), jsonTag)
	}
	fmt.Fprintf(w, "}\n")

	if !t.IsInput && len(t.InterfaceGetters) > 0 {
		flattened, err := t.FlattenedFields()
		if err != nil {
			return err
		}
		for _, field := range flattened {
			if !t.InterfaceGetters[field.GoName] {
				continue
			}
			description := fmt.Sprintf(
				"Get%s returns %s.%s, and is useful for accessing the field via an interface.",
				field.GoName, t.GoName, field.GoName,
			)
			writeDescription(w, description)
			fmt.Fprintf(w, "func (v *%s) Get%s() %s { return v.%s }\n",
				t.GoName, field.GoName, r.typeRef(field.GoType), field.Selector)
		}
	}

	if t.NeedsMarshaling() {
		err := r.render("unmarshal.go.tmpl", w, t)
		if err != nil {
			return err
		}
		err = r.render("marshal.go.tmpl", w, t)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *goStructType) QualifiedRef() string          { return t.GoName }
func (t *goStructType) renderRef(*importScope) string { return t.GoName }
func (t *goStructType) GraphQLTypeName() string       { return t.GraphQLName }

// goInterfaceType represents a Go interface type for a GraphQL interface or union.
type goInterfaceType struct {
	GoName              string
	SharedFields        []*goStructField
	Implementations     []*goStructType
	OtherImplementation *goStructType
	descriptionInfo
}

func (t *goInterfaceType) markerMethodName() string {
	return "implementsGraphQLInterface" + t.GoName
}

func (t *goInterfaceType) WriteDefinition(w io.Writer, r *renderer) error {
	writeDescription(w, interfaceDescription(t))

	fmt.Fprintf(w, "type %s interface {\n", t.GoName)
	fmt.Fprintf(w, "\t%s()\n", t.markerMethodName())
	for _, field := range t.SharedFields {
		ref := r.typeRef(field.GoType)
		if field.IsEmbedded() {
			fmt.Fprintf(w, "\t%s\n", ref)
			continue
		}
		methodName := "Get" + field.GoName
		description := fmt.Sprintf(
			`%s returns the interface-field %q from its implementation.`,
			methodName, field.GraphQLName,
		)
		if field.Description != "" {
			description = fmt.Sprintf(
				"%s\nThe GraphQL interface field's documentation follows.\n\n%s",
				description, field.Description,
			)
		}
		if field.GraphQLName == "__typename" {
			description = fmt.Sprintf(
				"%s returns the receiver's concrete GraphQL type-name "+
					"(see interface doc for possible values).", methodName,
			)
		}

		writeDescription(w, description)
		fmt.Fprintf(w, "\t%s() %s\n", methodName, ref)
	}
	fmt.Fprintf(w, "}\n")

	implementsMethodName := t.markerMethodName()
	for _, impl := range t.Implementations {
		fmt.Fprintf(w, "func (v *%s) %s() {}\n", impl.GoName, implementsMethodName)
	}
	fmt.Fprintf(w, "func (v *%s) %s() {}\n", t.OtherImplementation.GoName, implementsMethodName)

	err := r.render("unmarshal_helper.go.tmpl", w, t)
	if err != nil {
		return err
	}
	return r.render("marshal_helper.go.tmpl", w, t)
}

func (t *goInterfaceType) QualifiedRef() string          { return t.GoName }
func (t *goInterfaceType) renderRef(*importScope) string { return t.GoName }
func (t *goInterfaceType) GraphQLTypeName() string       { return t.GraphQLName }

func (t *goOpaqueType) Unwrap() goType             { return t }
func (t *goTypenameForBuiltinType) Unwrap() goType { return t }
func (t *goSliceType) Unwrap() goType              { return t.Elem.Unwrap() }
func (t *goPointerType) Unwrap() goType            { return t.Elem.Unwrap() }
func (t *goEnumType) Unwrap() goType               { return t }
func (t *goStructType) Unwrap() goType             { return t }
func (t *goInterfaceType) Unwrap() goType          { return t }

func (t *goOpaqueType) SliceDepth() int             { return 0 }
func (t *goTypenameForBuiltinType) SliceDepth() int { return 0 }
func (t *goSliceType) SliceDepth() int              { return t.Elem.SliceDepth() + 1 }
func (t *goPointerType) SliceDepth() int            { return 0 }
func (t *goEnumType) SliceDepth() int               { return 0 }
func (t *goStructType) SliceDepth() int             { return 0 }
func (t *goInterfaceType) SliceDepth() int          { return 0 }

func writeDescription(w io.Writer, desc string) {
	if desc != "" {
		for line := range strings.SplitSeq(desc, "\n") {
			fmt.Fprintf(w, "// %s\n", strings.TrimLeft(line, " \t"))
		}
	}
}
