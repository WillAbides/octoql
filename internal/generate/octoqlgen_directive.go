package generate

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/willabides/octoql/internal"
)

const (
	octoqlgenDirectiveName = "octoqlgen"
	octoqlgenDefaultsName  = "octoqlgenDefaults"
	octoqlgenForName       = "octoqlgenFor"
)

// defaultOptions holds field and variable defaults from @octoqlgenDefaults on
// an operation or fragment definition.
type defaultOptions struct {
	Omitempty *bool
	Pointer   *bool
	Struct    *bool
}

// operationOptions holds @octoqlgen and @octoqlgenDefaults options for an
// operation definition.
type operationOptions struct {
	pos      *ast.Position
	TypeName string
	Flatten  bool
	Defaults defaultOptions
}

// fragmentOptions holds @octoqlgen and @octoqlgenDefaults options for a named
// fragment definition.
type fragmentOptions struct {
	pos      *ast.Position
	Flatten  bool
	Defaults defaultOptions
}

// fieldOptions holds @octoqlgen or @octoqlgenFor options for a variable,
// selected field, or input-object field.
type fieldOptions struct {
	pos       *ast.Position
	Omitempty *bool
	Pointer   *bool
	Struct    *bool
	Flatten   *bool
	Bind      string
	TypeName  string
	Alias     string
}

func newFieldOptions(pos *ast.Position) *fieldOptions {
	return &fieldOptions{
		pos: pos,
	}
}

func (d *fieldOptions) GetOmitempty() bool   { return d.Omitempty != nil && *d.Omitempty }
func (d *fieldOptions) GetPointer() bool     { return d.Pointer != nil && *d.Pointer }
func (d *fieldOptions) PointerIsFalse() bool { return d.Pointer != nil && !*d.Pointer }
func (d *fieldOptions) GetStruct() bool      { return d.Struct != nil && *d.Struct }
func (d *fieldOptions) GetFlatten() bool     { return d.Flatten != nil && *d.Flatten }

type fieldKey struct {
	typeName  string
	fieldName string
}

func (k fieldKey) String() string { return k.typeName + "." + k.fieldName }

// directiveSet holds parsed, validated, and scope-separated octoqlgen
// directives stripped from a validated QueryDocument.
type directiveSet struct {
	operations map[*ast.OperationDefinition]operationOptions
	fragments  map[*ast.FragmentDefinition]fragmentOptions
	variables  map[*ast.VariableDefinition]*fieldOptions
	fields     map[*ast.Field]*fieldOptions
	forFields  map[fieldKey]*fieldOptions
}

func collectDirectives(doc *ast.QueryDocument, schema *ast.Schema) (*directiveSet, error) {
	set := &directiveSet{
		operations: make(map[*ast.OperationDefinition]operationOptions, len(doc.Operations)),
		fragments:  make(map[*ast.FragmentDefinition]fragmentOptions, len(doc.Fragments)),
		variables:  map[*ast.VariableDefinition]*fieldOptions{},
		fields:     map[*ast.Field]*fieldOptions{},
		forFields:  map[fieldKey]*fieldOptions{},
	}

	for _, op := range doc.Operations {
		err := set.collectOperation(op, schema)
		if err != nil {
			return nil, err
		}
		for _, variable := range op.VariableDefinitions {
			err = set.collectVariable(variable)
			if err != nil {
				return nil, err
			}
		}
		err = set.collectSelectionSet(op.SelectionSet, schema)
		if err != nil {
			return nil, err
		}
	}

	for _, fragment := range doc.Fragments {
		err := set.collectFragment(fragment, schema)
		if err != nil {
			return nil, err
		}
		err = set.collectSelectionSet(fragment.SelectionSet, schema)
		if err != nil {
			return nil, err
		}
	}

	return set, nil
}

func (s *directiveSet) forOperation(op *ast.OperationDefinition) operationOptions {
	opts, ok := s.operations[op]
	if ok {
		return opts
	}
	return operationOptions{
		pos: op.Position,
	}
}

func (s *directiveSet) forFragment(frag *ast.FragmentDefinition) fragmentOptions {
	opts, ok := s.fragments[frag]
	if ok {
		return opts
	}
	return fragmentOptions{
		pos: frag.Position,
	}
}

func (s *directiveSet) forVariable(
	variable *ast.VariableDefinition,
	defaults defaultOptions,
) *fieldOptions {
	base := s.variables[variable]
	merged := newFieldOptions(variable.Position)
	if base != nil {
		*merged = *base
	}
	fillDefaultBool(&merged.Omitempty, defaults.Omitempty)
	fillDefaultBool(&merged.Pointer, defaults.Pointer)
	return merged
}

func (s *directiveSet) forField(
	field *ast.Field,
	defaults defaultOptions,
) (*fieldOptions, error) {
	base := s.fields[field]
	merged := newFieldOptions(field.Position)
	if base != nil {
		*merged = *base
	}
	forField := s.forFields[fieldKey{
		typeName:  field.ObjectDefinition.Name,
		fieldName: field.Name,
	}]
	if forField != nil {
		fillDefaultBool(&merged.Pointer, forField.Pointer)
		fillDefaultString(&merged.Bind, forField.Bind)
		fillDefaultString(&merged.TypeName, forField.TypeName)
		fillDefaultString(&merged.Alias, forField.Alias)
	}
	fillDefaultBool(&merged.Pointer, defaults.Pointer)
	fillDefaultBool(&merged.Struct, defaults.Struct)

	if merged.TypeName != "" && merged.Bind != "" && merged.Bind != "-" {
		return nil, errorf(merged.pos, "typename and bind may not be used together")
	}
	return merged, nil
}

func (s *directiveSet) forInputField(
	def *ast.Definition,
	field *ast.FieldDefinition,
) (*fieldOptions, error) {
	merged := newFieldOptions(field.Position)
	forField := s.forFields[fieldKey{
		typeName:  def.Name,
		fieldName: field.Name,
	}]
	if forField != nil {
		merged.pos = forField.pos
		fillDefaultBool(&merged.Omitempty, forField.Omitempty)
		fillDefaultBool(&merged.Pointer, forField.Pointer)
		fillDefaultString(&merged.Bind, forField.Bind)
		fillDefaultString(&merged.TypeName, forField.TypeName)
	}
	if merged.TypeName != "" && merged.Bind != "" && merged.Bind != "-" {
		return nil, errorf(merged.pos, "typename and bind may not be used together")
	}
	return merged, nil
}

func stripOctoqlgenDirectives(directives *ast.DirectiveList) []*ast.Directive {
	var ours []*ast.Directive
	var others ast.DirectiveList
	for _, dir := range *directives {
		switch dir.Name {
		case octoqlgenDirectiveName, octoqlgenDefaultsName, octoqlgenForName:
			ours = append(ours, dir)
		default:
			others = append(others, dir)
		}
	}
	if len(ours) > 0 {
		*directives = others
	}
	return ours
}

func (s *directiveSet) collectOperation(op *ast.OperationDefinition, schema *ast.Schema) error {
	rawNode, defaults, err := s.collectDefinitionDirectives(&op.Directives, schema)
	if err != nil || rawNode == nil {
		return err
	}

	if rawNode.Bind != "" {
		return errorf(rawNode.pos, "bind may not be applied to the entire operation")
	}
	if rawNode.Alias != "" {
		return errorf(rawNode.pos, "alias is only applicable to selected fields, not operations")
	}

	s.operations[op] = operationOptions{
		pos:      rawNode.pos,
		TypeName: rawNode.TypeName,
		Flatten:  rawNode.GetFlatten(),
		Defaults: defaults,
	}
	return nil
}

func (s *directiveSet) collectFragment(frag *ast.FragmentDefinition, schema *ast.Schema) error {
	rawNode, defaults, err := s.collectDefinitionDirectives(&frag.Directives, schema)
	if err != nil || rawNode == nil {
		return err
	}

	if rawNode.Bind != "" {
		return errorf(rawNode.pos, "bind is not implemented for named fragments")
	}
	if rawNode.Alias != "" {
		return errorf(rawNode.pos, "alias is only applicable to selected fields, not fragment-definitions")
	}
	if rawNode.TypeName != "" {
		return errorf(rawNode.pos,
			"typename is not applicable to fragment-definitions; "+
				"the generated type is named after the fragment")
	}

	s.fragments[frag] = fragmentOptions{
		pos:      rawNode.pos,
		Flatten:  rawNode.GetFlatten(),
		Defaults: defaults,
	}
	return nil
}

func (s *directiveSet) collectDefinitionDirectives(
	directives *ast.DirectiveList,
	schema *ast.Schema,
) (*fieldOptions, defaultOptions, error) {
	ours := stripOctoqlgenDirectives(directives)
	if len(ours) == 0 {
		return nil, defaultOptions{}, nil
	}

	rawNode := newFieldOptions(ours[0].Position)
	var defaults defaultOptions
	forDirectives := map[fieldKey]*fieldOptions{}

	for _, dir := range ours {
		pos := dir.Position
		switch dir.Name {
		case octoqlgenForName:
			err := addFor(forDirectives, dir, pos)
			if err != nil {
				return nil, defaultOptions{}, err
			}
		case octoqlgenDefaultsName:
			err := applyDefaultsArguments(&defaults, dir, pos)
			if err != nil {
				return nil, defaultOptions{}, err
			}
		default:
			err := rejectDefaultArgsOnDefinition(dir, pos)
			if err != nil {
				return nil, defaultOptions{}, err
			}
			err = rawNode.applyArguments(dir, pos)
			if err != nil {
				return nil, defaultOptions{}, err
			}
		}
	}

	err := s.recordForDirectives(forDirectives, schema)
	if err != nil {
		return nil, defaultOptions{}, err
	}
	return rawNode, defaults, nil
}

func (s *directiveSet) collectVariable(variable *ast.VariableDefinition) error {
	ours := stripOctoqlgenDirectives(&variable.Directives)
	if len(ours) == 0 {
		return nil
	}

	opts := newFieldOptions(ours[0].Position)
	for _, dir := range ours {
		err := opts.applyArguments(dir, dir.Position)
		if err != nil {
			return err
		}
	}

	if opts.TypeName != "" && opts.Bind != "" && opts.Bind != "-" {
		return errorf(opts.pos, "typename and bind may not be used together")
	}
	if opts.GetOmitempty() && variable.Type.NonNull && variable.DefaultValue == nil {
		return errorf(opts.pos, "omitempty may only be used on optional arguments")
	}
	if opts.Struct != nil {
		return errorf(opts.pos, "struct is only applicable to fields, not variable-definitions")
	}
	if opts.Flatten != nil {
		return errorf(opts.pos, "flatten is only applicable to fields, not variable-definitions")
	}
	if opts.Alias != "" {
		return errorf(opts.pos,
			"alias is only applicable to selected fields, not variable-definitions; "+
				"rename the variable instead")
	}

	s.variables[variable] = opts
	return nil
}

func (s *directiveSet) collectSelectionSet(selectionSet ast.SelectionSet, schema *ast.Schema) error {
	for _, selection := range selectionSet {
		switch selection := selection.(type) {
		case *ast.Field:
			err := s.collectField(selection, schema)
			if err != nil {
				return err
			}
			err = s.collectSelectionSet(selection.SelectionSet, schema)
			if err != nil {
				return err
			}
		case *ast.InlineFragment:
			err := s.collectSelectionSet(selection.SelectionSet, schema)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *directiveSet) collectField(field *ast.Field, schema *ast.Schema) error {
	ours := stripOctoqlgenDirectives(&field.Directives)
	if len(ours) == 0 {
		return nil
	}

	opts := newFieldOptions(ours[0].Position)
	for _, dir := range ours {
		err := opts.applyArguments(dir, dir.Position)
		if err != nil {
			return err
		}
	}

	if opts.TypeName != "" && opts.Bind != "" && opts.Bind != "-" {
		return errorf(opts.pos, "typename and bind may not be used together")
	}
	if opts.Omitempty != nil {
		return errorf(opts.pos,
			"omitempty is only applicable to variables and input-type fields, "+
				"not to selected fields")
	}

	typ := schema.Types[field.Definition.Type.Name()]
	if opts.Struct != nil {
		err := validateStructOption(typ, field.SelectionSet, opts.pos)
		if err != nil {
			return err
		}
	}
	if opts.Flatten != nil {
		_, err := validateFlattenOption(typ, field.SelectionSet, opts.pos)
		if err != nil {
			return err
		}
	}

	s.fields[field] = opts
	return nil
}

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

func (d *fieldOptions) applyArguments(graphQLDirective *ast.Directive, pos *ast.Position) error {
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

func applyDefaultsArguments(defaults *defaultOptions, graphQLDirective *ast.Directive, pos *ast.Position) error {
	var err error
	for _, arg := range graphQLDirective.Arguments {
		switch arg.Name {
		case "omitempty":
			err = setBool("omitempty", &defaults.Omitempty, arg.Value, pos)
		case "pointer":
			err = setBool("pointer", &defaults.Pointer, arg.Value, pos)
		case "struct":
			err = setBool("struct", &defaults.Struct, arg.Value, pos)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

var operationDefaultOptions = map[string]bool{
	"omitempty": true,
	"pointer":   true,
	"struct":    true,
}

func rejectDefaultArgsOnDefinition(graphQLDirective *ast.Directive, pos *ast.Position) error {
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

func addFor(
	forDirectives map[fieldKey]*fieldOptions,
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
	key := fieldKey{
		typeName:  parts[0],
		fieldName: parts[1],
	}

	fieldDir := forDirectives[key]
	if fieldDir == nil {
		fieldDir = newFieldOptions(pos)
		forDirectives[key] = fieldDir
	}
	return fieldDir.applyArguments(graphQLDirective, pos)
}

func (d *fieldOptions) validateFor(key fieldKey, schema *ast.Schema) error {
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

func (s *directiveSet) recordForDirectives(
	forDirectives map[fieldKey]*fieldOptions,
	schema *ast.Schema,
) error {
	for key, fieldDirective := range forDirectives {
		err := fieldDirective.validateFor(key, schema)
		if err != nil {
			return err
		}
		existing, ok := s.forFields[key]
		if !ok {
			s.forFields[key] = fieldDirective
			continue
		}
		if forFieldOptionsMatch(existing, fieldDirective) {
			continue
		}
		return errorf(fieldDirective.pos,
			"conflicting @%s declarations for %s: %s asks for something different; "+
				"a named type generates one Go type, so every @%s for it must agree",
			octoqlgenForName, key, posString(existing.pos), octoqlgenForName)
	}
	return nil
}

func forFieldOptionsMatch(a, b *fieldOptions) bool {
	return boolOptionsMatch(a.Omitempty, b.Omitempty) &&
		boolOptionsMatch(a.Pointer, b.Pointer) &&
		a.Bind == b.Bind &&
		a.TypeName == b.TypeName &&
		a.Alias == b.Alias
}

func boolOptionsMatch(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func validateStructOption(
	typ *ast.Definition,
	selectionSet ast.SelectionSet,
	pos *ast.Position,
) error {
	if typ.Kind != ast.Interface && typ.Kind != ast.Union {
		return errorf(pos, "struct is only applicable to interface-typed fields")
	}

	for _, selection := range selectionSet {
		switch selection.(type) {
		case *ast.Field:
		case *ast.InlineFragment, *ast.FragmentSpread:
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
