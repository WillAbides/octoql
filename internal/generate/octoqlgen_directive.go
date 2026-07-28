package generate

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/willabides/octoql/internal/directive"
)

// Represents the @octoqlgen directive, described in detail in
// docs/directive.md.
type octoqlgenDirective struct {
	pos       *ast.Position
	Omitempty *bool
	Pointer   *bool
	Struct    *bool
	Flatten   *bool
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
func (d *octoqlgenDirective) GetFlatten() bool     { return d.Flatten != nil && *d.Flatten }

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
		if b == "" {
			return errorf(pos, "%s must not be empty", optionName)
		}
		*dst = b
		return nil
	}
	return errorf(pos, "expected string, got non-string value %T(%v)", ei, ei)
}

// add adds to this octoqlgenDirective struct the settings from the given
// GraphQL directive.
//
// The directive is declared repeatable, so a node may carry several, e.g.
//
//	myField @octoqlgen(...) @octoqlgen(...)
//
// add will be called several times.  In this case, conflicts between the
// options are an error.
func (d *octoqlgenDirective) add(graphQLDirective *ast.Directive, pos *ast.Position) error {
	if graphQLDirective.Name != octoqlgenDirectiveName {
		// Callers filter by name, so this only fires on an octoqlgen bug.
		return errorf(pos, "the only valid directive is @%s, got %v",
			octoqlgenDirectiveName, graphQLDirective.Name)
	}

	// First, see if this directive has a "for" option;
	// if it does, the rest of our work will operate on the
	// appropriate place in FieldDirectives.
	var err error
	forField := ""
	hasForField := false
	for _, arg := range graphQLDirective.Arguments {
		if arg.Name != "for" {
			continue
		}
		if hasForField {
			return errorf(pos, `@octoqlgen directive had "for:" twice`)
		}
		hasForField = true
		err = setString("for", &forField, arg.Value, pos)
		if err != nil {
			return err
		}
		if forField == "" {
			return errorf(pos, "for must not be empty")
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
		case "flatten":
			err = setBool("flatten", &d.Flatten, arg.Value, pos)
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
			if fieldDir.Flatten != nil {
				return errorf(fieldDir.pos, "flatten can't be used via for")
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
		if d.GetOmitempty() && node.Type.NonNull && node.DefaultValue == nil {
			return errorf(d.pos, "omitempty may only be used on optional arguments")
		}

		if d.Struct != nil {
			return errorf(d.pos, "struct is only applicable to fields, not variable-definitions")
		}
		if d.Flatten != nil {
			return errorf(d.pos, "flatten is only applicable to fields, not variable-definitions")
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
			err := validateStructOption(typ, node.SelectionSet, d.pos)
			if err != nil {
				return err
			}
		}
		if d.Flatten != nil {
			_, err := validateFlattenOption(typ, node.SelectionSet, d.pos)
			if err != nil {
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

func validateFlattenOption(
	typ *ast.Definition,
	selectionSet ast.SelectionSet,
	pos *ast.Position,
) (int, error) {
	index := -1
	if len(selectionSet) == 0 {
		return index, errorf(pos, "flatten is not allowed for leaf fields")
	}

	for i, selection := range selectionSet {
		switch selection := selection.(type) {
		case *ast.Field:
			// Ignore __typename when octoqlgen added it for abstract decoding.
			if selection.Name == "__typename" && selection.Position == nil {
				continue
			}
			return -1, errorf(pos, "flatten is not yet supported for fields (only fragment spreads)")
		case *ast.InlineFragment:
			return -1, errorf(pos, "flatten is not allowed for selections with inline fragments")
		case *ast.FragmentSpread:
			if index != -1 {
				return -1, errorf(pos, "flatten is not allowed for fields with multiple selections")
			}
			if !fragmentMatches(typ, selection.Definition.Definition) {
				return -1, errorf(pos,
					"flatten is not allowed for fields with fragment-spreads "+
						"unless the field-type implements the fragment-type; "+
						"field-type %s does not implement fragment-type %s",
					typ.Name, selection.Definition.Definition.Name)
			}
			index = i
		}
	}
	return index, nil
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
// parentIfInputField is as described in directiveFor.  operationDirective is the
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
	// struct and flatten aren't settable via "for".
	fillDefaultBool(&d.Struct, operationDirective.Struct)
	fillDefaultBool(&d.Flatten, operationDirective.Flatten)
	fillDefaultString(&d.Bind, forField.Bind, operationDirective.Bind)
	// typename isn't settable on the operation (when set there it replies to
	// the response-type).
	fillDefaultString(&d.TypeName, forField.TypeName)
	fillDefaultString(&d.Alias, forField.Alias, operationDirective.Alias)
}

// octoqlgenDirectiveName is the name of the directive octoqlgen recognizes on
// operations, fragments, fields, and variable definitions.
const octoqlgenDirectiveName = directive.Name

// addOctoqlgenDirectiveDefinition injects the @octoqlgen declaration into the
// parsed schema document.
//
// Any declaration already in the document is discarded first.  Schema files
// octoqlgen writes carry a copy of the declaration so editors can resolve the
// directive, and that copy may have been written by a different version of
// octoqlgen or edited by hand; the generator always uses its own.
func addOctoqlgenDirectiveDefinition(document *ast.SchemaDocument) error {
	kept := document.Directives[:0]
	for _, definition := range document.Directives {
		if definition.Name == octoqlgenDirectiveName {
			continue
		}
		kept = append(kept, definition)
	}
	document.Directives = kept

	parsed, graphqlError := parser.ParseSchema(&ast.Source{
		Name:  "octoqlgen-directive.graphql",
		Input: directive.SDL,
	})
	if graphqlError != nil {
		return errorf(nil, "invalid @%s declaration (octoqlgen bug): %v",
			octoqlgenDirectiveName, graphqlError)
	}

	document.Directives = append(document.Directives, parsed.Directives...)
	return nil
}

// collectDirectives walks the validated query document, moving every
// @octoqlgen directive off the AST and into g.directives, keyed by the node it
// was attached to.
//
// Removing the directives here, once and before anything formats an operation,
// is what keeps them off the wire: the query body octoqlgen sends is re-printed
// from this same AST.  It has to happen before conversion rather than
// per-operation, because fragments are shared between operations and stripping
// them while formatting one operation would drop them before another converts.
//
// It runs after validation, so node.Definition and node.ObjectDefinition are
// populated and the per-node validation below can rely on them.
func (g *generator) collectDirectives(doc *ast.QueryDocument) error {
	for _, op := range doc.Operations {
		err := g.collectDirectivesForNode(op, &op.Directives)
		if err != nil {
			return err
		}
		for _, variable := range op.VariableDefinitions {
			err = g.collectDirectivesForNode(variable, &variable.Directives)
			if err != nil {
				return err
			}
		}
		err = g.collectDirectivesInSelectionSet(op.SelectionSet)
		if err != nil {
			return err
		}
	}

	for _, fragment := range doc.Fragments {
		err := g.collectDirectivesForNode(fragment, &fragment.Directives)
		if err != nil {
			return err
		}
		err = g.collectDirectivesInSelectionSet(fragment.SelectionSet)
		if err != nil {
			return err
		}
	}

	return nil
}

func (g *generator) collectDirectivesInSelectionSet(selectionSet ast.SelectionSet) error {
	for _, selection := range selectionSet {
		switch selection := selection.(type) {
		case *ast.Field:
			err := g.collectDirectivesForNode(selection, &selection.Directives)
			if err != nil {
				return err
			}
			err = g.collectDirectivesInSelectionSet(selection.SelectionSet)
			if err != nil {
				return err
			}
		case *ast.InlineFragment:
			err := g.collectDirectivesInSelectionSet(selection.SelectionSet)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// collectDirectivesForNode parses the @octoqlgen directives in directives,
// records the result for node, and removes them from the list so they are
// never formatted into a query body.  Other directives, such as @skip and
// @include, are left alone.
func (g *generator) collectDirectivesForNode(node any, directives *ast.DirectiveList) error {
	var ours []*ast.Directive
	var others ast.DirectiveList
	for _, graphQLDirective := range *directives {
		if graphQLDirective.Name == octoqlgenDirectiveName {
			ours = append(ours, graphQLDirective)
			continue
		}
		others = append(others, graphQLDirective)
	}
	if len(ours) == 0 {
		return nil
	}
	*directives = others

	directive := newOctoqlgenDirective(ours[0].Position)
	for _, graphQLDirective := range ours {
		err := directive.add(graphQLDirective, graphQLDirective.Position)
		if err != nil {
			return err
		}
	}

	err := directive.validate(node, g.schema)
	if err != nil {
		return err
	}

	g.directives[node] = directive
	return nil
}

// directiveFor returns the options that apply to node, merged with the options
// that apply to the containing operation or fragment.
//
// queryOptions should be nil when node is the operation or fragment itself.
// parentIfInputField need only be set if node is an input-type field; it should
// be the type containing this field.  (We can get this from gqlparser in other
// cases, but not input-type fields.)
func (g *generator) directiveFor(
	node any,
	parentIfInputField *ast.Definition,
	pos *ast.Position,
	queryOptions *octoqlgenDirective,
) (*octoqlgenDirective, error) {
	directive := g.directives[node]
	if directive == nil {
		directive = newOctoqlgenDirective(pos)
	}

	if queryOptions == nil {
		return directive, nil
	}

	// The merge mutates the directive, so operate on a copy; the same node is
	// converted more than once when a fragment is used by several operations.
	merged := *directive
	merged.mergeOperationDirective(node, parentIfInputField, queryOptions)

	// TODO(benkraft): Really we should do all the validation after
	// merging, probably?  But this is the only check that can fail only
	// after merging, and it's a bit tricky because the "does not apply"
	// checks may need to happen before merging so we know where the
	// directive "is".
	if merged.TypeName != "" && merged.Bind != "" && merged.Bind != "-" {
		return nil, errorf(merged.pos, "typename and bind may not be used together")
	}

	return &merged, nil
}

// precedingComment returns the human-readable comment immediately preceding
// the node at pos, which becomes the doc comment of the generated Go
// declaration.
//
// This reads the source directly rather than using the comment groups
// gqlparser attaches to AST nodes, because those group comments across blank
// lines and absorb a trailing comment written after code on an earlier line.
// Unlike the options, which are now real directives attached to the node they
// modify, a doc comment only affects generated prose.
func precedingComment(pos *ast.Position) string {
	if pos == nil || pos.Src == nil {
		return ""
	}

	var commentLines []string
	sourceLines := strings.Split(pos.Src.Input, "\n")
	for i := pos.Line - 1; i > 0; i-- {
		line := strings.TrimSpace(sourceLines[i-1])
		if !strings.HasPrefix(line, "#") {
			break
		}
		commentLines = append(commentLines, strings.TrimSpace(strings.TrimPrefix(line, "#")))
	}

	reverse(commentLines)
	return strings.TrimSpace(strings.Join(commentLines, "\n"))
}
