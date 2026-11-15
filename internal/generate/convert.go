package generate

// This file converts validated GraphQL operations, fragments, and input types
// into Go type definitions in planState.typeMap.

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

func (g *planState) getType(
	goName string,
	graphQLName string,
	newSource string,
	pos *ast.Position,
) (goType, error) {
	typ, ok := g.typeMap[goName]
	if !ok {
		return nil, nil
	}

	if typ.GraphQLTypeName() != graphQLName {
		oldSource := describeTypeSource(typ)
		oldPos := g.typePositions[goName]
		return typ, errorf(
			pos, "conflicting definition for the Go type %s: it is generated "+
				"from both %s (at %s) and %s (at %s), which are different "+
				"GraphQL types (expected GraphQL type %s, got %s); give one of "+
				"them a distinct name with an @octoqlgen(typename:) directive so "+
				"they produce separate Go types",
			goName, oldSource, posString(oldPos), newSource, posString(pos),
			typ.GraphQLTypeName(), graphQLName,
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
	otherTyp, err := g.getType(goName, typ.GraphQLTypeName(), newSource, pos)
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

func markInterfaceGetters(typeMap map[string]goType) {
	for _, typ := range typeMap {
		iface, ok := typ.(*goInterfaceType)
		if !ok {
			continue
		}
		getters := interfaceGetterFields(iface, map[string]bool{})
		for _, impl := range iface.Implementations {
			impl.addInterfaceGetters(getters)
		}
		if iface.OtherImplementation != nil {
			iface.OtherImplementation.addInterfaceGetters(getters)
		}
	}

	visited := make(map[string]bool, len(typeMap))
	for _, typ := range typeMap {
		structTyp, ok := typ.(*goStructType)
		if !ok {
			continue
		}
		propagateEmbeddedInterfaceGetters(structTyp, visited)
	}
}

func propagateEmbeddedInterfaceGetters(t *goStructType, visited map[string]bool) {
	if visited[t.GoName] {
		return
	}
	visited[t.GoName] = true

	for _, field := range t.Fields {
		if !field.IsEmbedded() {
			continue
		}
		embedded, ok := field.GoType.Unwrap().(*goStructType)
		if !ok {
			continue
		}
		propagateEmbeddedInterfaceGetters(embedded, visited)
		for getter := range embedded.InterfaceGetters {
			t.addInterfaceGetters([]string{getter})
		}
	}
}

func interfaceGetterFields(t *goInterfaceType, visited map[string]bool) []string {
	if visited[t.GoName] {
		return nil
	}
	visited[t.GoName] = true

	var fields []string
	for _, field := range t.SharedFields {
		if field.IsEmbedded() {
			embedded, ok := field.GoType.Unwrap().(*goInterfaceType)
			if !ok {
				continue
			}
			fields = append(fields, interfaceGetterFields(embedded, visited)...)
			continue
		}
		fields = append(fields, field.GoName)
	}
	return fields
}

func checkGeneratedIdentifiers(typeMap map[string]goType, pkgPath string) error {
	markInterfaceGetters(typeMap)
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
			err := checkInterfaceIdentifiers(t, pkgPath)
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
		remedy = "rename the variable or input field"
	}
	used := make(map[string]identifierSource)
	register := func(name, description string, pos *ast.Position) error {
		if name == "" || name == "_" {
			return nil
		}
		existing, ok := used[name]
		if ok {
			return errorf(cmp.Or(pos, existing.pos),
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
		for _, method := range []string{
			"MarshalJSON",
			"MarshalJSONTo",
			"UnmarshalJSON",
			"UnmarshalJSONFrom",
		} {
			err := register(method, method+" method", nil)
			if err != nil {
				return err
			}
		}
	}

	if t.IsInput || len(t.InterfaceGetters) == 0 {
		return nil
	}
	flattened, err := t.FlattenedFields()
	if err != nil {
		return err
	}
	for _, field := range flattened {
		if !t.InterfaceGetters[field.GoName] {
			continue
		}
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

func canonicalizeTypeRef(ref, pkgPath string) string {
	start := strings.LastIndexAny(ref, "*]") + 1
	name := ref[start:]
	if pkgPath != "" {
		name = strings.TrimPrefix(name, pkgPath+".")
	}
	switch name {
	case "byte":
		name = "uint8"
	case "rune":
		name = "int32"
	}
	return ref[:start] + name
}

func interfaceMethodSet(
	t *goInterfaceType,
	pkgPath string,
	visited map[string]bool,
) []interfaceMethod {
	if visited[t.GoName] {
		return nil
	}
	visited[t.GoName] = true

	methods := []interfaceMethod{{
		name:           t.markerMethodName(),
		signature:      "()",
		canonSignature: "()",
		owner:          t.GoName,
		description:    fmt.Sprintf("the implements-marker method of interface %s", t.GoName),
	}}
	for _, field := range t.SharedFields {
		if field.IsEmbedded() {
			embedded, ok := field.GoType.Unwrap().(*goInterfaceType)
			if !ok {
				continue
			}
			methods = append(methods, interfaceMethodSet(embedded, pkgPath, visited)...)
			continue
		}
		methodName := "Get" + field.GoName
		ref := field.GoType.QualifiedRef()
		methods = append(methods, interfaceMethod{
			name:           methodName,
			signature:      "() " + ref,
			canonSignature: "() " + canonicalizeTypeRef(ref, pkgPath),
			owner:          t.GoName,
			description: fmt.Sprintf("getter %s (for field %s, GraphQL %s)",
				methodName, field.GoName, field.GraphQLName),
			pos: field.Position,
		})
	}
	return methods
}

func checkInterfaceIdentifiers(t *goInterfaceType, pkgPath string) error {
	methods := interfaceMethodSet(t, pkgPath, map[string]bool{})
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
		return errorf(cmp.Or(method.pos, existing.pos),
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
	"CustomPropertyValue": "encoding/json/jsontext.Value",
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
	scalarType, ok := builtinTypes[graphQLName]
	if ok {
		return scalarType, true
	}
	scalarType, ok = githubScalarTypes[graphQLName]
	return scalarType, ok
}

type inputFieldSpec struct {
	ownerName   string
	fieldName   string
	kindLabel   string
	astType     *ast.Type
	hasDefault  bool
	description string
	fieldPos    *ast.Position
	errPos      *ast.Position
	options     *fieldOptions
}

func (g *planState) convertInputField(spec *inputFieldSpec) (*goStructField, error) {
	if spec.astType.NonNull && !spec.hasDefault && spec.options.GetOmitempty() {
		return nil, errorf(spec.errPos,
			"omitempty may only be used on optional arguments: %s.%s",
			spec.ownerName, spec.fieldName)
	}

	goTyp, err := g.convertInputType(spec.astType, spec.options)
	if err != nil {
		return nil, err
	}

	omitempty := spec.options.GetOmitempty()

	_, isPointer := goTyp.(*goPointerType)
	if spec.astType.NonNull && isPointer && !omitempty {
		return nil, errorf(spec.errPos,
			"pointer on non-null %s can only be used together with omitempty: %s.%s",
			spec.kindLabel, spec.ownerName, spec.fieldName)
	}

	goName := upperFirst(spec.fieldName)
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
			ownerName:  operation.Name,
			fieldName:  arg.Variable,
			kindLabel:  "argument",
			astType:    arg.Type,
			hasDefault: arg.DefaultValue != nil,
			fieldPos:   arg.Position,
			errPos:     arg.Position,
			options:    options,
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
) (goType, error) {
	if options.Bind != "" && options.Bind != "-" {
		return g.convertLocalBinding(typ, nil, options)
	}
	if typ.Elem != nil {
		elem, err := g.convertInputType(typ.Elem, options)
		if err != nil {
			return nil, err
		}
		return &goSliceType{Elem: elem}, nil
	}

	def := g.schema.Types[typ.Name()]
	goTyp, err := g.convertInputDefinition(def, typ.Position, options)
	if err != nil {
		return nil, err
	}
	return applyPointerSelection(goTyp, typ, options), nil
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
	goTyp, err := g.convertOutputDefinition(&outputDefinitionSpec{
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

func (g *planState) resolveConfiguredBinding(
	def *ast.Definition,
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
		}
		parsedType, err := parseRef(globalBinding)
		if err != nil {
			return nil, err
		}
		return &goOpaqueType{
			Ref:         parsedType,
			GraphQLName: def.Name,
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
	existing, err := g.getType(name, def.Name, describeGraphQLSource(def.Name, ""), provenancePos)
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
			goName := name + goConstName(val.Name)
			conflict := goNames[goName]
			if conflict != nil {
				return nil, errorf(val.Position,
					"enum values %s and %s have conflicting Go name %s",
					val.Name, conflict.GraphQLName, goName)
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
) (goType, error) {
	bound, err := g.resolveConfiguredBinding(def, nil, options)
	if bound != nil || err != nil {
		return bound, err
	}

	name := upperFirst(def.Name)
	if options.TypeName != "" {
		if goKeywords[options.TypeName] {
			return nil, errorf(pos, "typename option must not be a go keyword")
		}
		name = options.TypeName
	}

	provPos := provenancePosition(pos, options)
	if def.Kind != ast.InputObject {
		return g.convertScalarOrEnumDefinition(name, def, pos, provPos)
	}

	existing, err := g.getType(name, def.Name, describeGraphQLSource(def.Name, ""), provPos)
	if existing != nil || err != nil {
		return existing, err
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
		return nil, err
	}

	for i, field := range def.Fields {
		opts, dirErr := g.directives.forInputField(def, field)
		if dirErr != nil {
			return nil, dirErr
		}
		convertedField, fieldErr := g.convertInputField(&inputFieldSpec{
			ownerName:   name,
			fieldName:   field.Name,
			kindLabel:   "input field",
			astType:     field.Type,
			hasDefault:  field.DefaultValue != nil,
			description: field.Description,
			fieldPos:    field.Position,
			errPos:      pos,
			options:     opts,
		})
		if fieldErr != nil {
			return nil, fieldErr
		}
		goTyp.Fields[i] = convertedField
	}
	return goTyp, nil
}

type compositeSelectionSpec struct {
	goName        string
	prefix        namePrefix
	def           *ast.Definition
	selectionSet  ast.SelectionSet
	defaults      defaultOptions
	desc          descriptionInfo
	provenancePos *ast.Position
	pos           *ast.Position
	forceStruct   bool
	flatten       bool
}

func (g *planState) buildCompositeSelectionType(spec *compositeSelectionSpec) (goType, error) {
	fields, err := g.convertSelectionSet(spec.prefix, spec.selectionSet, spec.def, spec.defaults)
	if err != nil {
		return nil, err
	}
	if spec.flatten {
		fieldIndex, flattenErr := validateFlattenOption(spec.def, spec.selectionSet, spec.pos)
		if flattenErr != nil {
			return nil, flattenErr
		}
		return fields[fieldIndex].GoType, nil
	}

	kind := spec.def.Kind
	if spec.forceStruct && validateStructOption(spec.def, spec.selectionSet, spec.pos) == nil {
		kind = ast.Object
	}

	switch kind {
	case ast.Object:
		goTyp := &goStructType{
			GoName:          spec.goName,
			Fields:          fields,
			descriptionInfo: spec.desc,
		}
		return g.addType(goTyp, goTyp.GoName, spec.provenancePos)

	case ast.Interface, ast.Union:
		return g.buildAbstractSelectionType(spec, fields)

	default:
		return nil, errorf(spec.pos, "unexpected kind: %v", spec.def.Kind)
	}
}

func (g *planState) buildAbstractSelectionType(
	spec *compositeSelectionSpec,
	sharedFields []*goStructField,
) (goType, error) {
	existing, err := g.getType(
		spec.goName,
		spec.def.Name,
		describeGraphQLSource(spec.def.Name, spec.desc.FragmentName),
		spec.provenancePos,
	)
	if err != nil {
		return nil, err
	}

	implementationTypes := g.schema.GetPossibleTypes(spec.def)
	sort.Slice(implementationTypes, func(i, j int) bool {
		return implementationTypes[i].Name < implementationTypes[j].Name
	})
	implementationTypes = g.filterReferencedImplementations(implementationTypes, spec.selectionSet)

	desc := spec.desc
	desc.IsUnion = spec.def.Kind == ast.Union
	goTyp := &goInterfaceType{
		GoName:          spec.goName,
		SharedFields:    sharedFields,
		descriptionInfo: desc,
	}
	existingInterface, isInterface := existing.(*goInterfaceType)
	if isInterface {
		goTyp.OtherImplementation = existingInterface.OtherImplementation
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

		implName := makeTypeName(spec.prefix, implDef.Name)
		if spec.prefix.single {
			implName = makeLongTypeName(spec.prefix, implDef.Name)
		}
		implTyp := &goStructType{
			GoName:          implName,
			Fields:          implFields,
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

	if existing == nil {
		err = g.attachCatchAllImplementation(goTyp, spec.def.Name, spec.pos)
		if err != nil {
			return nil, err
		}
	}
	return g.addType(goTyp, goTyp.GoName, spec.provenancePos)
}

type outputDefinitionSpec struct {
	prefix       namePrefix
	def          *ast.Definition
	pos          *ast.Position
	selectionSet ast.SelectionSet
	options      *fieldOptions
	defaults     defaultOptions
}

func (g *planState) convertOutputDefinition(spec *outputDefinitionSpec) (goType, error) {
	prefix := spec.prefix
	def := spec.def
	pos := spec.pos
	selectionSet := spec.selectionSet
	options := spec.options
	defaults := spec.defaults
	bound, err := g.resolveConfiguredBinding(def, selectionSet, options)
	if bound != nil || err != nil {
		return bound, err
	}

	var name string
	switch {
	case options.TypeName != "":
		if goKeywords[options.TypeName] {
			return nil, errorf(pos, "typename option must not be a go keyword")
		}
		name = options.TypeName
		if prefix.isSingle(name) {
			name = makeLongTypeName(prefix, def.Name)
		}
		prefix = newNamePrefix(options.TypeName)
	case def.Kind == ast.Enum:
		name = upperFirst(def.Name)
	default:
		name = makeTypeName(prefix, def.Name)
	}

	provPos := provenancePosition(pos, options)
	if def.Kind == ast.Enum || def.Kind == ast.Scalar {
		return g.convertScalarOrEnumDefinition(name, def, pos, provPos)
	}

	return g.buildCompositeSelectionType(&compositeSelectionSpec{
		goName:       name,
		prefix:       prefix,
		def:          def,
		selectionSet: selectionSet,
		defaults:     defaults,
		desc: descriptionInfo{
			GraphQLDescription: def.Description,
			GraphQLName:        def.Name,
		},
		provenancePos: provPos,
		pos:           pos,
		forceStruct:   options.GetStruct(),
		flatten:       options.GetFlatten(),
	})
}

func (g *planState) filterReferencedImplementations(
	implementations []*ast.Definition,
	selectionSet ast.SelectionSet,
) []*ast.Definition {
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
			fragmentApplicable := map[string]bool{}
			for _, possibleType := range g.schema.GetPossibleTypes(definition) {
				if current.applicable[possibleType.Name] {
					fragmentApplicable[possibleType.Name] = true
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

func (g *planState) attachCatchAllImplementation(
	abstractType *goInterfaceType,
	graphQLName string,
	pos *ast.Position,
) error {
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

	name := g.catchAllName(abstractType.GoName)
	comment := fmt.Sprintf(
		"%s represents %s implementations not explicitly selected by a fragment.",
		name,
		abstractType.GoName,
	)
	if slices.Contains(interfaceGetterFields(abstractType, map[string]bool{}), "Typename") {
		comment += " Use GetTypename to identify the concrete GraphQL type."
	}
	catchAll := &goStructType{
		GoName:          name,
		Fields:          fields,
		GraphQLName:     graphQLName,
		CommentOverride: comment,
	}
	added, err := g.addType(catchAll, catchAll.GoName, pos)
	if err != nil {
		return err
	}
	registered, ok := added.(*goStructType)
	if !ok {
		return errorf(pos, "internal error: catch-all type was %T", added)
	}
	abstractType.OtherImplementation = registered
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
		if typ == iface {
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

	return g.buildCompositeSelectionType(&compositeSelectionSpec{
		goName:       fragment.Name,
		prefix:       newNamePrefix(fragment.Name),
		def:          typ,
		selectionSet: fragment.SelectionSet,
		defaults:     fragOptions.Defaults,
		desc: descriptionInfo{
			CommentOverride:    precedingComment(fragment.Position),
			GraphQLName:        typ.Name,
			GraphQLDescription: typ.Description,
			FragmentName:       fragment.Name,
		},
		provenancePos: fragment.Position,
		pos:           fragment.Position,
		flatten:       fragOptions.Flatten,
	})
}

func (g *planState) convertField(
	prefix namePrefix,
	field *ast.Field,
	opts *fieldOptions,
	defaults defaultOptions,
) (*goStructField, error) {
	if field.Definition == nil {
		return nil, errorf(field.Position, "undefined field %v", field.Alias)
	}

	goName := field.Alias
	if opts.Alias != "" {
		goName = opts.Alias
	}
	goName = upperFirst(goName)
	prefix = nextPrefix(prefix, field)

	fieldGoType, err := g.convertSelectionType(&selectionTypeSpec{
		prefix:       prefix,
		typ:          field.Definition.Type,
		selectionSet: field.SelectionSet,
		options:      opts,
		defaults:     defaults,
	})
	if err != nil {
		return nil, err
	}

	if hasConditionalDirective(field.Directives) {
		if opts.PointerIsFalse() {
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
