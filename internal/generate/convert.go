package generate

// This file converts validated GraphQL operations, fragments, and input types
// into destination-neutral Go type definitions in planState.typeMap.

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"github.com/vektah/gqlparser/v2/ast"
)

type typeLookup struct {
	goName       string
	graphQLName  string
	newSource    string
	selectionSet ast.SelectionSet
	pos          *ast.Position
}

func (g *planState) getType(lookup *typeLookup) (goType, error) {
	typ, ok := g.typeMap[lookup.goName]
	if !ok {
		return nil, nil
	}

	if typ.GraphQLTypeName() != lookup.graphQLName {
		oldSource := describeTypeSource(typ)
		oldPos := g.typePositions[lookup.goName]
		return typ, errorf(
			lookup.pos, "conflicting definition for the Go type %s: it is generated "+
				"from both %s (at %s) and %s (at %s), which are different "+
				"GraphQL types (expected GraphQL type %s, got %s); give one of "+
				"them a distinct name with an @octoqlgen(typename:) directive so "+
				"they produce separate Go types",
			lookup.goName, oldSource, posString(oldPos), lookup.newSource, posString(lookup.pos),
			typ.GraphQLTypeName(), lookup.graphQLName,
		)
	}

	err := selectionsMatch(lookup.pos, lookup.selectionSet, typ.SelectionSet())
	if err != nil {
		oldSource := describeTypeSource(typ)
		oldPos := g.typePositions[lookup.goName]
		return typ, errorf(
			lookup.pos, "conflicting definition for the Go type %s: it is generated "+
				"from both %s (at %s) and %s (at %s), which select different "+
				"fields (%v); give one of them a distinct name with an "+
				"@octoqlgen(typename:) directive so they produce separate Go types",
			lookup.goName, oldSource, posString(oldPos), lookup.newSource, posString(lookup.pos), err,
		)
	}

	return typ, nil
}

func describeTypeSource(typ goType) string {
	graphQLName := typ.GraphQLTypeName()
	fragmentName := ""
	switch t := typ.(type) {
	case *goStructType:
		if t.IsInput {
			return fmt.Sprintf("input GraphQL type %s", graphQLName)
		}
		fragmentName = t.FragmentName
	case *goInterfaceType:
		fragmentName = t.FragmentName
	}
	return describeGraphQLSource(graphQLName, fragmentName)
}

func describeGraphQLSource(graphQLName, fragmentName string) string {
	if fragmentName != "" {
		return fmt.Sprintf("the fragment %s on GraphQL type %s", fragmentName, graphQLName)
	}
	return fmt.Sprintf("the selection of GraphQL type %s", graphQLName)
}

func posString(pos *ast.Position) string {
	if pos == nil || pos.Src == nil {
		return "an unknown location"
	}
	return fmt.Sprintf("%s:%d", pos.Src.Name, pos.Line)
}

func (g *planState) addType(typ goType, goName string, pos *ast.Position) (goType, error) {
	newSource := describeTypeSource(typ)
	otherTyp, err := g.getType(&typeLookup{
		goName:       goName,
		graphQLName:  typ.GraphQLTypeName(),
		newSource:    newSource,
		selectionSet: typ.SelectionSet(),
		pos:          pos,
	})
	if err != nil {
		return otherTyp, err
	}
	if otherTyp != nil {
		fieldErr := generatedTypeFieldsMatch(otherTyp, typ)
		if fieldErr != nil {
			return otherTyp, errorf(
				pos, "conflicting definition for the Go type %s: it is generated "+
					"from both %s (at %s) and %s (at %s), which select different "+
					"fields (%v); give one of them a distinct name with an "+
					"@octoqlgen(typename:) directive so they produce separate Go types",
				goName, describeTypeSource(otherTyp), posString(g.typePositions[goName]),
				newSource, posString(pos), fieldErr,
			)
		}
		return otherTyp, nil
	}
	g.typeMap[goName] = typ
	g.typePositions[goName] = pos
	return typ, nil
}

func checkGeneratedIdentifiers(typeMap map[string]goType, pkgPath string) error {
	scope := newImportScope(pkgPath, "")
	names := make([]string, 0, len(typeMap))
	for name := range typeMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch t := typeMap[name].(type) {
		case *goStructType:
			err := checkStructIdentifiers(t)
			if err != nil {
				return err
			}
		case *goInterfaceType:
			err := checkInterfaceIdentifiers(t, scope)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func checkStructIdentifiers(t *goStructType) error {
	type identifierSource struct {
		description string
		pos         *ast.Position
	}
	remedy := "disambiguate the conflicting selection with a field alias"
	if t.IsInput {
		remedy = "rename the variable or input field, or change the casing configuration"
	}
	used := make(map[string]identifierSource)
	register := func(name, description string, pos *ast.Position) error {
		if name == "" || name == "_" {
			return nil
		}
		existing, ok := used[name]
		if ok {
			errPos := pos
			if errPos == nil {
				errPos = existing.pos
			}
			return errorf(errPos,
				"generated type %s would emit the Go identifier %s for both %s and %s; %s",
				t.GoName, name, existing.description, description, remedy)
		}
		used[name] = identifierSource{
			description: description,
			pos:         pos,
		}
		return nil
	}

	for _, field := range t.Fields {
		if field.IsEmbedded() {
			embeddedName := field.GoType.Unwrap().QualifiedRef()
			err := register(embeddedName,
				fmt.Sprintf("embedded fragment %s", embeddedName), field.Position)
			if err != nil {
				return err
			}
			continue
		}
		err := register(field.GoName,
			fmt.Sprintf("field %s (GraphQL %s)", field.GoName, field.GraphQLName),
			field.Position)
		if err != nil {
			return err
		}
	}

	if t.NeedsMarshaling() {
		for _, method := range []string{"MarshalJSON", "UnmarshalJSON"} {
			err := register(method, method+" method", nil)
			if err != nil {
				return err
			}
		}
	}

	if t.IsInput {
		return nil
	}
	flattened, err := t.FlattenedFields()
	if err != nil {
		return err
	}
	for _, field := range flattened {
		getter := "Get" + field.GoName
		err = register(getter,
			fmt.Sprintf("getter %s (for field %s)", getter, field.GoName),
			field.Position)
		if err != nil {
			return err
		}
	}
	return nil
}

type interfaceMethod struct {
	name           string
	signature      string
	canonSignature string
	owner          string
	description    string
	pos            *ast.Position
}

var predeclaredTypeAliases = []struct {
	alias *regexp.Regexp
	canon string
}{
	{regexp.MustCompile(`\bbyte\b`), "uint8"},
	{regexp.MustCompile(`\brune\b`), "int32"},
	{regexp.MustCompile(`\bany\b`), "interface{}"},
}

func canonicalizeTypeRef(ref string) string {
	for _, a := range predeclaredTypeAliases {
		ref = a.alias.ReplaceAllString(ref, a.canon)
	}
	return ref
}

func interfaceMethodSet(
	t *goInterfaceType,
	scope *importScope,
	visited map[string]bool,
) ([]interfaceMethod, error) {
	if visited[t.GoName] {
		return nil, nil
	}
	visited[t.GoName] = true

	members, err := t.members(scope)
	if err != nil {
		return nil, err
	}

	var methods []interfaceMethod
	for _, member := range members {
		switch member.kind {
		case interfaceMarkerMember:
			methods = append(methods, interfaceMethod{
				name:           member.methodName,
				signature:      "()",
				canonSignature: "()",
				owner:          t.GoName,
				description:    fmt.Sprintf("the implements-marker method of interface %s", t.GoName),
			})
		case interfaceGetterMember:
			methods = append(methods, interfaceMethod{
				name:           member.methodName,
				signature:      "() " + member.resultRef,
				canonSignature: "() " + canonicalizeTypeRef(member.resultRef),
				owner:          t.GoName,
				description: fmt.Sprintf("getter %s (for field %s, GraphQL %s)",
					member.methodName, member.field.GoName, member.field.GraphQLName),
				pos: member.field.Position,
			})
		case interfaceEmbeddedMember:
			embedded, ok := member.field.GoType.Unwrap().(*goInterfaceType)
			if !ok {
				continue
			}
			promoted, promotedErr := interfaceMethodSet(embedded, scope, visited)
			if promotedErr != nil {
				return nil, promotedErr
			}
			methods = append(methods, promoted...)
		}
	}
	return methods, nil
}

func checkInterfaceIdentifiers(t *goInterfaceType, scope *importScope) error {
	methods, err := interfaceMethodSet(t, scope, map[string]bool{})
	if err != nil {
		return err
	}
	seen := make(map[string]interfaceMethod, len(methods))
	for _, method := range methods {
		existing, ok := seen[method.name]
		if !ok {
			seen[method.name] = method
			continue
		}
		sameOwner := existing.owner == method.owner
		if !sameOwner && existing.canonSignature == method.canonSignature {
			continue
		}
		errPos := method.pos
		if errPos == nil {
			errPos = existing.pos
		}
		return errorf(errPos,
			"generated interface %s would emit two methods named %s that do not "+
				"agree: %s%s from %s, and %s%s from %s; "+
				"disambiguate the conflicting selection with a field alias",
			t.GoName, method.name,
			method.name, existing.signature, existing.description,
			method.name, method.signature, method.description)
	}
	return nil
}

func (g *planState) baseTypeForOperation(operation ast.Operation) (*ast.Definition, error) {
	switch operation {
	case ast.Query:
		return g.schema.Query, nil
	case ast.Mutation:
		return g.schema.Mutation, nil
	default:
		return nil, errorf(nil, "unexpected operation: %v", operation)
	}
}

func (g *planState) convertOperation(
	operation *ast.OperationDefinition,
	opOptions operationOptions,
) (goType, error) {
	name := operation.Name + "Response"
	prefix := newNamePrefix(operation.Name)
	if opOptions.TypeName != "" {
		if goKeywords[opOptions.TypeName] {
			return nil, errorf(opOptions.pos, "typename option must not be a go keyword")
		}
		name = opOptions.TypeName
		prefix = newNamePrefix(opOptions.TypeName)
	}

	baseType, err := g.baseTypeForOperation(operation.Operation)
	if err != nil {
		return nil, errorf(operation.Position, "%v", err)
	}

	fields, err := g.convertSelectionSet(
		prefix,
		operation.SelectionSet,
		baseType,
		opOptions.Defaults,
	)
	if err != nil {
		return nil, err
	}

	if opOptions.Flatten {
		fieldIndex, flattenErr := validateFlattenOption(
			baseType, operation.SelectionSet, operation.Position,
		)
		if flattenErr != nil {
			return nil, flattenErr
		}
		return fields[fieldIndex].GoType, nil
	}

	goTyp := &goStructType{
		GoName: name,
		CommentOverride: fmt.Sprintf(
			"%v is returned by %v on success.", name, operation.Name,
		),
		GraphQLName: baseType.Name,
		Fields:      fields,
		Selection:   operation.SelectionSet,
	}

	return g.addType(goTyp, goTyp.GoName, operation.Position)
}

var builtinTypes = map[string]string{
	"Int":     "int",
	"Float":   "float64",
	"String":  "string",
	"Boolean": "bool",
	"ID":      "string",
}

var githubScalarTypes = map[string]string{
	"Base64String":        "string",
	"BigInt":              "string",
	"CustomPropertyValue": "encoding/json.RawMessage",
	"Date":                "string",
	"DateTime":            "time.Time",
	"GitObjectID":         "string",
	"GitRefname":          "string",
	"GitSSHRemote":        "string",
	"GitTimestamp":        "time.Time",
	"HTML":                "string",
	"PreciseDateTime":     "time.Time",
	"URI":                 "string",
	"X509Certificate":     "string",
}

func defaultScalarType(graphQLName string) (string, bool) {
	goType, ok := builtinTypes[graphQLName]
	if ok {
		return goType, true
	}
	goType, ok = githubScalarTypes[graphQLName]
	return goType, ok
}

type inputFieldSpec struct {
	ownerName          string
	fieldName          string
	kindLabel          string
	astType            *ast.Type
	hasDefault         bool
	description        string
	fieldPos           *ast.Position
	errPos             *ast.Position
	enforcePointerRule bool
	enforceOmitRule    bool
	options            *fieldOptions
}

func (g *planState) convertInputField(spec *inputFieldSpec) (*goStructField, error) {
	if spec.enforceOmitRule && spec.astType.NonNull && !spec.hasDefault && spec.options.GetOmitempty() {
		return nil, errorf(spec.errPos,
			"omitempty may only be used on optional arguments: %s.%s",
			spec.ownerName, spec.fieldName)
	}

	goTyp, structRefOmitempty, err := g.convertInputType(spec.astType, spec.options)
	if err != nil {
		return nil, err
	}

	omitempty := structRefOmitempty
	if spec.options.Omitempty != nil {
		omitempty = *spec.options.Omitempty
	}

	_, isPointer := goTyp.(*goPointerType)
	if spec.enforcePointerRule && spec.astType.NonNull && isPointer && !omitempty {
		return nil, errorf(spec.errPos,
			"pointer on non-null %s can only be used together with omitempty: %s.%s",
			spec.kindLabel, spec.ownerName, spec.fieldName)
	}

	goName := ApplyCasing(spec.fieldName, g.config.GetDefaultCasingAlgorithm(), true)
	return &goStructField{
		GoName:      goName,
		GoType:      goTyp,
		JSONName:    spec.fieldName,
		GraphQLName: spec.fieldName,
		Description: spec.description,
		Omitempty:   omitempty,
		Position:    spec.fieldPos,
	}, nil
}

func (g *planState) convertArguments(
	operation *ast.OperationDefinition,
	opOptions operationOptions,
) (*goStructType, error) {
	if len(operation.VariableDefinitions) == 0 {
		return nil, nil
	}
	name := operation.Name + "Variables"
	fields := make([]*goStructField, len(operation.VariableDefinitions))
	for i, arg := range operation.VariableDefinitions {
		if goKeywords[arg.Variable] {
			return nil, errorf(arg.Position, "variable name must not be a go keyword")
		}
		options := g.directives.forVariable(arg, opOptions.Defaults)
		field, err := g.convertInputField(&inputFieldSpec{
			ownerName:          operation.Name,
			fieldName:          arg.Variable,
			kindLabel:          "argument",
			astType:            arg.Type,
			hasDefault:         arg.DefaultValue != nil,
			fieldPos:           arg.Position,
			errPos:             arg.Position,
			enforcePointerRule: true,
			enforceOmitRule:    true,
			options:            options,
		})
		if err != nil {
			return nil, err
		}
		fields[i] = field
	}
	goTyp := &goStructType{
		GoName:  name,
		Fields:  fields,
		IsInput: true,
		CommentOverride: fmt.Sprintf(
			"%s contains the variables accepted by %s.", name, operation.Name,
		),
		GraphQLName: name,
	}
	added, err := g.addType(goTyp, goTyp.GoName, operation.Position)
	if err != nil {
		return nil, err
	}
	result, ok := added.(*goStructType)
	if !ok {
		return nil, errorf(operation.Position, "internal error: input type was %T", added)
	}
	return result, nil
}

func (g *planState) convertLocalBinding(
	typ *ast.Type,
	selectionSet ast.SelectionSet,
	options *fieldOptions,
) (goType, error) {
	err := rejectConditionalDirectivesInBoundSelection(typ.Name(), selectionSet)
	if err != nil {
		return nil, err
	}
	parsedRef, err := parseRef(options.Bind)
	if err != nil {
		return nil, err
	}
	goTyp := &goOpaqueType{
		Ref:         parsedRef,
		GraphQLName: typ.Name(),
	}
	return applyPointerSelection(goTyp, typ, options), nil
}

func (g *planState) convertInputType(
	typ *ast.Type,
	options *fieldOptions,
) (goType, bool, error) {
	if options.Bind != "" && options.Bind != "-" {
		bound, err := g.convertLocalBinding(typ, nil, options)
		return bound, false, err
	}
	if typ.Elem != nil {
		elem, defaultOmitempty, err := g.convertInputType(typ.Elem, options)
		if err != nil {
			return nil, false, err
		}
		return &goSliceType{Elem: elem}, defaultOmitempty, nil
	}

	def := g.schema.Types[typ.Name()]
	goTyp, isBound, err := g.convertInputDefinition(def, typ.Position, options)
	if err != nil {
		return nil, false, err
	}
	if !isBound && g.getStructReference(def) {
		if !options.PointerIsFalse() {
			goTyp = &goPointerType{Elem: goTyp}
		}
		return goTyp, true, nil
	}
	return applyPointerSelection(goTyp, typ, options), false, nil
}

type selectionTypeSpec struct {
	prefix       namePrefix
	typ          *ast.Type
	selectionSet ast.SelectionSet
	options      *fieldOptions
	defaults     defaultOptions
}

func (g *planState) convertSelectionType(spec *selectionTypeSpec) (goType, error) {
	if spec.options.Bind != "" && spec.options.Bind != "-" {
		return g.convertLocalBinding(spec.typ, spec.selectionSet, spec.options)
	}
	if spec.typ.Elem != nil {
		elem, err := g.convertSelectionType(&selectionTypeSpec{
			prefix:       spec.prefix,
			typ:          spec.typ.Elem,
			selectionSet: spec.selectionSet,
			options:      spec.options,
			defaults:     spec.defaults,
		})
		if err != nil {
			return nil, err
		}
		return &goSliceType{Elem: elem}, nil
	}

	def := g.schema.Types[spec.typ.Name()]
	goTyp, isBound, err := g.convertOutputDefinition(&outputDefinitionSpec{
		prefix:       spec.prefix,
		def:          def,
		pos:          spec.typ.Position,
		selectionSet: spec.selectionSet,
		options:      spec.options,
		defaults:     spec.defaults,
	})
	if err != nil {
		return nil, err
	}
	if !isBound && g.getStructReference(def) {
		if !spec.options.PointerIsFalse() {
			goTyp = &goPointerType{Elem: goTyp}
		}
		return goTyp, nil
	}
	return applyPointerSelection(goTyp, spec.typ, spec.options), nil
}

func applyPointerSelection(
	goTyp goType,
	typ *ast.Type,
	options *fieldOptions,
) goType {
	_, isInterface := goTyp.(*goInterfaceType)
	if isInterface {
		return goTyp
	}
	if options.PointerIsFalse() {
		return goTyp
	}
	if !options.GetPointer() && typ.NonNull {
		return goTyp
	}
	return &goPointerType{Elem: goTyp}
}

func (g *planState) getStructReference(def *ast.Definition) bool {
	return g.config.StructReferences &&
		(def.Kind == ast.Object || def.Kind == ast.InputObject)
}

func (g *planState) resolveConfiguredBinding(
	def *ast.Definition,
	pos *ast.Position,
	selectionSet ast.SelectionSet,
	options *fieldOptions,
) (*goOpaqueType, error) {
	globalBinding, ok := g.config.Bindings[def.Name]
	if ok && options.Bind != "-" {
		if options.TypeName != "" {
			return nil, errorf(options.pos,
				"typename option conflicts with global binding for %s; "+
					"use `bind: \"-\"` to override it", def.Name)
		}
		if def.Kind == ast.Object || def.Kind == ast.Interface || def.Kind == ast.Union {
			err := rejectConditionalDirectivesInBoundSelection(def.Name, selectionSet)
			if err != nil {
				return nil, err
			}
			err = validateBindingSelection(def.Name, globalBinding, pos, selectionSet)
			if err != nil {
				return nil, err
			}
		}
		parsedType, err := parseRef(globalBinding.Type)
		if err != nil {
			return nil, err
		}
		var marshalerRef goRef
		if globalBinding.Marshaler != "" {
			marshalerRef, err = parseRef(globalBinding.Marshaler)
			if err != nil {
				return nil, err
			}
		}
		var unmarshalerRef goRef
		if globalBinding.Unmarshaler != "" {
			unmarshalerRef, err = parseRef(globalBinding.Unmarshaler)
			if err != nil {
				return nil, err
			}
		}
		return &goOpaqueType{
			Ref:         parsedType,
			GraphQLName: def.Name,
			Marshaler:   marshalerRef,
			Unmarshaler: unmarshalerRef,
		}, nil
	}

	goBuiltinName, ok := defaultScalarType(def.Name)
	if ok && options.TypeName == "" {
		parsedBuiltin, err := parseRef(goBuiltinName)
		if err != nil {
			return nil, err
		}
		return &goOpaqueType{
			Ref:         parsedBuiltin,
			GraphQLName: def.Name,
		}, nil
	}
	return nil, nil
}

func provenancePosition(pos *ast.Position, options *fieldOptions) *ast.Position {
	if options != nil && options.pos != nil {
		return options.pos
	}
	return pos
}

func (g *planState) convertScalarOrEnumDefinition(
	name string,
	def *ast.Definition,
	pos, provenancePos *ast.Position,
) (goType, error) {
	existing, err := g.getType(&typeLookup{
		goName:      name,
		graphQLName: def.Name,
		newSource:   describeGraphQLSource(def.Name, ""),
		pos:         provenancePos,
	})
	if existing != nil || err != nil {
		return existing, err
	}

	switch def.Kind {
	case ast.Enum:
		goTyp := &goEnumType{
			GoName:      name,
			GraphQLName: def.Name,
			Description: def.Description,
			Values:      make([]goEnumValue, len(def.EnumValues)),
		}

		goNames := make(map[string]*goEnumValue, len(def.EnumValues))
		for i, val := range def.EnumValues {
			goName := g.config.Casing.enumValueName(name, def, val)
			conflict := goNames[goName]
			if conflict != nil {
				return nil, errorf(val.Position,
					"enum values %s and %s have conflicting Go name %s; "+
						"add 'all_enums: raw' or 'enums: %v: raw' "+
						"to 'casing' in octoqlgen.yaml to fix",
					val.Name, conflict.GraphQLName, goName, def.Name)
			}

			goTyp.Values[i] = goEnumValue{
				GoName:      goName,
				GraphQLName: val.Name,
				Description: val.Description,
			}
			goNames[goName] = &goTyp.Values[i]
		}
		return g.addType(goTyp, goTyp.GoName, provenancePos)

	case ast.Scalar:
		if builtinTypes[def.Name] != "" {
			goTyp := &goTypenameForBuiltinType{
				GoTypeName:    name,
				GoBuiltinName: builtinTypes[def.Name],
				GraphQLName:   def.Name,
			}
			return g.addType(goTyp, goTyp.GoTypeName, provenancePos)
		}
		return nil, errorf(
			pos, "unknown scalar %v: please add it to \"bindings\" in octoqlgen.yaml"+
				"\nExample: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml", def.Name,
		)

	default:
		return nil, errorf(pos, "unexpected kind: %v", def.Kind)
	}
}

func (g *planState) convertInputDefinition(
	def *ast.Definition,
	pos *ast.Position,
	options *fieldOptions,
) (goType, bool, error) {
	bound, err := g.resolveConfiguredBinding(def, pos, nil, options)
	if bound != nil || err != nil {
		return bound, bound != nil, err
	}

	name := ApplyCasing(def.Name, g.config.GetDefaultCasingAlgorithm(), true)
	if options.TypeName != "" {
		if goKeywords[options.TypeName] {
			return nil, false, errorf(pos, "typename option must not be a go keyword")
		}
		name = options.TypeName
	}

	provPos := provenancePosition(pos, options)
	if def.Kind != ast.InputObject {
		typ, scalarErr := g.convertScalarOrEnumDefinition(name, def, pos, provPos)
		return typ, false, scalarErr
	}

	existing, err := g.getType(&typeLookup{
		goName:      name,
		graphQLName: def.Name,
		newSource:   describeGraphQLSource(def.Name, ""),
		pos:         provPos,
	})
	if existing != nil || err != nil {
		return existing, false, err
	}

	goTyp := &goStructType{
		GoName:             name,
		Fields:             make([]*goStructField, len(def.Fields)),
		GraphQLDescription: def.Description,
		GraphQLName:        def.Name,
		IsInput:            true,
	}
	_, err = g.addType(goTyp, goTyp.GoName, provPos)
	if err != nil {
		return nil, false, err
	}

	enforceRules := !g.config.StructReferences
	for i, field := range def.Fields {
		fieldOptions, dirErr := g.directives.forInputField(def, field)
		if dirErr != nil {
			return nil, false, dirErr
		}
		convertedField, fieldErr := g.convertInputField(&inputFieldSpec{
			ownerName:          name,
			fieldName:          field.Name,
			kindLabel:          "input field",
			astType:            field.Type,
			hasDefault:         field.DefaultValue != nil,
			description:        field.Description,
			fieldPos:           field.Position,
			errPos:             pos,
			enforcePointerRule: enforceRules,
			enforceOmitRule:    enforceRules,
			options:            fieldOptions,
		})
		if fieldErr != nil {
			return nil, false, fieldErr
		}
		goTyp.Fields[i] = convertedField
	}
	return goTyp, false, nil
}

type abstractSelectionSpec struct {
	goName        string
	prefix        namePrefix
	def           *ast.Definition
	selectionSet  ast.SelectionSet
	sharedFields  []*goStructField
	defaults      defaultOptions
	desc          descriptionInfo
	provenancePos *ast.Position
	pos           *ast.Position
	implGoName    func(*ast.Definition) string
}

func (g *planState) buildAbstractSelectionType(spec *abstractSelectionSpec) (goType, error) {
	implementationTypes := g.schema.GetPossibleTypes(spec.def)
	sort.Slice(implementationTypes, func(i, j int) bool {
		return implementationTypes[i].Name < implementationTypes[j].Name
	})
	implementationTypes = g.filterReferencedImplementations(implementationTypes, spec.selectionSet)

	goTyp := &goInterfaceType{
		GoName:          spec.goName,
		SharedFields:    spec.sharedFields,
		Selection:       spec.selectionSet,
		descriptionInfo: spec.desc,
	}

	result, addErr := g.addType(goTyp, goTyp.GoName, spec.provenancePos)
	if addErr != nil {
		return nil, addErr
	}

	for _, implDef := range implementationTypes {
		implFields, convertErr := g.convertSelectionSet(
			spec.prefix, spec.selectionSet, implDef, spec.defaults,
		)
		if convertErr != nil {
			return nil, convertErr
		}

		implDesc := spec.desc
		implDesc.GraphQLName = implDef.Name
		if spec.desc.FragmentName == "" {
			implDesc.GraphQLDescription = implDef.Description
		}

		implTyp := &goStructType{
			GoName:          spec.implGoName(implDef),
			Fields:          implFields,
			Selection:       spec.selectionSet,
			descriptionInfo: implDesc,
		}
		addedImpl, implErr := g.addType(implTyp, implTyp.GoName, spec.provenancePos)
		if implErr != nil {
			return nil, implErr
		}
		implStruct, ok := addedImpl.(*goStructType)
		if !ok {
			return nil, errorf(spec.pos, "interface %s had non-object implementation %s",
				spec.def.Name, implDef.Name)
		}
		goTyp.Implementations = append(goTyp.Implementations, implStruct)
	}

	if result != goTyp {
		return result, nil
	}

	err := g.attachCatchAllImplementation(goTyp, spec.def.Name, spec.pos)
	if err != nil {
		return nil, err
	}
	return goTyp, nil
}

type outputDefinitionSpec struct {
	prefix       namePrefix
	def          *ast.Definition
	pos          *ast.Position
	selectionSet ast.SelectionSet
	options      *fieldOptions
	defaults     defaultOptions
}

func (g *planState) convertOutputDefinition(spec *outputDefinitionSpec) (goType, bool, error) {
	prefix := spec.prefix
	def := spec.def
	pos := spec.pos
	selectionSet := spec.selectionSet
	options := spec.options
	defaults := spec.defaults
	bound, err := g.resolveConfiguredBinding(def, pos, selectionSet, options)
	if bound != nil || err != nil {
		return bound, bound != nil, err
	}

	casing := g.config.GetDefaultCasingAlgorithm()
	var name string
	switch {
	case options.TypeName != "":
		if goKeywords[options.TypeName] {
			return nil, false, errorf(pos, "typename option must not be a go keyword")
		}
		name = options.TypeName
		if prefix.isSingle(name) {
			name = makeLongTypeName(prefix, def.Name, casing)
		}
		prefix = newNamePrefix(options.TypeName)
	case def.Kind == ast.Enum:
		name = ApplyCasing(def.Name, casing, true)
	default:
		name = makeTypeName(prefix, def.Name, casing)
	}

	provPos := provenancePosition(pos, options)
	if def.Kind == ast.Enum || def.Kind == ast.Scalar {
		typ, scalarErr := g.convertScalarOrEnumDefinition(name, def, pos, provPos)
		return typ, false, scalarErr
	}

	desc := descriptionInfo{
		GraphQLDescription: def.Description,
		GraphQLName:        def.Name,
	}

	kind := def.Kind
	if options.GetStruct() && validateStructOption(def, selectionSet, pos) == nil {
		kind = ast.Object
	}

	fields, err := g.convertSelectionSet(prefix, selectionSet, def, defaults)
	if err != nil {
		return nil, false, err
	}
	if options.GetFlatten() {
		fieldIndex, flattenErr := validateFlattenOption(def, selectionSet, pos)
		if flattenErr != nil {
			return nil, false, flattenErr
		}
		return fields[fieldIndex].GoType, false, nil
	}

	switch kind {
	case ast.Object:
		goTyp := &goStructType{
			GoName:          name,
			Fields:          fields,
			Selection:       selectionSet,
			descriptionInfo: desc,
		}
		added, addErr := g.addType(goTyp, goTyp.GoName, provPos)
		return added, false, addErr

	case ast.Interface, ast.Union:
		explicitTypeName := options.TypeName
		typ, absErr := g.buildAbstractSelectionType(&abstractSelectionSpec{
			goName:        name,
			prefix:        prefix,
			def:           def,
			selectionSet:  selectionSet,
			sharedFields:  fields,
			defaults:      defaults,
			desc:          desc,
			provenancePos: provPos,
			pos:           pos,
			implGoName: func(implDef *ast.Definition) string {
				if explicitTypeName != "" && prefix.isSingle(explicitTypeName) {
					return makeLongTypeName(prefix, implDef.Name, casing)
				}
				return makeTypeName(prefix, implDef.Name, casing)
			},
		})
		return typ, false, absErr

	default:
		return nil, false, errorf(pos, "unexpected kind: %v", def.Kind)
	}
}

func (g *planState) filterReferencedImplementations(
	implementations []*ast.Definition,
	selectionSet ast.SelectionSet,
) []*ast.Definition {
	if !g.config.omitUnreferencedImplementations() {
		return implementations
	}

	applicable := make(map[string]bool, len(implementations))
	for _, implementation := range implementations {
		applicable[implementation.Name] = true
	}

	type selectionContext struct {
		selectionSet ast.SelectionSet
		applicable   map[string]bool
	}

	referenced := map[string]bool{}
	queue := []selectionContext{{
		selectionSet: selectionSet,
		applicable:   applicable,
	}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, selection := range current.selectionSet {
			var typeCondition string
			var fragmentSelectionSet ast.SelectionSet
			switch selection := selection.(type) {
			case *ast.InlineFragment:
				typeCondition = selection.TypeCondition
				fragmentSelectionSet = selection.SelectionSet
			case *ast.FragmentSpread:
				if selection.Definition == nil {
					continue
				}
				typeCondition = selection.Definition.TypeCondition
				fragmentSelectionSet = selection.Definition.SelectionSet
			default:
				continue
			}

			if typeCondition == "" {
				if selectionSetHasDirectFields(fragmentSelectionSet) {
					for concreteType := range current.applicable {
						referenced[concreteType] = true
					}
				}
				queue = append(queue, selectionContext{
					selectionSet: fragmentSelectionSet,
					applicable:   current.applicable,
				})
				continue
			}

			definition := g.schema.Types[typeCondition]
			conditionTypes := g.possibleObjectTypes(definition)
			fragmentApplicable := map[string]bool{}
			for concreteType := range conditionTypes {
				if current.applicable[concreteType] {
					fragmentApplicable[concreteType] = true
				}
			}
			if len(fragmentApplicable) == 0 {
				continue
			}
			referencesApplicableTypes := definition.Kind == ast.Object ||
				selectionSetHasDirectFields(fragmentSelectionSet)
			if referencesApplicableTypes {
				for concreteType := range fragmentApplicable {
					referenced[concreteType] = true
				}
			}
			queue = append(queue, selectionContext{
				selectionSet: fragmentSelectionSet,
				applicable:   fragmentApplicable,
			})
		}
	}

	return slices.DeleteFunc(slices.Clone(implementations), func(implementation *ast.Definition) bool {
		return !referenced[implementation.Name]
	})
}

func selectionSetHasDirectFields(selectionSet ast.SelectionSet) bool {
	for _, selection := range selectionSet {
		field, ok := selection.(*ast.Field)
		if ok && field.Name != "__typename" {
			return true
		}
	}
	return false
}

func (g *planState) possibleObjectTypes(definition *ast.Definition) map[string]bool {
	if definition == nil {
		return map[string]bool{}
	}
	if definition.Kind == ast.Object {
		return map[string]bool{definition.Name: true}
	}
	if definition.Kind != ast.Interface && definition.Kind != ast.Union {
		return map[string]bool{}
	}

	possibleTypes := g.schema.GetPossibleTypes(definition)
	result := make(map[string]bool, len(possibleTypes))
	for _, possibleType := range possibleTypes {
		result[possibleType.Name] = true
	}
	return result
}

func (g *planState) attachCatchAllImplementation(
	abstractType *goInterfaceType,
	graphQLName string,
	pos *ast.Position,
) error {
	if !g.config.omitUnreferencedImplementations() {
		return nil
	}

	fields := make([]*goStructField, 0, len(abstractType.SharedFields))
	for _, field := range abstractType.SharedFields {
		if field.GoName != "" {
			fields = append(fields, field)
			continue
		}

		embedded, ok := field.GoType.(*goInterfaceType)
		if !ok {
			fields = append(fields, field)
			continue
		}

		replacement := *field
		replacement.GoType = embedded.OtherImplementation
		fields = append(fields, &replacement)
	}

	catchAll := &goStructType{
		GoName:      g.catchAllName(abstractType.GoName),
		Fields:      fields,
		Selection:   abstractType.Selection,
		GraphQLName: graphQLName,
		CommentOverride: fmt.Sprintf(
			"%sOctoqlOther represents %s implementations not explicitly selected by a fragment. "+
				"Use GetTypename to identify the concrete GraphQL type.",
			abstractType.GoName,
			abstractType.GoName,
		),
	}
	_, err := g.addType(catchAll, catchAll.GoName, pos)
	if err != nil {
		return err
	}
	abstractType.OtherImplementation = catchAll
	return nil
}

func (g *planState) catchAllName(abstractTypeName string) string {
	name := abstractTypeName + "OctoqlOther"
	for suffix := 2; g.typeMap[name] != nil; suffix++ {
		name = fmt.Sprintf("%s%dOctoqlOther", abstractTypeName, suffix)
	}
	return name
}

func (g *planState) convertSelectionSet(
	prefix namePrefix,
	selectionSet ast.SelectionSet,
	containingTypedef *ast.Definition,
	defaults defaultOptions,
) ([]*goStructField, error) {
	fields := make([]*goStructField, 0, len(selectionSet))
	for _, selection := range selectionSet {
		switch sel := selection.(type) {
		case *ast.Field:
			selectionOptions, err := g.directives.forField(sel, defaults)
			if err != nil {
				return nil, err
			}
			field, err := g.convertField(prefix, sel, selectionOptions, defaults)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
		case *ast.FragmentSpread:
			if hasConditionalDirective(sel.Directives) {
				return nil, errorf(sel.Position,
					"@skip and @include are not supported on fragment spreads "+
						"(...%s): octoqlgen cannot represent the absence of "+
						"every field the fragment contributes, so it would "+
						"otherwise silently generate value types for fields "+
						"that can vanish",
					sel.Name)
			}
			maybeField, err := g.convertFragmentSpread(sel, containingTypedef)
			if err != nil {
				return nil, err
			}
			if maybeField != nil {
				fields = append(fields, maybeField)
			}
		case *ast.InlineFragment:
			if hasConditionalDirective(sel.Directives) {
				return nil, errorf(sel.Position,
					"@skip and @include are not supported on inline fragments: "+
						"octoqlgen cannot represent the absence of every field "+
						"the fragment contributes, so it would otherwise "+
						"silently generate value types for fields that can vanish")
			}
			fragmentFields, err := g.convertInlineFragment(
				prefix, sel, containingTypedef, defaults,
			)
			if err != nil {
				return nil, err
			}
			fields = append(fields, fragmentFields...)
		default:
			return nil, errorf(nil, "invalid selection type: %T", sel)
		}
	}

	uniqFields := make([]*goStructField, 0, len(selectionSet))
	fragmentNames := make(map[string]bool, len(selectionSet))
	fieldNames := make(map[string]bool, len(selectionSet))
	for _, field := range fields {
		if field.JSONName == "" {
			name := field.GoType.QualifiedRef()
			if fragmentNames[name] {
				continue
			}
			uniqFields = append(uniqFields, field)
			fragmentNames[name] = true
			continue
		}

		if fieldNames[field.JSONName] {
			switch field.GoType.Unwrap().(type) {
			case *goOpaqueType, *goEnumType:
				continue
			case *goStructType, *goInterfaceType:
				return nil, errorf(nil,
					"octoqlgen doesn't allow duplicate fields with different selections "+
						"(see https://github.com/Khan/genqlient/issues/64); "+
						"duplicate field: %s.%s", containingTypedef.Name, field.JSONName)
			default:
				return nil, errorf(nil, "unexpected field-type: %T", field.GoType.Unwrap())
			}
		}
		uniqFields = append(uniqFields, field)
		fieldNames[field.JSONName] = true
	}
	return uniqFields, nil
}

func fragmentMatches(containingTypedef, fragmentTypedef *ast.Definition) bool {
	if containingTypedef.Name == fragmentTypedef.Name {
		return true
	}
	if slices.Contains(containingTypedef.Interfaces, fragmentTypedef.Name) {
		return true
	}
	if fragmentTypedef.Kind == ast.Union {
		return slices.Contains(fragmentTypedef.Types, containingTypedef.Name)
	}
	return false
}

func (g *planState) convertInlineFragment(
	prefix namePrefix,
	fragment *ast.InlineFragment,
	containingTypedef *ast.Definition,
	defaults defaultOptions,
) ([]*goStructField, error) {
	fragmentTypedef := g.schema.Types[fragment.TypeCondition]
	if !fragmentMatches(containingTypedef, fragmentTypedef) {
		return nil, nil
	}
	return g.convertSelectionSet(prefix, fragment.SelectionSet, containingTypedef, defaults)
}

func (g *planState) convertFragmentSpread(
	fragmentSpread *ast.FragmentSpread,
	containingTypedef *ast.Definition,
) (*goStructField, error) {
	if !fragmentMatches(containingTypedef, fragmentSpread.Definition.Definition) {
		return nil, nil
	}

	typ, err := g.convertNamedFragment(fragmentSpread.Definition)
	if err != nil {
		return nil, err
	}

	iface, ok := typ.(*goInterfaceType)
	if ok && containingTypedef.Kind == ast.Object {
		for _, impl := range iface.Implementations {
			if impl.GraphQLName == containingTypedef.Name {
				typ = impl
				break
			}
		}
		if typ == iface && iface.OtherImplementation != nil {
			typ = iface.OtherImplementation
		}
	}

	return &goStructField{
		GoName:   "",
		GoType:   typ,
		Position: fragmentSpread.Position,
	}, nil
}

func (g *planState) convertNamedFragment(fragment *ast.FragmentDefinition) (goType, error) {
	typ := g.schema.Types[fragment.TypeCondition]
	fragOptions := g.directives.forFragment(fragment)
	comment := precedingComment(fragment.Position)

	desc := descriptionInfo{
		CommentOverride:    comment,
		GraphQLName:        typ.Name,
		GraphQLDescription: typ.Description,
		FragmentName:       fragment.Name,
	}

	prefix := newNamePrefix(fragment.Name)
	fields, err := g.convertSelectionSet(prefix, fragment.SelectionSet, typ, fragOptions.Defaults)
	if err != nil {
		return nil, err
	}
	if fragOptions.Flatten {
		fieldIndex, flattenErr := validateFlattenOption(typ, fragment.SelectionSet, fragment.Position)
		if flattenErr != nil {
			return nil, flattenErr
		}
		return fields[fieldIndex].GoType, nil
	}

	switch typ.Kind {
	case ast.Object:
		goTyp := &goStructType{
			GoName:          fragment.Name,
			Fields:          fields,
			Selection:       fragment.SelectionSet,
			descriptionInfo: desc,
		}
		return g.addType(goTyp, goTyp.GoName, fragment.Position)

	case ast.Interface, ast.Union:
		return g.buildAbstractSelectionType(&abstractSelectionSpec{
			goName:        fragment.Name,
			prefix:        prefix,
			def:           typ,
			selectionSet:  fragment.SelectionSet,
			sharedFields:  fields,
			defaults:      fragOptions.Defaults,
			desc:          desc,
			provenancePos: fragment.Position,
			pos:           fragment.Position,
			implGoName: func(implDef *ast.Definition) string {
				return fragment.Name + upperFirst(implDef.Name)
			},
		})

	default:
		return nil, errorf(fragment.Position, "invalid type for fragment: %v is a %v",
			fragment.TypeCondition, typ.Kind)
	}
}

func (g *planState) convertField(
	prefix namePrefix,
	field *ast.Field,
	fieldOptions *fieldOptions,
	defaults defaultOptions,
) (*goStructField, error) {
	if field.Definition == nil {
		return nil, errorf(field.Position, "undefined field %v", field.Alias)
	}

	goName := field.Alias
	if fieldOptions.Alias != "" {
		goName = fieldOptions.Alias
	}
	casing := g.config.GetDefaultCasingAlgorithm()
	goName = ApplyCasing(goName, casing, true)
	prefix = nextPrefix(prefix, field, casing)

	fieldGoType, err := g.convertSelectionType(&selectionTypeSpec{
		prefix:       prefix,
		typ:          field.Definition.Type,
		selectionSet: field.SelectionSet,
		options:      fieldOptions,
		defaults:     defaults,
	})
	if err != nil {
		return nil, err
	}

	if hasConditionalDirective(field.Directives) {
		if fieldOptions.PointerIsFalse() {
			return nil, errorf(field.Position,
				"field %s carries @skip or @include and so may be absent from "+
					"the response; it cannot be combined with "+
					"@octoqlgen(pointer: false), which asks for a value type "+
					"that cannot represent absence",
				field.Name)
		}
		fieldGoType = forceConditionalPointer(fieldGoType)
	}

	return &goStructField{
		GoName:      goName,
		GoType:      fieldGoType,
		JSONName:    field.Alias,
		GraphQLName: field.Name,
		Description: field.Definition.Description,
		Position:    field.Position,
	}, nil
}

func hasConditionalDirective(directives ast.DirectiveList) bool {
	return directives.ForName("skip") != nil ||
		directives.ForName("include") != nil
}

func forceConditionalPointer(goTyp goType) goType {
	switch t := goTyp.(type) {
	case *goPointerType, *goInterfaceType, *goSliceType:
		return goTyp
	case *goOpaqueType:
		if t.Ref.isNilable() {
			return goTyp
		}
	}
	return &goPointerType{Elem: goTyp}
}

func rejectConditionalDirectivesInBoundSelection(
	bindingName string,
	selectionSet ast.SelectionSet,
) error {
	return rejectConditionalDirectivesInSelection(
		bindingName, selectionSet, map[string]bool{},
	)
}

func rejectConditionalDirectivesInSelection(
	bindingName string,
	selectionSet ast.SelectionSet,
	seenFragments map[string]bool,
) error {
	for _, selection := range selectionSet {
		var directives ast.DirectiveList
		var nested ast.SelectionSet
		switch selection := selection.(type) {
		case *ast.Field:
			directives = selection.Directives
			nested = selection.SelectionSet
		case *ast.InlineFragment:
			directives = selection.Directives
			nested = selection.SelectionSet
		case *ast.FragmentSpread:
			directives = selection.Directives
			if selection.Definition != nil && !seenFragments[selection.Name] {
				seenFragments[selection.Name] = true
				nested = selection.Definition.SelectionSet
			}
		}
		if hasConditionalDirective(directives) {
			return errorf(selection.GetPosition(),
				"@skip and @include are not supported inside a selection bound "+
					"to a Go type (%s): the bound type's selection set is not "+
					"generated, so octoqlgen cannot represent the absence of a "+
					"conditionally-skipped field and would silently decode it "+
					"to the Go zero value",
				bindingName)
		}
		err := rejectConditionalDirectivesInSelection(
			bindingName, nested, seenFragments,
		)
		if err != nil {
			return err
		}
	}
	return nil
}
