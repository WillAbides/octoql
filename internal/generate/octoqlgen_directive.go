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

// applyArguments applies the option arguments of one directive to this struct.
//
// The directives are declared repeatable, so a node may carry several, e.g.
//
//	myField @octoqlgen(...) @octoqlgen(...)
//
// applyArguments will be called several times.  In this case, conflicts between
// the options are an error.
func (d *octoqlgenDirective) applyArguments(graphQLDirective *ast.Directive, pos *ast.Position) error {
	var err error
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
		case "field":
			// The target of @octoqlgenFor, read by addFor.
		default:
			// Unreachable while collection runs after validation, which
			// rejects an argument the declaration does not have.  Kept so that
			// an unknown option is refused rather than ignored if it ever runs
			// earlier.
			return errorf(pos, "unknown argument %v for @%v",
				arg.Name, graphQLDirective.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// addFor records one @octoqlgenFor directive against the type and field it
// names, and returns that target.
func (d *octoqlgenDirective) addFor(graphQLDirective *ast.Directive, pos *ast.Position) (fieldKey, error) {
	target := ""
	seen := false
	for _, arg := range graphQLDirective.Arguments {
		if arg.Name != "field" {
			continue
		}
		if seen {
			return fieldKey{}, errorf(pos, `@%s had "field:" twice`, octoqlgenForName)
		}
		seen = true
		err := setString("field", &target, arg.Value, pos)
		if err != nil {
			return fieldKey{}, err
		}
	}
	// GraphQL requires the argument to be present, but not to be non-empty.
	if target == "" {
		return fieldKey{}, errorf(pos, "field must not be empty")
	}

	parts := strings.Split(target, ".")
	if len(parts) != 2 {
		return fieldKey{}, errorf(pos, `field must be of the form "MyType.myField"`)
	}
	key := fieldKey{typeName: parts[0], fieldName: parts[1]}

	if d.FieldDirectives[key.typeName] == nil {
		d.FieldDirectives[key.typeName] = make(map[string]*octoqlgenDirective)
	}
	fieldDir := d.FieldDirectives[key.typeName][key.fieldName]
	if fieldDir == nil {
		fieldDir = newOctoqlgenDirective(pos)
		d.FieldDirectives[key.typeName][key.fieldName] = fieldDir
	}

	err := fieldDir.applyArguments(graphQLDirective, pos)
	if err != nil {
		return fieldKey{}, err
	}
	return key, nil
}

// fieldKey identifies the target of an @octoqlgenFor directive.
type fieldKey struct {
	typeName  string
	fieldName string
}

func (k fieldKey) String() string { return k.typeName + "." + k.fieldName }

func (d *octoqlgenDirective) validate(node interface{}, schema *ast.Schema) error {
	// TODO(benkraft): This function has a lot of duplicated checks, figure out
	// how to organize them better to avoid the duplication.
	for typeName, byField := range d.FieldDirectives {
		typ, ok := schema.Types[typeName]
		if !ok {
			return errorf(d.pos, `@%s got invalid type-name "%s"`, octoqlgenForName, typeName)
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
					`@%s got invalid field-name "%s" for type "%s"`,
					octoqlgenForName, fieldName, typeName)
			}

			// Only an input type's field is omitted when empty; a selected
			// field is decoded, not sent.
			if fieldDir.Omitempty != nil && typ.Kind != ast.InputObject {
				return errorf(fieldDir.pos,
					"omitempty is only applicable to input-type fields, and %s.%s is not one",
					typeName, fieldName)
			}
			// Only a selected field takes its Go name from alias; an input
			// type's fields are named after the GraphQL field.
			if fieldDir.Alias != "" && typ.Kind == ast.InputObject {
				return errorf(fieldDir.pos,
					"alias is only applicable to selected fields, and %s.%s is an input-type field",
					typeName, fieldName)
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
		// The response type is named by typename; nothing reads alias here.
		if d.Alias != "" {
			return errorf(d.pos,
				"alias is only applicable to selected fields, not operations")
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
		// A fragment's generated type is named after the fragment.
		if d.Alias != "" {
			return errorf(d.pos,
				"alias is only applicable to selected fields, not fragment-definitions")
		}
		if d.TypeName != "" {
			return errorf(d.pos,
				"typename is not applicable to fragment-definitions; "+
					"the generated type is named after the fragment")
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
		// A variables-struct field is named after the variable, so an alias
		// here would be accepted and then never read.
		if d.Alias != "" {
			return errorf(d.pos,
				"alias is only applicable to selected fields, not variable-definitions; "+
					"rename the variable instead")
		}

		if len(d.FieldDirectives) > 0 {
			return errorf(d.pos, "@%s is only applicable to operations and fragments",
				octoqlgenForName)
		}

		if d.TypeName != "" && d.Bind != "" && d.Bind != "-" {
			return errorf(d.pos, "typename and bind may not be used together")
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

		if len(d.FieldDirectives) > 0 {
			return errorf(d.pos, "@%s is only applicable to operations and fragments",
				octoqlgenForName)
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
// the @octoqlgenFor declaration for that node and the @octoqlgenDefaults of the
// operation or fragment containing it.  (The update is in-place.)
//
// Note this has slightly different semantics than applyArguments, see inline
// for details.
func (d *octoqlgenDirective) mergeOperationDirective(
	forField *octoqlgenDirective,
	operationDirective *octoqlgenDirective,
) {
	// Just to simplify nil-checking in the code below:
	if forField == nil {
		forField = newOctoqlgenDirective(nil)
	}

	// Now fill defaults; in general the local directive wins over the
	// @octoqlgenFor declaration, which wins over @octoqlgenDefaults.
	fillDefaultBool(&d.Omitempty, forField.Omitempty, operationDirective.Omitempty)
	fillDefaultBool(&d.Pointer, forField.Pointer, operationDirective.Pointer)
	// struct isn't settable via @octoqlgenFor.
	fillDefaultBool(&d.Struct, operationDirective.Struct)
	// flatten is not a default: it only applies where a selection is a single
	// fragment spread, and it is an error anywhere else, so propagating it
	// would reject the operations it was propagated into.
	fillDefaultString(&d.Bind, forField.Bind)
	// typename and alias name one generated construct, so they would collide
	// if they were defaults; neither is settable on the operation.
	fillDefaultString(&d.TypeName, forField.TypeName)
	fillDefaultString(&d.Alias, forField.Alias)
}

// The directives octoqlgen recognizes, one per scope an option can have.
const (
	octoqlgenDirectiveName = directive.Name
	octoqlgenDefaultsName  = directive.DefaultsName
	octoqlgenForName       = directive.ForName
)

// addOctoqlgenDirectiveDefinition injects octoqlgen's declarations into the
// parsed schema document.
//
// Any declaration already in the document is discarded first.  Schema
// directories octoqlgen writes carry a copy so editors can resolve the
// directives, and that copy may have been written by a different version of
// octoqlgen or edited by hand; the generator always uses its own.
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
		Name:  directive.FileName,
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
	for _, graphQLDirective := range ours {
		pos := graphQLDirective.Position
		switch graphQLDirective.Name {
		case octoqlgenForName:
			key, err := directive.addFor(graphQLDirective, pos)
			if err != nil {
				return err
			}
			err = g.recordForDeclaration(key, directive.FieldDirectives[key.typeName][key.fieldName], pos)
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

	g.directives[node] = directive
	return nil
}

// operationDefaultOptions are the options that only make sense as defaults for
// the fields inside an operation or fragment, so on an operation or fragment
// they belong to @octoqlgenDefaults rather than to @octoqlgen.
var operationDefaultOptions = map[string]bool{
	"omitempty": true,
	"pointer":   true,
	"struct":    true,
}

// checkNodeOptions rejects an @octoqlgen option that describes the fields
// inside an operation or fragment rather than the operation or fragment itself.
//
// The two scopes are separate directives, but @octoqlgen has one argument list
// across every location it is valid on, so GraphQL cannot make this
// distinction for us.
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

// recordForDeclaration remembers one @octoqlgenFor declaration and rejects it
// if another declaration already asked for something different.
//
// A named type generates one Go type, so two operations that disagree about a
// field of that type cannot both be satisfied.  Without this check the first
// one converted wins and the other silently does not get what it asked for,
// which also makes the generated code depend on the order of the operations.
//
// Declarations only have to agree with each other.  An operation that says
// nothing is not disagreeing, so adding an operation that happens to use the
// type does not force it to repeat the declaration.
func (g *generator) recordForDeclaration(
	key fieldKey,
	declared *octoqlgenDirective,
	pos *ast.Position,
) error {
	existing, ok := g.forDeclarations[key]
	if !ok {
		g.forDeclarations[key] = forDeclaration{directive: declared, pos: pos}
		return nil
	}
	if existing.pos == pos {
		// Repeated on one node; applyArguments already rejects conflicts there.
		return nil
	}
	if directiveOptionsMatch(existing.directive, declared) {
		return nil
	}
	return errorf(pos,
		"conflicting @%s declarations for %s: %s asks for something different; "+
			"a named type generates one Go type, so every @%s for it must agree",
		octoqlgenForName, key, posString(existing.pos), octoqlgenForName)
}

// forDeclarationFor returns the @octoqlgenFor declaration that applies to node,
// from any operation or fragment.
//
// The declaration names a type and a field, and that named type generates one
// Go type, so the declaration has to apply everywhere that field is generated
// rather than only inside the operation that wrote it.  Reading it from the
// containing operation instead would mean an operation that declares nothing
// generates a different type than one that does, and whichever converted first
// would decide which -- the order dependence @octoqlgenFor declarations are
// reconciled to prevent.
//
// recordForDeclaration has already rejected declarations that disagree, so any
// one of them is as good as another.
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
	return g.forDeclarations[key].directive
}

// forDeclaration is one @octoqlgenFor declaration, kept so that a later,
// conflicting one can point at it.
type forDeclaration struct {
	directive *octoqlgenDirective
	pos       *ast.Position
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
