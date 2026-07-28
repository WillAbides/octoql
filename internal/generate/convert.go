package generate

// This file implements the core type-generation logic of octoqlgen, whereby we
// traverse an operation-definition (and the schema against which it will be
// executed), and convert that into Go types.  It returns data structures
// representing the types to be generated; these are defined, and converted
// into code, in types.go.
//
// The entrypoints are convertOperation, which builds the response-type for a
// query, and convertArguments, which builds the argument-types.

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// getType returns the existing type in g.typeMap with the given name, if any,
// and an error if such type is incompatible with this one.
//
// This is useful as an early-out and a safety-check when generating types; if
// the type has already been generated we can skip generating it again.  (This
// is necessary to handle recursive input types, and an optimization in other
// cases.)
func (g *generator) getType(
	goName, graphQLName, newSource string,
	selectionSet ast.SelectionSet,
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
			typ.GraphQLTypeName(), graphQLName)
	}

	expectedSelectionSet := typ.SelectionSet()
	err := selectionsMatch(pos, selectionSet, expectedSelectionSet)
	if err != nil {
		oldSource := describeTypeSource(typ)
		oldPos := g.typePositions[goName]
		return typ, errorf(
			pos, "conflicting definition for the Go type %s: it is generated "+
				"from both %s (at %s) and %s (at %s), which select different "+
				"fields (%v); give one of them a distinct name with an "+
				"@octoqlgen(typename:) directive so they produce separate Go types",
			goName, oldSource, posString(oldPos), newSource, posString(pos), err)
	}

	return typ, nil
}

// inputFieldOptions returns the options that apply to each field of an input
// type under one operation's @octoqlgenDefaults.
func (g *generator) inputFieldOptions(
	def *ast.Definition,
	queryOptions *octoqlgenDirective,
) (map[string]*octoqlgenDirective, error) {
	options := make(map[string]*octoqlgenDirective, len(def.Fields))
	for _, field := range def.Fields {
		fieldOptions, err := g.directiveFor(field, def, field.Position, queryOptions)
		if err != nil {
			return nil, err
		}
		options[field.Name] = fieldOptions
	}
	return options, nil
}

// checkInputDefaultsAgree rejects a second operation that would have generated
// a different shape for an input type another operation already generated.
//
// An input type is named by the schema, so every operation shares one Go type
// for it, and the early-out above returns that type without rebuilding its
// fields.  @octoqlgenDefaults are per-operation and may legitimately differ, so
// without this the operation converted first would silently decide the shape
// for all of them and reordering the operations would change the result.
//
// @octoqlgenFor is not the problem here: those declarations are reconciled
// across operations before conversion, so they give every operation the same
// answer.  That is also the remedy, which is what the error says.
func (g *generator) checkInputDefaultsAgree(
	def *ast.Definition,
	name string,
	queryOptions *octoqlgenDirective,
	pos *ast.Position,
) error {
	existing, ok := g.inputTypeOptions[name]
	if !ok {
		return nil
	}
	options, err := g.inputFieldOptions(def, queryOptions)
	if err != nil {
		return err
	}
	for _, field := range def.Fields {
		if directiveOptionsMatch(existing.perField[field.Name], options[field.Name]) {
			continue
		}
		return errorf(pos,
			"conflicting @%s for the input type %s: this operation asks for "+
				"something different for %s.%s than the one at %s, and both "+
				"share one generated type; declare it once with "+
				"@%s(field: \"%s.%s\", ...) instead",
			octoqlgenDefaultsName, def.Name, def.Name, field.Name,
			posString(existing.pos), octoqlgenForName, def.Name, field.Name)
	}
	return nil
}

// inputTypeOptions records the options an input type was generated with, so a
// later operation that would have generated it differently can be rejected.
type inputTypeOptions struct {
	perField map[string]*octoqlgenDirective
	pos      *ast.Position
}

// describeTypeSource returns a human-readable description of the GraphQL
// construct that produced a generated type, for use in conflict errors.
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

// describeGraphQLSource describes a construct by its GraphQL type name and,
// optionally, the fragment that selected it.
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

// addType inserts the type into g.typeMap, checking for conflicts.
//
// The conflict-checking is as described in getType.  Note we have to do it
// here again, even if the caller has already called getType, because the
// caller in between may have generated new types, which potentially creates
// new conflicts.
//
// After the AST-level selection check in getType passes, addType additionally
// compares the generated field plan (Go field names, type references, JSON
// names, omitempty) via generatedTypeFieldsMatch, so that selection-based
// types that produce different Go output are caught even when their AST
// selections are identical.
//
// Returns an already-existing type if found, and otherwise the given type.
func (g *generator) addType(typ goType, goName string, pos *ast.Position) (goType, error) {
	newSource := describeTypeSource(typ)
	otherTyp, err := g.getType(goName, typ.GraphQLTypeName(), newSource, typ.SelectionSet(), pos)
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
				newSource, posString(pos), fieldErr)
		}
		return otherTyp, nil
	}
	g.typeMap[goName] = typ
	g.typePositions[goName] = pos
	return typ, nil
}

// checkGeneratedIdentifiers verifies that every Go identifier octoqlgen emits
// into a generated struct or interface is unique: declared fields, Get<Field>
// getters, and the synthesized MarshalJSON/UnmarshalJSON methods.
func (g *generator) checkGeneratedIdentifiers() error {
	names := make([]string, 0, len(g.typeMap))
	for name := range g.typeMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch t := g.typeMap[name].(type) {
		case *goStructType:
			err := g.checkStructIdentifiers(t)
			if err != nil {
				return err
			}
		case *goInterfaceType:
			err := g.checkInterfaceIdentifiers(t)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// checkStructIdentifiers reports an error if two identifiers octoqlgen would
// emit for the given struct -- its declared fields, its getters, or its
// (un)marshal methods -- share the same Go name.  The identifiers are checked
// after casing normalization, since that is what turns names like foo_bar and
// fooBar into the same Go identifier.
func (g *generator) checkStructIdentifiers(t *goStructType) error {
	type identifierSource struct {
		description string
		pos         *ast.Position
	}
	// The remedy depends on where the fields come from.  Output selections can
	// be renamed with a GraphQL field alias, but input-object fields and
	// operation variables cannot be aliased, so for those we point at the
	// achievable fixes instead of suggesting an impossible alias.
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
			// Prefer the position of whichever source has one, so the error
			// points at the user's GraphQL rather than at nothing.
			errPos := pos
			if errPos == nil {
				errPos = existing.pos
			}
			return errorf(errPos,
				"generated type %s would emit the Go identifier %s for both %s and %s; %s",
				t.GoName, name, existing.description, description, remedy)
		}
		used[name] = identifierSource{description: description, pos: pos}
		return nil
	}

	// Declared struct fields.  Embedded fields (from named-fragment spreads)
	// are promoted under their type's Go name, so that is the identifier they
	// occupy.
	for _, field := range t.Fields {
		if field.IsEmbedded() {
			embeddedName := field.GoType.Unwrap().Reference()
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

	// The MarshalJSON/UnmarshalJSON methods, emitted only when the struct needs
	// custom (un)marshaling.
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

// interfaceMethod describes one method in a generated interface's method set,
// including methods promoted from embedded interface fragments.  It is derived
// from goInterfaceType.members, so the method set that is checked matches the
// one WriteDefinition emits.
type interfaceMethod struct {
	name string
	// signature is the method signature as it will be emitted, preserving the
	// binding's original type spelling so diagnostics show what the user wrote.
	signature string
	// canonSignature is signature with Go's predeclared type aliases folded to
	// their canonical spellings.  Compare this, not signature, when deciding
	// whether two same-named methods legally collapse: Go's rule is identical
	// types, not identical source text.
	canonSignature string
	// owner is the Go name of the interface that explicitly declares this
	// method.  For methods promoted from an embedded interface, owner is that
	// embedded interface, not the interface it was promoted into.
	owner       string
	description string
	pos         *ast.Position
}

// predeclaredTypeAliases folds Go's predeclared type aliases (byte, rune, any)
// to their canonical spelling.  Each pattern matches the alias as a whole
// identifier, so it also rewrites them inside pointer, slice, array, and map
// spellings (e.g. []byte) while leaving names like bytes untouched.
//
// A word boundary also matches after a dot, so a qualified pkg.byte would fold
// to pkg.uint8.  That is harmless: byte, rune, and any are lowercase, so such a
// type is unexported and can never be named through a cross-package selector.
var predeclaredTypeAliases = []struct {
	alias *regexp.Regexp
	canon string
}{
	{regexp.MustCompile(`\bbyte\b`), "uint8"},
	{regexp.MustCompile(`\brune\b`), "int32"},
	{regexp.MustCompile(`\bany\b`), "interface{}"},
}

// canonicalizeTypeRef rewrites Go's predeclared type aliases to their canonical
// spellings so type references Go considers identical compare equal.  Residual:
// identical types spelled through a user-defined or imported alias are not
// folded and will still be reported as conflicting.
func canonicalizeTypeRef(ref string) string {
	for _, a := range predeclaredTypeAliases {
		ref = a.alias.ReplaceAllString(ref, a.canon)
	}
	return ref
}

// interfaceMethodSet returns the full method set of the Go interface t would
// emit, expanding embedded interfaces into the methods Go promotes from them.
// It builds from the same members list WriteDefinition emits, so the checked
// method set cannot drift from the emitted one.  visited guards against
// re-expanding an interface already in progress; GraphQL forbids fragment
// cycles, so the embedding graph is acyclic and this is only defensive.
func interfaceMethodSet(t *goInterfaceType, visited map[string]bool) []interfaceMethod {
	if visited[t.GoName] {
		return nil
	}
	visited[t.GoName] = true

	var methods []interfaceMethod
	for _, member := range t.members() {
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
			// An embedded interface contributes its method set, not its name,
			// so expand it.  A non-interface cannot be embedded, so there is
			// nothing to promote.
			embedded, ok := member.field.GoType.Unwrap().(*goInterfaceType)
			if !ok {
				continue
			}
			methods = append(methods, interfaceMethodSet(embedded, visited)...)
		}
	}
	return methods
}

// checkInterfaceIdentifiers reports an error if the Go interface t would emit a
// method set that fails to compile: two same-named methods with different
// signatures, one or both promoted from an embedded interface.  It validates
// the method set -- expanding embedded interfaces into the methods they promote
// -- rather than the syntactic elements, which would mistake an embedded
// interface for a method and miss genuine promoted-method conflicts.
//
// checkStructIdentifiers does not cover this: an interface with no concrete
// implementations (reachable when omit_unreferenced_implementations is false)
// has no struct in the type map to inspect.
func (g *generator) checkInterfaceIdentifiers(t *goInterfaceType) error {
	seen := make(map[string]interfaceMethod)
	for _, method := range interfaceMethodSet(t, map[string]bool{}) {
		existing, ok := seen[method.name]
		if !ok {
			seen[method.name] = method
			continue
		}
		sameOwner := existing.owner == method.owner
		if !sameOwner && existing.canonSignature == method.canonSignature {
			// Identical methods promoted from distinct embedded interfaces are
			// legal and collapse into one.  Comparing canonSignature (not
			// signature) makes byte and uint8 collapse as Go collapses them.
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

// baseTypeForOperation returns the definition of the GraphQL type to which the
// root of the operation corresponds, e.g. the "Query" or "Mutation" type.
func (g *generator) baseTypeForOperation(operation ast.Operation) (*ast.Definition, error) {
	switch operation {
	case ast.Query:
		return g.schema.Query, nil
	case ast.Mutation:
		return g.schema.Mutation, nil
	default:
		return nil, errorf(nil, "unexpected operation: %v", operation)
	}
}

// convertOperation builds the response-type into which the given operation's
// result will be unmarshaled.
func (g *generator) convertOperation(
	operation *ast.OperationDefinition,
	queryOptions *octoqlgenDirective,
) (goType, error) {
	name := operation.Name + "Response"
	namePrefix := newPrefixList(operation.Name)
	if queryOptions.TypeName != "" {
		name = queryOptions.TypeName
		namePrefix = newPrefixList(queryOptions.TypeName)
	}

	baseType, err := g.baseTypeForOperation(operation.Operation)
	if err != nil {
		return nil, errorf(operation.Position, "%v", err)
	}

	// Instead of calling out to convertType/convertDefinition, we do our own
	// thing, because we want to do a few things differently, and because we
	// know we have an object type, so we can include only that case.
	fields, err := g.convertSelectionSet(
		namePrefix, operation.SelectionSet, baseType, queryOptions)
	if err != nil {
		return nil, err
	}

	// It's uncommon to spread a fragment across the whole operation, but doing
	// so allows multiple operations to return the same generated type.
	if queryOptions.GetFlatten() {
		fieldIndex, flattenErr := validateFlattenOption(
			baseType, operation.SelectionSet, operation.Position)
		if flattenErr != nil {
			return nil, flattenErr
		}
		return fields[fieldIndex].GoType, nil
	}

	goType := &goStructType{
		GoName: name,
		descriptionInfo: descriptionInfo{
			CommentOverride: fmt.Sprintf(
				"%v is returned by %v on success.", name, operation.Name),
			GraphQLName: baseType.Name,
			// omit the GraphQL description for baseType; it's uninteresting.
		},
		Fields:    fields,
		Selection: operation.SelectionSet,
	}

	return g.addType(goType, goType.GoName, operation.Position)
}

var builtinTypes = map[string]string{
	// GraphQL guarantees int32 is enough, but using int seems more idiomatic
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

// convertArguments builds the type of the GraphQL arguments to the given
// operation.
//
// This type is used as the generated operation's variables container.
func (g *generator) convertArguments(
	operation *ast.OperationDefinition,
	queryOptions *octoqlgenDirective,
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

		options, err := g.directiveFor(arg, nil, arg.Position, queryOptions)
		if err != nil {
			return nil, err
		}
		if arg.Type.NonNull && arg.DefaultValue == nil && options.GetOmitempty() {
			return nil, errorf(arg.Position,
				"omitempty may only be used on optional arguments: %s.%s",
				operation.Name, arg.Variable)
		}

		goName := arg.Variable
		goName = ApplyCasing(goName, g.Config.GetDefaultCasingAlgorithm(), true)
		// Some of the arguments don't apply here, namely the name-prefix (see
		// names.go) and the selection-set (we use all the input type's fields,
		// and so on recursively).  See also the `case ast.InputObject` in
		// convertDefinition, below.
		goTyp, err := g.convertType(nil, arg.Type, nil, options, queryOptions)
		if err != nil {
			return nil, err
		}
		_, isPointer := goTyp.(*goPointerType)
		if arg.Type.NonNull && isPointer && !options.GetOmitempty() {
			return nil, errorf(arg.Position,
				"pointer on non-null argument can only be used together with omitempty: %s.%s",
				operation.Name, arg.Variable)
		}

		fields[i] = &goStructField{
			GoName:      goName,
			GoType:      goTyp,
			JSONName:    arg.Variable,
			GraphQLName: arg.Variable,
			Omitempty:   options.GetOmitempty(),
			Position:    arg.Position,
		}
	}
	goTyp := &goStructType{
		GoName:    name,
		Fields:    fields,
		Selection: nil,
		IsInput:   true,
		descriptionInfo: descriptionInfo{
			CommentOverride: fmt.Sprintf(
				"%s contains the variables accepted by %s.", name, operation.Name),
			// fake name, used by addType
			GraphQLName: name,
		},
	}
	goTypAgain, err := g.addType(goTyp, goTyp.GoName, operation.Position)
	if err != nil {
		return nil, err
	}
	goTyp, ok := goTypAgain.(*goStructType)
	if !ok {
		return nil, errorf(
			operation.Position, "internal error: input type was %T", goTypAgain)
	}
	return goTyp, nil
}

// convertType decides the Go type we will generate corresponding to a
// particular GraphQL type.  In this context, "type" represents the type of a
// field, and may be a list or a reference to a named type, with or without the
// "non-null" annotation.
func (g *generator) convertType(
	namePrefix *prefixList,
	typ *ast.Type,
	selectionSet ast.SelectionSet,
	options, queryOptions *octoqlgenDirective,
) (goType, error) {
	// We check for local bindings here, so that you can bind, say, a
	// `[String!]` to a struct instead of a slice.  Global bindings can only
	// bind GraphQL named types, at least for now.
	localBinding := options.Bind
	if localBinding != "" && localBinding != "-" {
		if err := rejectConditionalDirectivesInBoundSelection(typ.Name(), selectionSet); err != nil {
			return nil, err
		}
		goRef, err := g.ref(localBinding)
		if err != nil {
			return nil, err
		}
		// TODO(benkraft): Add syntax to specify a custom (un)marshaler, if
		// it proves useful.
		goTyp := &goOpaqueType{
			GoRef:          goRef,
			QualifiedGoRef: localBinding,
			GraphQLName:    typ.Name(),
		}
		return applyPointerSelection(goTyp, typ, options), nil
	}

	if typ.Elem != nil {
		// Type is a list.
		elem, err := g.convertType(
			namePrefix, typ.Elem, selectionSet, options, queryOptions)
		if err != nil {
			return nil, err
		}
		return &goSliceType{elem}, nil
	}

	// If this is a builtin type or custom scalar, just refer to it.
	def := g.schema.Types[typ.Name()]
	goTyp, err := g.convertDefinition(
		namePrefix, def, typ.Position, selectionSet, options, queryOptions)
	if err != nil {
		return nil, err
	}

	if g.getStructReference(def) {
		if options.Pointer == nil || *options.Pointer {
			goTyp = &goPointerType{goTyp}
		}
		if options.Omitempty == nil || *options.Omitempty {
			oe := true
			options.Omitempty = &oe
		}
		return goTyp, nil
	}
	// Lists recurse before this point, so pointer selection applies to nullable
	// elements rather than slice containers. Whole-field local bindings apply it
	// before list recursion.
	return applyPointerSelection(goTyp, typ, options), nil
}

func applyPointerSelection(
	goTyp goType,
	typ *ast.Type,
	options *octoqlgenDirective,
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
	return &goPointerType{goTyp}
}

// getStructReference decides if a field should be of pointer type and have the omitempty flag set.
func (g *generator) getStructReference(
	def *ast.Definition,
) bool {
	return g.Config.StructReferences &&
		(def.Kind == ast.Object || def.Kind == ast.InputObject)
}

// convertDefinition decides the Go type we will generate corresponding to a
// particular GraphQL named type.
//
// In this context, "definition" (and "named type") refer to an
// *ast.Definition, which represents the definition of a type in the GraphQL
// schema, which may be referenced by a field-type (see convertType).
func (g *generator) convertDefinition(
	namePrefix *prefixList,
	def *ast.Definition,
	pos *ast.Position,
	selectionSet ast.SelectionSet,
	options, queryOptions *octoqlgenDirective,
) (goType, error) {
	// Check if we should use an existing type.  (This is usually true for
	// GraphQL scalars, but we allow you to bind non-scalar types too, if you
	// want, subject to the caveats described in Config.Bindings.)  Local
	// bindings are checked in the caller (convertType) and never get here,
	// unless the binding is "-" which means "ignore the global binding".
	globalBinding, ok := g.Config.Bindings[def.Name]
	if ok && options.Bind != "-" {
		if options.TypeName != "" {
			// The option position (in the query) is more useful here.
			return nil, errorf(options.pos,
				"typename option conflicts with global binding for %s; "+
					"use `bind: \"-\"` to override it", def.Name)
		}
		if def.Kind == ast.Object || def.Kind == ast.Interface || def.Kind == ast.Union {
			if err := rejectConditionalDirectivesInBoundSelection(def.Name, selectionSet); err != nil {
				return nil, err
			}
			err := g.validateBindingSelection(
				def.Name, globalBinding, pos, selectionSet)
			if err != nil {
				return nil, err
			}
		}
		goRef, err := g.ref(globalBinding.Type)
		return &goOpaqueType{
			GoRef:          goRef,
			QualifiedGoRef: globalBinding.Type,
			GraphQLName:    def.Name,
			Marshaler:      globalBinding.Marshaler,
			Unmarshaler:    globalBinding.Unmarshaler,
		}, err
	}
	goBuiltinName, ok := defaultScalarType(def.Name)
	if ok && options.TypeName == "" {
		goRef, err := g.ref(goBuiltinName)
		if err != nil {
			return nil, err
		}
		return &goOpaqueType{
			GoRef:          goRef,
			QualifiedGoRef: goBuiltinName,
			GraphQLName:    def.Name,
		}, nil
	}

	// Determine the name to use for this type.
	var name string
	if options.TypeName != "" {
		if goKeywords[options.TypeName] {
			return nil, errorf(pos, "typename option must not be a go keyword")
		}
		// If the user specified a name, use it!
		name = options.TypeName
		if namePrefix != nil && namePrefix.head == name && namePrefix.tail == nil {
			// Special case: if this name is also the only component of the
			// name-prefix, append the type-name anyway.  This happens when you
			// assign a type name to an interface type, and we are generating
			// one of its implementations.
			name = makeLongTypeName(namePrefix, def.Name, g.Config.GetDefaultCasingAlgorithm())
		}
		// (But the prefix is shared.)
		namePrefix = newPrefixList(options.TypeName)
	} else if def.Kind == ast.InputObject || def.Kind == ast.Enum {
		// If we're an input-object or enum, there is only one type we will
		// ever possibly generate for this type, so we don't need any of the
		// qualifiers.  This is especially helpful because the caller is very
		// likely to need to reference these types in their code.
		name = ApplyCasing(def.Name, g.Config.GetDefaultCasingAlgorithm(), true)
		// (namePrefix is ignored in this case.)
	} else {
		// Else, construct a name using the usual algorithm (see names.go).
		name = makeTypeName(namePrefix, def.Name, g.Config.GetDefaultCasingAlgorithm())
	}

	// Register and compare types against a position in the operation (the
	// field, variable, or @octoqlgen directive that selected this type), not
	// against pos, which is the schema location of the type's definition.  A
	// conflict is between two selections in the user's operations, so pointing
	// the error there -- and at two distinct locations -- is what makes it
	// actionable.  options.pos is the selecting node's position (the field or
	// variable, or the directive comment when one is present).
	provenancePos := pos
	if options != nil && options.pos != nil {
		provenancePos = options.pos
	}

	// For schema-definition types (InputObject, Enum, Scalar), take an early-
	// out if the type has already been generated: these types are generated
	// from the schema definition rather than from the selection, so a
	// selection-plan comparison has nothing meaningful to compare.  InputObject
	// also requires this path for recursive types: it pre-inserts an empty
	// struct via addType so getType on a second call returns that placeholder,
	// and the early-out is required to avoid overwriting it.
	// @octoqlgenFor declarations are reconciled across operations before any
	// of this runs, so they cannot make the result depend on which operation
	// converted the type first.  @octoqlgenDefaults are per-operation and may
	// legitimately differ, so an input type they reach is checked below.
	// For selection-based types (Object, Interface, Union), skip the early-
	// out so the candidate is built and reaches addType for structural
	// comparison.
	if def.Kind == ast.InputObject || def.Kind == ast.Enum || def.Kind == ast.Scalar {
		existing, err := g.getType(name, def.Name, describeGraphQLSource(def.Name, ""), selectionSet, provenancePos)
		if err != nil {
			return existing, err
		}
		if existing != nil {
			err = g.checkInputDefaultsAgree(def, name, queryOptions, provenancePos)
			if err != nil {
				return nil, err
			}
			return existing, nil
		}
	}

	if def.Kind == ast.InputObject {
		options, err := g.inputFieldOptions(def, queryOptions)
		if err != nil {
			return nil, err
		}
		g.inputTypeOptions[name] = inputTypeOptions{perField: options, pos: provenancePos}
	}

	desc := descriptionInfo{
		// TODO(benkraft): Copy any comment above this selection-set?
		GraphQLDescription: def.Description,
		GraphQLName:        def.Name,
	}

	// The struct option basically means "treat this as if it were an object".
	// (It only applies if valid; this is important if you said the whole
	// query should have `struct: true`.)
	kind := def.Kind
	if options.GetStruct() && validateStructOption(def, selectionSet, pos) == nil {
		kind = ast.Object
	}
	switch kind {
	case ast.Object:
		fields, err := g.convertSelectionSet(
			namePrefix, selectionSet, def, queryOptions)
		if err != nil {
			return nil, err
		}
		if options.GetFlatten() {
			fieldIndex, flattenErr := validateFlattenOption(def, selectionSet, pos)
			if flattenErr != nil {
				return nil, flattenErr
			}
			return fields[fieldIndex].GoType, nil
		}
		goType := &goStructType{
			GoName:          name,
			Fields:          fields,
			Selection:       selectionSet,
			descriptionInfo: desc,
		}
		return g.addType(goType, goType.GoName, provenancePos)

	case ast.InputObject:
		goType := &goStructType{
			GoName:          name,
			Fields:          make([]*goStructField, len(def.Fields)),
			descriptionInfo: desc,
			IsInput:         true,
		}
		// To handle recursive types, we need to add the type to the type-map
		// *before* converting its fields.
		_, err := g.addType(goType, goType.GoName, provenancePos)
		if err != nil {
			return nil, err
		}

		for i, field := range def.Fields {
			fieldOptions, err := g.directiveFor(
				field, def, field.Position, queryOptions)
			if err != nil {
				return nil, err
			}

			goName := field.Name
			goName = ApplyCasing(goName, g.Config.GetDefaultCasingAlgorithm(), true)
			// Several of the arguments don't really make sense here:
			// (note field.Type is necessarily a scalar, input, or enum)
			//  - namePrefix is ignored for input types and enums (see
			//    names.go) and for scalars (they use client-specified
			//    names)
			//  - selectionSet is ignored for input types, because we
			//    just use all fields of the type; and it's nonexistent
			//    for scalars and enums, our only other possible types
			// TODO(benkraft): Can we refactor to avoid passing the values that
			// will be ignored?  We know field.Type is a scalar, enum, or input
			// type.  But plumbing that is a bit tricky in practice.
			fieldGoType, err := g.convertType(
				namePrefix, field.Type, nil, fieldOptions, queryOptions)
			if err != nil {
				return nil, err
			}

			if !g.Config.StructReferences {
				// Only do this validation when StructReferences are not used, as that can generate types that would not
				// pass these validations. See https://github.com/Khan/genqlient/issues/342

				// Try to protect against generating a field type that could send `null` to a non-nullable graphQL
				// type. This does not protect against lists/slices, as Go zero-slices are already serialized as `null`
				// (which can therefore currently send invalid graphQL value - e.g. `null` for [String!]!).
				// And does not protect against custom MarshalJSON.
				_, isPointer := fieldGoType.(*goPointerType)
				if field.Type.NonNull && isPointer && !fieldOptions.GetOmitempty() {
					return nil, errorf(pos, "pointer on non-null input field can only be used together with omitempty: %s.%s", name, field.Name)
				}

				if fieldOptions.GetOmitempty() && field.Type.NonNull && field.DefaultValue == nil {
					return nil, errorf(pos, "omitempty may only be used on optional arguments: %s.%s", name, field.Name)
				}
			}

			goType.Fields[i] = &goStructField{
				GoName:      goName,
				GoType:      fieldGoType,
				JSONName:    field.Name,
				GraphQLName: field.Name,
				Description: field.Description,
				Omitempty:   fieldOptions.GetOmitempty(),
				Position:    field.Position,
			}
		}
		return goType, nil

	case ast.Interface, ast.Union:
		sharedFields, err := g.convertSelectionSet(
			namePrefix, selectionSet, def, queryOptions)
		if err != nil {
			return nil, err
		}
		// Flattening an abstract selection is valid only when its single
		// fragment spread applies to the whole abstract type.
		if options.GetFlatten() {
			fieldIndex, flattenErr := validateFlattenOption(def, selectionSet, pos)
			if flattenErr != nil {
				return nil, flattenErr
			}
			return sharedFields[fieldIndex].GoType, nil
		}
		implementationTypes := g.schema.GetPossibleTypes(def)
		// Make sure we generate stable output by sorting the types by name when we get them
		sort.Slice(implementationTypes, func(i, j int) bool { return implementationTypes[i].Name < implementationTypes[j].Name })
		implementationTypes = g.filterReferencedImplementations(implementationTypes, selectionSet)
		goType := &goInterfaceType{
			GoName:          name,
			SharedFields:    sharedFields,
			Selection:       selectionSet,
			descriptionInfo: desc,
		}

		for _, implDef := range implementationTypes {
			// TODO(benkraft): In principle we should skip generating a Go
			// field for __typename each of these impl-defs if you didn't
			// request it (and it was automatically added by
			// preprocessQueryDocument).  But in practice it doesn't really
			// hurt, and would be extra work to avoid, so we just leave it.
			implTyp, convertErr := g.convertDefinition(
				namePrefix, implDef, pos, selectionSet, options, queryOptions)
			if convertErr != nil {
				return nil, convertErr
			}

			implStructTyp, ok := implTyp.(*goStructType)
			if !ok { // (should never happen on a valid schema)
				return nil, errorf(
					pos, "interface %s had non-object implementation %s",
					def.Name, implDef.Name)
			}
			goType.Implementations = append(goType.Implementations, implStructTyp)
		}
		// Register the interface type before attaching the catch-all.  If an
		// existing compatible type is returned, its catch-all was already
		// attached on the first registration; return it to avoid a duplicate.
		result, addErr := g.addType(goType, goType.GoName, provenancePos)
		if addErr != nil {
			return nil, addErr
		}
		if result != goType {
			return result, nil
		}
		err = g.attachCatchAllImplementation(goType, def.Name, pos)
		if err != nil {
			return nil, err
		}
		return goType, nil

	case ast.Enum:
		goType := &goEnumType{
			GoName:      name,
			GraphQLName: def.Name,
			Description: def.Description,
			Values:      make([]goEnumValue, len(def.EnumValues)),
		}

		goNames := make(map[string]*goEnumValue, len(def.EnumValues))
		for i, val := range def.EnumValues {
			goName := g.Config.Casing.enumValueName(name, def, val)
			if conflict := goNames[goName]; conflict != nil {
				return nil, errorf(val.Position,
					"enum values %s and %s have conflicting Go name %s; "+
						"add 'all_enums: raw' or 'enums: %v: raw' "+
						"to 'casing' in octoqlgen.yaml to fix",
					val.Name, conflict.GraphQLName, goName, def.Name)
			}

			goType.Values[i] = goEnumValue{
				GoName:      goName,
				GraphQLName: val.Name,
				Description: val.Description,
			}
			goNames[goName] = &goType.Values[i]
		}
		return g.addType(goType, goType.GoName, provenancePos)

	case ast.Scalar:
		if builtinTypes[def.Name] != "" {
			// In this case, the user asked for a custom Go type-name
			// for a built-in type, e.g. `type MyString string`.
			goType := &goTypenameForBuiltinType{
				GoTypeName:    name,
				GoBuiltinName: builtinTypes[def.Name],
				GraphQLName:   def.Name,
			}
			return g.addType(goType, goType.GoTypeName, provenancePos)
		}

		// (If you had an entry in bindings, we would have returned it above.)
		return nil, errorf(
			pos, "unknown scalar %v: please add it to \"bindings\" in octoqlgen.yaml"+
				"\nExample: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml", def.Name)
	default:
		return nil, errorf(pos, "unexpected kind: %v", def.Kind)
	}
}

func (g *generator) filterReferencedImplementations(
	implementations []*ast.Definition,
	selectionSet ast.SelectionSet,
) []*ast.Definition {
	if !g.Config.omitUnreferencedImplementations() {
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

func (g *generator) possibleObjectTypes(definition *ast.Definition) map[string]bool {
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

func (g *generator) attachCatchAllImplementation(
	abstractType *goInterfaceType,
	graphQLName string,
	pos *ast.Position,
) error {
	if !g.Config.omitUnreferencedImplementations() {
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
		GoName:    g.catchAllName(abstractType.GoName),
		Fields:    fields,
		Selection: abstractType.Selection,
		descriptionInfo: descriptionInfo{
			GraphQLName: graphQLName,
			CommentOverride: fmt.Sprintf(
				"%sOctoqlOther represents %s implementations not explicitly selected by a fragment. "+
					"Use GetTypename to identify the concrete GraphQL type.",
				abstractType.GoName,
				abstractType.GoName,
			),
		},
	}
	_, err := g.addType(catchAll, catchAll.GoName, pos)
	if err != nil {
		return err
	}
	abstractType.OtherImplementation = catchAll
	return nil
}

func (g *generator) catchAllName(abstractTypeName string) string {
	name := abstractTypeName + "OctoqlOther"
	for suffix := 2; g.typeMap[name] != nil; suffix++ {
		name = fmt.Sprintf("%s%dOctoqlOther", abstractTypeName, suffix)
	}
	return name
}

// convertSelectionSet converts a GraphQL selection-set into a list of
// corresponding Go struct-fields (and their Go types)
//
// A selection-set is a list of fields within braces like `{ myField }`, as
// appears at the toplevel of a query, in a field's sub-selections, or within
// an inline or named fragment.
//
// containingTypedef is the type-def whose fields we are selecting, and may be
// an object type or an interface type.  In the case of interfaces, we'll call
// convertSelectionSet once for the interface, and once for each
// implementation.
func (g *generator) convertSelectionSet(
	namePrefix *prefixList,
	selectionSet ast.SelectionSet,
	containingTypedef *ast.Definition,
	queryOptions *octoqlgenDirective,
) ([]*goStructField, error) {
	fields := make([]*goStructField, 0, len(selectionSet))
	for _, selection := range selectionSet {
		selectionOptions, err := g.directiveFor(
			selection, nil, selection.GetPosition(), queryOptions)
		if err != nil {
			return nil, err
		}

		switch selection := selection.(type) {
		case *ast.Field:
			field, err := g.convertField(
				namePrefix, selection, selectionOptions, queryOptions)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
		case *ast.FragmentSpread:
			if hasConditionalDirective(selection.Directives) {
				return nil, errorf(selection.Position,
					"@skip and @include are not supported on fragment spreads "+
						"(...%s): octoqlgen cannot represent the absence of "+
						"every field the fragment contributes, so it would "+
						"otherwise silently generate value types for fields "+
						"that can vanish",
					selection.Name)
			}
			maybeField, err := g.convertFragmentSpread(selection, containingTypedef)
			if err != nil {
				return nil, err
			} else if maybeField != nil {
				fields = append(fields, maybeField)
			}
		case *ast.InlineFragment:
			if hasConditionalDirective(selection.Directives) {
				return nil, errorf(selection.Position,
					"@skip and @include are not supported on inline fragments: "+
						"octoqlgen cannot represent the absence of every field "+
						"the fragment contributes, so it would otherwise "+
						"silently generate value types for fields that can vanish")
			}
			// (Note this will return nil, nil if the fragment doesn't apply to
			// this type.)
			fragmentFields, err := g.convertInlineFragment(
				namePrefix, selection, containingTypedef, queryOptions)
			if err != nil {
				return nil, err
			}
			fields = append(fields, fragmentFields...)
		default:
			return nil, errorf(nil, "invalid selection type: %T", selection)
		}
	}

	// We need to deduplicate, if you asked for
	//	{ id, id, id, ... on SubType { id } }
	// (which, yes, is legal) we'll treat that as just { id }.
	uniqFields := make([]*goStructField, 0, len(selectionSet))
	fragmentNames := make(map[string]bool, len(selectionSet))
	fieldNames := make(map[string]bool, len(selectionSet))
	for _, field := range fields {
		// If you embed a field twice via a named fragment, we keep both, even
		// if there are complicated overlaps, since they are separate types to
		// us.  (See also the special handling for IsEmbedded in
		// unmarshal.go.tmpl.)
		//
		// But if you spread the same named fragment twice, e.g.
		//	{ ...MyFragment, ... on SubType { ...MyFragment } }
		// we'll still deduplicate that.
		if field.JSONName == "" {
			name := field.GoType.Reference()
			if fragmentNames[name] {
				continue
			}
			uniqFields = append(uniqFields, field)
			fragmentNames[name] = true
			continue
		}

		// GraphQL (and, effectively, JSON) requires that all fields with the
		// same alias (JSON-name) must be the same (i.e. refer to the same
		// field), so that's how we deduplicate.
		if fieldNames[field.JSONName] {
			// GraphQL (and, effectively, JSON) forbids you from having two
			// fields with the same alias (JSON-name) that refer to different
			// GraphQL fields.  But it does allow you to have the same field
			// with different selections (subject to some additional rules).
			// We say: that's too complicated! and allow duplicate fields
			// only if they're "leaf" types (enum or scalar).
			switch field.GoType.Unwrap().(type) {
			case *goOpaqueType, *goEnumType:
				// Leaf field; we can just deduplicate.
				// Note GraphQL already guarantees that the conflicting field
				// has scalar/enum type iff this field does:
				// https://spec.graphql.org/draft/#SameResponseShape()
				continue
			case *goStructType, *goInterfaceType:
				// TODO(benkraft): Keep track of the position of each
				// selection, so we can put this error on the right line.
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

// fragmentMatches returns true if the given fragment is "active" when applied
// to the given type.
//
// "Active" here means "the fragment's fields will be returned on all objects
// of the given type", which is true when the given type is or implements
// the fragment's type.  This is distinct from the rules for when a fragment
// spread is legal, which is true when the fragment would be active for *any*
// of the concrete types the spread-context could have (see the [GraphQL spec]).
//
// containingTypedef is as described in convertInlineFragment, below.
// fragmentTypedef is the definition of the fragment's type-condition, i.e. the
// definition of MyType in a fragment `on MyType`.
//
// [GraphQL spec]: https://spec.graphql.org/draft/#sec-Fragment-Spreads
func fragmentMatches(containingTypedef, fragmentTypedef *ast.Definition) bool {
	if containingTypedef.Name == fragmentTypedef.Name {
		return true
	}
	for _, iface := range containingTypedef.Interfaces {
		// Note we don't need to recurse into the interfaces here, because in
		// GraphQL types must list all the interfaces they implement, including
		// all types those interfaces implement ([spec]).  Actually, at present
		// gqlparser doesn't even support interfaces implementing other
		// interfaces, but our code would handle that too.
		//
		// [spec]: https://spec.graphql.org/draft/#sec-Interfaces.Interfaces-Implementing-Interfaces
		if iface == fragmentTypedef.Name {
			return true
		}
	}

	// Handle the special case where the fragment is on a union, then the
	// fragment can match any of the types in the union.
	if fragmentTypedef.Kind == ast.Union {
		for _, typeName := range fragmentTypedef.Types {
			if typeName == containingTypedef.Name {
				return true
			}
		}
	}

	return false
}

// convertInlineFragment converts a single GraphQL inline fragment
// (`... on MyType { myField }`) into Go struct-fields.
//
// containingTypedef is the type-def corresponding to the type into which we
// are spreading; it may be either an interface type (when spreading into one)
// or an object type (when writing the implementations of such an interface, or
// when using an inline fragment in an object type which is rare).  If the
// given fragment does not apply to that type, this function returns nil, nil.
//
// In general, we treat such fragments' fields as if they were fields of the
// parent selection-set, except they are only included in types the fragment
// matches.
func (g *generator) convertInlineFragment(
	namePrefix *prefixList,
	fragment *ast.InlineFragment,
	containingTypedef *ast.Definition,
	queryOptions *octoqlgenDirective,
) ([]*goStructField, error) {
	// You might think fragmentTypedef is just fragment.ObjectDefinition, but
	// actually that's the type into which the fragment is spread.
	fragmentTypedef := g.schema.Types[fragment.TypeCondition]
	if !fragmentMatches(containingTypedef, fragmentTypedef) {
		return nil, nil
	}
	return g.convertSelectionSet(namePrefix, fragment.SelectionSet,
		containingTypedef, queryOptions)
}

// convertFragmentSpread converts a single GraphQL fragment-spread
// (`...MyFragment`) into a Go struct-field.  If the fragment does not apply to
// this type, returns nil.
//
// containingTypedef is as described in convertInlineFragment, above.
func (g *generator) convertFragmentSpread(
	fragmentSpread *ast.FragmentSpread,
	containingTypedef *ast.Definition,
) (*goStructField, error) {
	if !fragmentMatches(containingTypedef, fragmentSpread.Definition.Definition) {
		return nil, nil
	}

	// Always convert the fragment via convertNamedFragment rather than taking
	// an early-out via getType.  The type stored under this fragment's name
	// may have been registered by an @octoqlgen(typename:) directive on an
	// unrelated field with different directives; convertNamedFragment routes
	// through addType, which validates both selection identity and structural
	// Go output equivalence, so any mismatch is caught rather than silently
	// reusing the wrong type.
	fragment := fragmentSpread.Definition
	typ, err := g.convertNamedFragment(fragment)
	if err != nil {
		return nil, err
	}

	iface, ok := typ.(*goInterfaceType)
	if ok && containingTypedef.Kind == ast.Object {
		// If the containing type is concrete, and the fragment spread is
		// abstract, refer directly to the appropriate implementation, to save
		// the caller having to do type-assertions that will always succeed.
		//
		// That is, if you do
		//	fragment F on I { ... }
		//  query Q { a { ...F } }
		// for the fragment we generate
		//  type F interface { ... }
		//  type FA struct { ... }
		//  // (other implementations)
		// when you spread F into a context of type A, we embed FA, not F.
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

	// TODO(benkraft): Set directive here if we ever allow @octoqlgen
	// directives on fragment-spreads.
	return &goStructField{GoName: "" /* i.e. embedded */, GoType: typ, Position: fragmentSpread.Position}, nil
}

// convertNamedFragment converts a single GraphQL named fragment-definition
// (`fragment MyFragment on MyType { ... }`) into a Go struct.
func (g *generator) convertNamedFragment(fragment *ast.FragmentDefinition) (goType, error) {
	typ := g.schema.Types[fragment.TypeCondition]

	directive, err := g.directiveFor(fragment, nil, fragment.Position, nil)
	if err != nil {
		return nil, err
	}
	comment := precedingComment(fragment.Position)

	desc := descriptionInfo{
		CommentOverride:    comment,
		GraphQLName:        typ.Name,
		GraphQLDescription: typ.Description,
		FragmentName:       fragment.Name,
	}

	// The rest basically follows how we convert a definition, except that
	// things like type-names are a bit different.

	fields, err := g.convertSelectionSet(
		newPrefixList(fragment.Name), fragment.SelectionSet, typ, directive)
	if err != nil {
		return nil, err
	}
	if directive.GetFlatten() {
		fieldIndex, flattenErr := validateFlattenOption(
			typ, fragment.SelectionSet, fragment.Position)
		if flattenErr != nil {
			return nil, flattenErr
		}
		return fields[fieldIndex].GoType, nil
	}
	switch typ.Kind {
	case ast.Object:
		goType := &goStructType{
			GoName:          fragment.Name,
			Fields:          fields,
			Selection:       fragment.SelectionSet,
			descriptionInfo: desc,
		}
		// Route through addType (rather than writing g.typeMap directly) so a
		// fragment whose Go name collides with an already-generated type is
		// rejected instead of silently overwriting it.
		return g.addType(goType, goType.GoName, fragment.Position)
	case ast.Interface, ast.Union:
		implementationTypes := g.schema.GetPossibleTypes(typ)
		// Make sure we generate stable output by sorting the types by name when we get them
		sort.Slice(implementationTypes, func(i, j int) bool { return implementationTypes[i].Name < implementationTypes[j].Name })
		implementationTypes = g.filterReferencedImplementations(implementationTypes, fragment.SelectionSet)
		goType := &goInterfaceType{
			GoName:          fragment.Name,
			SharedFields:    fields,
			Selection:       fragment.SelectionSet,
			descriptionInfo: desc,
		}
		// Register the interface first; addType validates shared-field
		// equivalence against any already-registered type of the same name.
		result, addErr := g.addType(goType, goType.GoName, fragment.Position)
		if addErr != nil {
			return nil, addErr
		}
		// Build and validate implementation candidates regardless of whether
		// the interface was newly registered or already existed.  On the reuse
		// path (result != goType) the per-implementation addType calls catch
		// field-plan differences that are invisible to shared-field comparison
		// alone — e.g. a directive inside an inline fragment that changes a
		// field's Go type.  goType.Implementations is populated here for the
		// first-registration path and discarded on the reuse path.
		for _, implDef := range implementationTypes {
			implFields, convertErr := g.convertSelectionSet(
				newPrefixList(fragment.Name), fragment.SelectionSet, implDef, directive)
			if convertErr != nil {
				return nil, convertErr
			}

			implDesc := desc
			implDesc.GraphQLName = implDef.Name

			implTyp := &goStructType{
				GoName:          fragment.Name + upperFirst(implDef.Name),
				Fields:          implFields,
				Selection:       fragment.SelectionSet,
				descriptionInfo: implDesc,
			}
			goType.Implementations = append(goType.Implementations, implTyp)
			// As above, route the fragment's per-implementation type through
			// addType so a name collision with a directly-selected type fails
			// generation instead of silently dropping the other type's fields.
			_, err = g.addType(implTyp, implTyp.GoName, fragment.Position)
			if err != nil {
				return nil, err
			}
		}

		// On the reuse path, the catch-all was already attached during the
		// first registration; return the existing type to avoid a duplicate.
		if result != goType {
			return result, nil
		}

		err = g.attachCatchAllImplementation(goType, typ.Name, fragment.Position)
		if err != nil {
			return nil, err
		}
		return goType, nil
	default:
		return nil, errorf(fragment.Position, "invalid type for fragment: %v is a %v",
			fragment.TypeCondition, typ.Kind)
	}
}

// convertField converts a single GraphQL operation-field into a Go
// struct-field (and its type).
//
// Note that input-type fields are handled separately (inline in
// convertDefinition), because they come from the type-definition, not the
// operation.
func (g *generator) convertField(
	namePrefix *prefixList,
	field *ast.Field,
	fieldOptions, queryOptions *octoqlgenDirective,
) (*goStructField, error) {
	if field.Definition == nil {
		// Unclear why gqlparser hasn't already rejected this,
		// but empirically it might not.
		return nil, errorf(
			field.Position, "undefined field %v", field.Alias)
	}

	goName := field.Alias
	if fieldOptions.Alias != "" {
		goName = fieldOptions.Alias
	}

	goName = ApplyCasing(goName, g.Config.GetDefaultCasingAlgorithm(), true)

	namePrefix = nextPrefix(namePrefix, field, g.Config.GetDefaultCasingAlgorithm())

	fieldGoType, err := g.convertType(
		namePrefix, field.Definition.Type, field.SelectionSet,
		fieldOptions, queryOptions)
	if err != nil {
		return nil, err
	}

	// A field carrying @skip(if:) or @include(if:) is legitimately absent from
	// a spec-correct response when its condition says so, regardless of the
	// field's schema nullability. Force a pointer so that absence is
	// representable as nil rather than silently decoding to the Go zero value.
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

// hasConditionalDirective reports whether the selection carries a @skip or
// @include directive. Such a selection may be absent from a spec-correct
// response, independent of schema nullability.
func hasConditionalDirective(directives ast.DirectiveList) bool {
	return directives.ForName("skip") != nil ||
		directives.ForName("include") != nil
}

// forceConditionalPointer makes goTyp able to represent an absent value as nil,
// for a field carrying @skip/@include. Types that can already hold nil are
// returned unchanged: pointers and interfaces obviously, but also slices — a nil
// slice already distinguishes an absent list from a present one, and wrapping a
// slice in an outer pointer would only mask its depth from the special
// marshal/unmarshal generation (which keys off the field's top-level slice
// depth), producing uncompilable code for lists of abstract or custom-marshalled
// elements. A bound Go type (goOpaqueType, from a local `bind:` or a global
// binding) is likewise left alone when its reference is already nil-able —
// a pointer, slice, map, or interface — since wrapping would churn the user's
// public API for no representational gain. Everything else (scalars, enums,
// structs, and non-nil-able bound types such as fixed-size arrays) is wrapped in
// a pointer.
func forceConditionalPointer(goTyp goType) goType {
	switch t := goTyp.(type) {
	case *goPointerType, *goInterfaceType, *goSliceType:
		return goTyp
	case *goOpaqueType:
		if goRefIsNilable(t.GoRef) {
			return goTyp
		}
	}
	return &goPointerType{goTyp}
}

// goRefIsNilable reports whether a Go type reference (as written in a `bind:`
// expression or a global binding, after resolution through (*generator).ref)
// denotes a type that can already hold nil. Only the leading token of the
// reference determines the nil-ability of the outermost type: pointers, slices,
// maps, and the `interface{}` literal are nil-able, while a fixed-size array
// [N]T is not — an array cannot represent absence, so a conditional field bound
// to one must still be wrapped in a pointer. The `[]` case must be tested before
// the bare `[` case so slices are not misread as arrays.
//
// Only the `interface{}` type literal counts as a nil-able interface, never the
// bare predeclared `any` identifier: `any` can be shadowed by a local
// declaration in the generated package, so trusting it could leave a
// non-nil-able field unwrapped. (*generator).ref normalizes bound `any` to
// `interface{}` at the source, so a well-formed reference never reaches here as
// bare `any`; treating it conservatively here is defense in depth in the safe
// direction — an unnecessary pointer, never a missing one.
func goRefIsNilable(goRef string) bool {
	goRef = strings.TrimSpace(goRef)
	switch {
	case goRef == "interface{}":
		return true
	case strings.HasPrefix(goRef, "*"):
		return true
	case strings.HasPrefix(goRef, "[]"):
		return true
	case strings.HasPrefix(goRef, "map["):
		return true
	case strings.HasPrefix(goRef, "["):
		// A fixed-size array [N]T cannot represent absence.
		return false
	default:
		return false
	}
}

// rejectConditionalDirectivesInBoundSelection returns an error if any selection
// nested within selectionSet (recursively, including through named fragments)
// carries @skip or @include.
//
// It guards binding sites. A composite type bound to a user-supplied Go type
// (local `bind:` or a global binding) is emitted as an opaque reference and its
// selection set is never converted into generated types, so convertField never
// runs on the nested fields and the forced-pointer protection for conditional
// fields cannot apply. Because octoqlgen cannot alter the bound Go type, a nested
// conditional field would silently decode an absent value to the Go zero value;
// we reject the operation instead. The bound field's own directive is handled by
// its convertField call, so only nested selections need this scan.
//
// bindingName identifies the bound GraphQL type in the error message.
func rejectConditionalDirectivesInBoundSelection(
	bindingName string,
	selectionSet ast.SelectionSet,
) error {
	return rejectConditionalDirectivesInSelection(
		bindingName, selectionSet, map[string]bool{})
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
			bindingName, nested, seenFragments)
		if err != nil {
			return err
		}
	}
	return nil
}
