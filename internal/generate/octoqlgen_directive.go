package generate

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Represents the octoqlgen comment directive, described in detail in
// docs/octoqlgen_directive.graphql.
type octoqlgenDirective struct {
	pos       *ast.Position
	Omitempty *bool
	Pointer   *bool
	Struct    *bool
	Bind      string
	TypeName  string
	Alias     string
	// FieldDirectives contains the directives to be
	// applied to specific fields via the "for" option.
	// Map from type-name -> field-name -> directive.
	FieldDirectives map[string]map[string]*octoqlgenDirective
}

func newOctoqlgenDirective(pos *ast.Position) *octoqlgenDirective {
	return &octoqlgenDirective{
		pos:             pos,
		FieldDirectives: make(map[string]map[string]*octoqlgenDirective),
	}
}

func (d *octoqlgenDirective) GetOmitempty() bool   { return d.Omitempty != nil && *d.Omitempty }
func (d *octoqlgenDirective) GetPointer() bool     { return d.Pointer != nil && *d.Pointer }
func (d *octoqlgenDirective) PointerIsFalse() bool { return d.Pointer != nil && !*d.Pointer }
func (d *octoqlgenDirective) GetStruct() bool      { return d.Struct != nil && *d.Struct }

func setBool(optionName string, dst **bool, v *ast.Value, pos *ast.Position) error {
	if *dst != nil {
		return errorf(pos, "conflicting values for %v", optionName)
	}
	ei, err := v.Value(nil) // no vars allowed
	if err != nil {
		return errorf(pos, "invalid boolean value %v: %v", v, err)
	}
	if b, ok := ei.(bool); ok {
		*dst = &b
		return nil
	}
	return errorf(pos, "expected boolean, got non-boolean value %T(%v)", ei, ei)
}

func setString(optionName string, dst *string, v *ast.Value, pos *ast.Position) error {
	if *dst != "" {
		return errorf(pos, "conflicting values for %v", optionName)
	}
	ei, err := v.Value(nil) // no vars allowed
	if err != nil {
		return errorf(pos, "invalid string value %v: %v", v, err)
	}
	if b, ok := ei.(string); ok {
		*dst = b
		return nil
	}
	return errorf(pos, "expected string, got non-string value %T(%v)", ei, ei)
}

// add adds to this octoqlgenDirective struct the settings from the given
// GraphQL directive.
//
// If multiple octoqlgen directives are applied to the same node,
// e.g.
//
//	# @octoqlgen(...)
//	# @octoqlgen(...)
//
// add will be called several times.  In this case, conflicts between the
// options are an error.
func (d *octoqlgenDirective) add(graphQLDirective *ast.Directive, pos *ast.Position) error {
	if graphQLDirective.Name != "octoqlgen" {
		// Actually we just won't get here; we only get here if the line starts
		// with "# @octoqlgen", unless there's some sort of bug.
		return errorf(pos, "the only valid comment-directive is @octoqlgen, got %v", graphQLDirective.Name)
	}

	// First, see if this directive has a "for" option;
	// if it does, the rest of our work will operate on the
	// appropriate place in FieldDirectives.
	var err error
	forField := ""
	for _, arg := range graphQLDirective.Arguments {
		if arg.Name == "for" {
			if forField != "" {
				return errorf(pos, `@octoqlgen directive had "for:" twice`)
			}
			err = setString("for", &forField, arg.Value, pos)
			if err != nil {
				return err
			}
		}
	}
	if forField != "" {
		forParts := strings.Split(forField, ".")
		if len(forParts) != 2 {
			return errorf(pos, `for must be of the form "MyType.myField"`)
		}
		typeName, fieldName := forParts[0], forParts[1]

		if d.FieldDirectives[typeName] == nil {
			d.FieldDirectives[typeName] = make(map[string]*octoqlgenDirective)
		}
		fieldDir := d.FieldDirectives[typeName][fieldName]
		if fieldDir == nil {
			fieldDir = newOctoqlgenDirective(pos)
			d.FieldDirectives[typeName][fieldName] = fieldDir
		}

		// Now, the rest of the function will operate on fieldDir.
		d = fieldDir
	}

	// Now parse the rest of the arguments.
	for _, arg := range graphQLDirective.Arguments {
		switch arg.Name {
		// TODO(benkraft): Use reflect and struct tags?
		case "omitempty":
			err = setBool("omitempty", &d.Omitempty, arg.Value, pos)
		case "pointer":
			err = setBool("pointer", &d.Pointer, arg.Value, pos)
		case "struct":
			err = setBool("struct", &d.Struct, arg.Value, pos)
		case "bind":
			err = setString("bind", &d.Bind, arg.Value, pos)
		case "typename":
			err = setString("typename", &d.TypeName, arg.Value, pos)
		case "alias":
			err = setString("alias", &d.Alias, arg.Value, pos)
		case "for":
			// handled above
		default:
			return errorf(pos, "unknown argument %v for @octoqlgen", arg.Name)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *octoqlgenDirective) validate(node interface{}, schema *ast.Schema) error {
	// TODO(benkraft): This function has a lot of duplicated checks, figure out
	// how to organize them better to avoid the duplication.
	for typeName, byField := range d.FieldDirectives {
		typ, ok := schema.Types[typeName]
		if !ok {
			return errorf(d.pos, `for got invalid type-name "%s"`, typeName)
		}
		for fieldName, fieldDir := range byField {
			var field *ast.FieldDefinition
			for _, typeField := range typ.Fields {
				if typeField.Name == fieldName {
					field = typeField
					break
				}
			}
			if field == nil {
				return errorf(fieldDir.pos,
					`for got invalid field-name "%s" for type "%s"`,
					fieldName, typeName)
			}

			// Struct requires per-use validation, so it can't be applied here.
			if fieldDir.Struct != nil {
				return errorf(fieldDir.pos, "struct can't be used via for")
			}

			if fieldDir.TypeName != "" && fieldDir.Bind != "" && fieldDir.Bind != "-" {
				return errorf(fieldDir.pos, "typename and bind may not be used together")
			}
		}
	}

	switch node := node.(type) {
	case *ast.OperationDefinition:
		if d.Bind != "" {
			return errorf(d.pos, "bind may not be applied to the entire operation")
		}

		// Anything else is valid on the entire operation; it will just apply
		// to whatever it is relevant to.
		return nil
	case *ast.FragmentDefinition:
		if d.Bind != "" {
			// TODO(benkraft): Implement this if people find it useful.
			return errorf(d.pos, "bind is not implemented for named fragments")
		}

		if d.Struct != nil {
			return errorf(d.pos, "struct is only applicable to fields, not fragment-definitions")
		}

		// Like operations, anything else will just apply to the entire
		// fragment.
		return nil
	case *ast.VariableDefinition:
		if d.Omitempty != nil && node.Type.NonNull {
			return errorf(d.pos, "omitempty may only be used on optional arguments")
		}

		if d.Struct != nil {
			return errorf(d.pos, "struct is only applicable to fields, not variable-definitions")
		}

		if len(d.FieldDirectives) > 0 {
			return errorf(d.pos, "for is only applicable to operations and arguments")
		}

		if d.TypeName != "" && d.Bind != "" && d.Bind != "-" {
			return errorf(d.pos, "typename and bind may not be used together")
		}

		return nil
	case *ast.Field:
		if d.Omitempty != nil {
			return errorf(d.pos, "omitempty is not applicable to variables, not fields")
		}

		typ := schema.Types[node.Definition.Type.Name()]
		if d.Struct != nil {
			if err := validateStructOption(typ, node.SelectionSet, d.pos); err != nil {
				return err
			}
		}

		if len(d.FieldDirectives) > 0 {
			return errorf(d.pos, "for is only applicable to operations and arguments")
		}

		if d.TypeName != "" && d.Bind != "" && d.Bind != "-" {
			return errorf(d.pos, "typename and bind may not be used together")
		}

		return nil
	default:
		return errorf(d.pos, "invalid @octoqlgen directive location: %T", node)
	}
}

func validateStructOption(
	typ *ast.Definition,
	selectionSet ast.SelectionSet,
	pos *ast.Position,
) error {
	if typ.Kind != ast.Interface && typ.Kind != ast.Union {
		return errorf(pos, "struct is only applicable to interface-typed fields")
	}

	// Make sure that all the requested fields apply to the interface itself
	// (not just certain implementations).
	for _, selection := range selectionSet {
		switch selection.(type) {
		case *ast.Field:
			// fields are fine.
		case *ast.InlineFragment, *ast.FragmentSpread:
			// Fragments aren't allowed. In principle we could allow them under
			// the condition that the fragment applies to the whole interface
			// (not just one implementation; and so on recursively), and for
			// fragment spreads additionally that the fragment has the same
			// option applied to it, but it seems more trouble than it's worth
			// right now.
			return errorf(pos, "struct is not allowed for types with fragments")
		}
	}
	return nil
}

func fillDefaultBool(target **bool, defaults ...*bool) {
	if *target != nil {
		return
	}

	for _, val := range defaults {
		if val != nil {
			*target = val
			return
		}
	}
}

func fillDefaultString(target *string, defaults ...string) {
	if *target != "" {
		return
	}

	for _, val := range defaults {
		if val != "" {
			*target = val
			return
		}
	}
}

// merge updates the receiver, which is a directive applied to some node, with
// the information from the directive applied to the fragment or operation
// containing that node.  (The update is in-place.)
//
// Note this has slightly different semantics than .add(), see inline for
// details.
//
// parent is as described in parsePrecedingComment.  operationDirective is the
// directive applied to this operation or fragment.
func (d *octoqlgenDirective) mergeOperationDirective(
	node interface{},
	parentIfInputField *ast.Definition,
	operationDirective *octoqlgenDirective,
) {
	// We'll set forField to the `@octoqlgen(for: "<this field>", ...)`
	// directive from our operation/fragment, if any.
	var forField *octoqlgenDirective
	switch field := node.(type) {
	case *ast.Field: // query field
		typeName := field.ObjectDefinition.Name
		forField = operationDirective.FieldDirectives[typeName][field.Name]
	case *ast.FieldDefinition: // input-type field
		forField = operationDirective.FieldDirectives[parentIfInputField.Name][field.Name]
	}
	// Just to simplify nil-checking in the code below:
	if forField == nil {
		forField = newOctoqlgenDirective(nil)
	}

	// Now fill defaults; in general local directive wins over the "for" field
	// directive wins over the operation directive.
	fillDefaultBool(&d.Omitempty, forField.Omitempty, operationDirective.Omitempty)
	fillDefaultBool(&d.Pointer, forField.Pointer, operationDirective.Pointer)
	// struct isn't settable via "for".
	fillDefaultBool(&d.Struct, operationDirective.Struct)
	fillDefaultString(&d.Bind, forField.Bind, operationDirective.Bind)
	// typename isn't settable on the operation (when set there it replies to
	// the response-type).
	fillDefaultString(&d.TypeName, forField.TypeName)
	fillDefaultString(&d.Alias, forField.Alias, operationDirective.Alias)
}

// parsePrecedingComment looks at the comment right before this node, and
// returns the octoqlgen directive applied to it (or an empty one if there is
// none), the remaining human-readable comment (or "" if there is none), and an
// error if the directive is invalid.
//
// queryOptions are the options to be applied to this entire query (or
// fragment); the local options will be merged into those.  It should be nil if
// we are parsing the directive on the entire query.
//
// parentIfInputField need only be set if node is an input-type field; it
// should be the type containing this field.  (We can get this from gqlparser
// in other cases, but not input-type fields.)
func (g *generator) parsePrecedingComment(
	node interface{},
	parentIfInputField *ast.Definition,
	pos *ast.Position,
	queryOptions *octoqlgenDirective,
) (comment string, directive *octoqlgenDirective, err error) {
	directive = newOctoqlgenDirective(pos)
	hasDirective := false

	// For directives on octoqlgen-generated nodes, we don't actually need to
	// parse anything.  (But we do need to merge below.)
	var commentLines []string
	if pos != nil && pos.Src != nil {
		sourceLines := strings.Split(pos.Src.Input, "\n")
		for i := pos.Line - 1; i > 0; i-- {
			line := strings.TrimSpace(sourceLines[i-1])
			trimmed := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if strings.HasPrefix(line, "# @octoqlgen") {
				hasDirective = true
				var graphQLDirective *ast.Directive
				graphQLDirective, err = parseDirective(trimmed, pos)
				if err != nil {
					return "", nil, err
				}
				err = directive.add(graphQLDirective, pos)
				if err != nil {
					return "", nil, err
				}
			} else if strings.HasPrefix(line, "#") {
				commentLines = append(commentLines, trimmed)
			} else {
				break
			}
		}
	}

	if hasDirective { // (else directive is empty)
		err = directive.validate(node, g.schema)
		if err != nil {
			return "", nil, err
		}
	}

	if queryOptions != nil {
		// If we are part of an operation/fragment, merge its options in.
		directive.mergeOperationDirective(node, parentIfInputField, queryOptions)

		// TODO(benkraft): Really we should do all the validation after
		// merging, probably?  But this is the only check that can fail only
		// after merging, and it's a bit tricky because the "does not apply"
		// checks may need to happen before merging so we know where the
		// directive "is".
		if directive.TypeName != "" && directive.Bind != "" && directive.Bind != "-" {
			return "", nil, errorf(directive.pos, "typename and bind may not be used together")
		}
	}

	reverse(commentLines)

	return strings.TrimSpace(strings.Join(commentLines, "\n")), directive, nil
}

func parseDirective(line string, pos *ast.Position) (*ast.Directive, error) {
	// HACK: parse the "directive" by making a fake query containing it.
	fakeQuery := fmt.Sprintf("query %v { field }", line)
	doc, err := parser.ParseQuery(&ast.Source{Input: fakeQuery})
	if err != nil {
		return nil, errorf(pos, "invalid octoqlgen directive: %v", err)
	}
	return doc.Operations[0].Directives[0], nil
}
