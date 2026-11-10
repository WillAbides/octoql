package generate

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/willabides/octoql/internal"
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
}

func newOctoqlgenDirective(pos *ast.Position) *octoqlgenDirective {
	return &octoqlgenDirective{
		pos: pos,
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
	if v.Kind != ast.BooleanValue {
		return errorf(pos, "expected boolean value for %s", optionName)
	}
	b := v.Raw == "true"
	*dst = &b
	return nil
}

func setString(optionName string, dst *string, v *ast.Value, pos *ast.Position) error {
	if *dst != "" {
		return errorf(pos, "conflicting values for %v", optionName)
	}
	if v.Kind != ast.StringValue && v.Kind != ast.BlockValue {
		return errorf(pos, "expected string value for %s", optionName)
	}
	if v.Raw == "" {
		return errorf(pos, "%s must not be empty", optionName)
	}
	*dst = v.Raw
	return nil
}

// applyArguments applies the option arguments of one directive to this struct.
func (d *octoqlgenDirective) applyArguments(graphQLDirective *ast.Directive, pos *ast.Position) error {
	var err error
	for _, arg := range graphQLDirective.Arguments {
		switch arg.Name {
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
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func addFor(
	forDirectives map[fieldKey]*octoqlgenDirective,
	graphQLDirective *ast.Directive,
	pos *ast.Position,
) error {
	fieldArg := graphQLDirective.Arguments.ForName("field")
	target := ""
	err := setString("field", &target, fieldArg.Value, pos)
	if err != nil {
		return err
	}

	parts := strings.Split(target, ".")
	if len(parts) != 2 {
		return errorf(pos, `field must be of the form "MyType.myField"`)
	}
	key := fieldKey{typeName: parts[0], fieldName: parts[1]}

	fieldDir := forDirectives[key]
	if fieldDir == nil {
		fieldDir = newOctoqlgenDirective(pos)
		forDirectives[key] = fieldDir
	}

	return fieldDir.applyArguments(graphQLDirective, pos)
}

type fieldKey struct {
	typeName  string
	fieldName string
}

func (k fieldKey) String() string { return k.typeName + "." + k.fieldName }

func (d *octoqlgenDirective) validateFor(key fieldKey, schema *ast.Schema) error {
	typ, ok := schema.Types[key.typeName]
	if !ok {
		return errorf(d.pos, `@%s got invalid type-name "%s"`, octoqlgenForName, key.typeName)
	}
	var field *ast.FieldDefinition
	for _, typeField := range typ.Fields {
		if typeField.Name == key.fieldName {
			field = typeField
			break
		}
	}
	if field == nil {
		return errorf(d.pos,
			`@%s got invalid field-name "%s" for type "%s"`,
			octoqlgenForName, key.fieldName, key.typeName)
	}

	if d.Omitempty != nil && typ.Kind != ast.InputObject {
		return errorf(d.pos,
			"omitempty is only applicable to input-type fields, and %s is not one",
			key)
	}
	if d.Alias != "" && typ.Kind == ast.InputObject {
		return errorf(d.pos,
			"alias is only applicable to selected fields, and %s is an input-type field",
			key)
	}

	if d.TypeName != "" && d.Bind != "" && d.Bind != "-" {
		return errorf(d.pos, "typename and bind may not be used together")
	}
	return nil
}

func (d *octoqlgenDirective) validate(node interface{}, schema *ast.Schema) error {
	if d.TypeName != "" && d.Bind != "" && d.Bind != "-" {
		return errorf(d.pos, "typename and bind may not be used together")
	}

	switch node := node.(type) {
	case *ast.OperationDefinition:
		if d.Bind != "" {
			return errorf(d.pos, "bind may not be applied to the entire operation")
		}
		if d.Alias != "" {
			return errorf(d.pos,
				"alias is only applicable to selected fields, not operations")
		}
		return nil
	case *ast.FragmentDefinition:
		if d.Bind != "" {
			// TODO(benkraft): Implement this if people find it useful.
			return errorf(d.pos, "bind is not implemented for named fragments")
		}
		if d.Alias != "" {
			return errorf(d.pos,
				"alias is only applicable to selected fields, not fragment-definitions")
		}
		if d.TypeName != "" {
			return errorf(d.pos,
				"typename is not applicable to fragment-definitions; "+
					"the generated type is named after the fragment")
		}
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
		if d.Alias != "" {
			return errorf(d.pos,
				"alias is only applicable to selected fields, not variable-definitions; "+
					"rename the variable instead")
		}
		return nil
	case *ast.Field:
		if d.Omitempty != nil {
			return errorf(d.pos,
				"omitempty is only applicable to variables and input-type fields, "+
					"not to selected fields")
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
		return nil
	}
	return nil
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

// mergeOperationDirective updates the receiver in place with the @octoqlgenFor
// declaration for that node and the @octoqlgenDefaults of the containing
// operation or fragment.
func (d *octoqlgenDirective) mergeOperationDirective(
	forField *octoqlgenDirective,
	operationDirective *octoqlgenDirective,
) {
	if forField == nil {
		forField = newOctoqlgenDirective(nil)
	}

	// Local directive wins over @octoqlgenFor, which wins over @octoqlgenDefaults.
	fillDefaultBool(&d.Omitempty, forField.Omitempty, operationDirective.Omitempty)
	fillDefaultBool(&d.Pointer, forField.Pointer, operationDirective.Pointer)
	fillDefaultBool(&d.Struct, operationDirective.Struct)
	fillDefaultString(&d.Bind, forField.Bind)
	fillDefaultString(&d.TypeName, forField.TypeName)
	fillDefaultString(&d.Alias, forField.Alias)
}

const (
	octoqlgenDirectiveName = "octoqlgen"
	octoqlgenDefaultsName  = "octoqlgenDefaults"
	octoqlgenForName       = "octoqlgenFor"
)

// addOctoqlgenDirectiveDefinition replaces any existing octoqlgen directive
// definitions in the parsed schema document with the built-in SDL.
func addOctoqlgenDirectiveDefinition(document *ast.SchemaDocument) error {
	ours := map[string]bool{
		octoqlgenDirectiveName: true,
		octoqlgenDefaultsName:  true,
		octoqlgenForName:       true,
	}
	kept := document.Directives[:0]
	for _, definition := range document.Directives {
		if ours[definition.Name] {
			continue
		}
		kept = append(kept, definition)
	}
	document.Directives = kept

	parsed, graphqlError := parser.ParseSchema(&ast.Source{
		Name:  internal.DirectiveFilename,
		Input: internal.DirectiveFileContents,
	})
	if graphqlError != nil {
		return errorf(nil, "invalid @%s declaration (octoqlgen bug): %v",
			octoqlgenDirectiveName, graphqlError)
	}

	document.Directives = append(document.Directives, parsed.Directives...)
	return nil
}

// collectDirectives strips octoqlgen directives from the validated AST and
// records them in g.directives before operations are formatted or converted.
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

// collectDirectivesForNode parses octoqlgen's directives on one node, records
// the result, and removes them from the list so they are never formatted into a
// query body.  Other directives, such as @skip and @include, are left alone.
func (g *generator) collectDirectivesForNode(node any, directives *ast.DirectiveList) error {
	var ours []*ast.Directive
	var others ast.DirectiveList
	for _, graphQLDirective := range *directives {
		switch graphQLDirective.Name {
		case octoqlgenDirectiveName, octoqlgenDefaultsName, octoqlgenForName:
			ours = append(ours, graphQLDirective)
		default:
			others = append(others, graphQLDirective)
		}
	}
	if len(ours) == 0 {
		return nil
	}
	*directives = others

	directive := newOctoqlgenDirective(ours[0].Position)
	forDirectives := make(map[fieldKey]*octoqlgenDirective)
	for _, graphQLDirective := range ours {
		pos := graphQLDirective.Position
		switch graphQLDirective.Name {
		case octoqlgenForName:
			err := addFor(forDirectives, graphQLDirective, pos)
			if err != nil {
				return err
			}
		case octoqlgenDefaultsName:
			err := directive.applyArguments(graphQLDirective, pos)
			if err != nil {
				return err
			}
		default:
			err := checkNodeOptions(node, graphQLDirective, pos)
			if err != nil {
				return err
			}
			err = directive.applyArguments(graphQLDirective, pos)
			if err != nil {
				return err
			}
		}
	}

	err := directive.validate(node, g.schema)
	if err != nil {
		return err
	}

	for key, fieldDirective := range forDirectives {
		err = fieldDirective.validateFor(key, g.schema)
		if err != nil {
			return err
		}
		err = g.recordForDeclaration(key, fieldDirective)
		if err != nil {
			return err
		}
	}

	g.directives[node] = directive
	return nil
}

var operationDefaultOptions = map[string]bool{
	"omitempty": true,
	"pointer":   true,
	"struct":    true,
}

// checkNodeOptions rejects field-default options placed on @octoqlgen at an
// operation or fragment definition.
func checkNodeOptions(node any, graphQLDirective *ast.Directive, pos *ast.Position) error {
	switch node.(type) {
	case *ast.OperationDefinition, *ast.FragmentDefinition:
	default:
		return nil
	}

	for _, arg := range graphQLDirective.Arguments {
		if !operationDefaultOptions[arg.Name] {
			continue
		}
		return errorf(pos,
			"@%s(%s:) applies to the node it is attached to, and does not describe "+
				"an operation or fragment; use @%s(%s:) to set it as a default for "+
				"the fields inside",
			octoqlgenDirectiveName, arg.Name, octoqlgenDefaultsName, arg.Name)
	}
	return nil
}

// recordForDeclaration records an @octoqlgenFor declaration and rejects
// conflicting declarations for the same type and field.
func (g *generator) recordForDeclaration(
	key fieldKey,
	declared *octoqlgenDirective,
) error {
	existing, ok := g.forDeclarations[key]
	if !ok {
		g.forDeclarations[key] = declared
		return nil
	}
	if directiveOptionsMatch(existing, declared) {
		return nil
	}
	return errorf(declared.pos,
		"conflicting @%s declarations for %s: %s asks for something different; "+
			"a named type generates one Go type, so every @%s for it must agree",
		octoqlgenForName, key, posString(existing.pos), octoqlgenForName)
}

func (g *generator) forDeclarationFor(node any, parentIfInputField *ast.Definition) *octoqlgenDirective {
	var key fieldKey
	switch field := node.(type) {
	case *ast.Field: // query field
		key = fieldKey{typeName: field.ObjectDefinition.Name, fieldName: field.Name}
	case *ast.FieldDefinition: // input-type field
		key = fieldKey{typeName: parentIfInputField.Name, fieldName: field.Name}
	default:
		return nil
	}
	return g.forDeclarations[key]
}

// directiveFor returns the options for node merged with any @octoqlgenFor
// declaration and the containing operation or fragment options.
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

	merged := *directive
	merged.mergeOperationDirective(g.forDeclarationFor(node, parentIfInputField), queryOptions)

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

// precedingComment returns the comment lines immediately preceding pos for use
// as the generated Go doc comment.
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
