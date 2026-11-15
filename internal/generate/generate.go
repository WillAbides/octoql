package generate

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/validator"
)

// planState holds mutable conversion state while building an immutable
// generationPlan.
type planState struct {
	config        *Config
	operations    []*plannedOperation
	typeMap       map[string]goType
	typePositions map[string]*ast.Position
	schema        *ast.Schema
	directives    *directiveSet
}

type plannedOperation struct {
	Type         ast.Operation
	Name         string
	Doc          string
	Body         string
	Input        *goStructType
	ResponseName string
}

// usedFragments returns the named fragments transitively spread into op.
func usedFragments(op *ast.OperationDefinition) ast.FragmentDefinitionList {
	var retval ast.FragmentDefinitionList
	seen := map[string]bool{}

	var visit func(ast.SelectionSet)
	visit = func(selectionSet ast.SelectionSet) {
		for _, selection := range selectionSet {
			switch sel := selection.(type) {
			case *ast.Field:
				visit(sel.SelectionSet)
			case *ast.InlineFragment:
				visit(sel.SelectionSet)
			case *ast.FragmentSpread:
				if sel.Definition == nil || seen[sel.Name] {
					continue
				}
				seen[sel.Name] = true
				retval = append(retval, sel.Definition)
				visit(sel.Definition.SelectionSet)
			}
		}
	}

	visit(op.SelectionSet)
	return retval
}

// preprocessQueryDocument normalizes __typename to non-null String! and injects
// __typename onto abstract field selections when missing.
func preprocessQueryDocument(schema *ast.Schema, doc *ast.QueryDocument) {
	var observers validator.Events
	observers.OnField(func(_ *validator.Walker, field *ast.Field) {
		if field.Name == "__typename" {
			definition := *field.Definition
			definition.Type = ast.NonNullNamedType(
				"String",
				field.Definition.Type.Position,
			)
			field.Definition = &definition
			return
		}

		fieldType := schema.Types[field.Definition.Type.Name()]
		if fieldType.Kind != ast.Interface && fieldType.Kind != ast.Union {
			return
		}

		for _, selection := range field.SelectionSet {
			subField, ok := selection.(*ast.Field)
			if ok && subField.Name == "__typename" {
				return
			}
		}

		field.SelectionSet = append(ast.SelectionSet{
			&ast.Field{
				Alias: "__typename",
				Name:  "__typename",
				Definition: &ast.FieldDefinition{
					Name: "__typename",
					Type: ast.NonNullNamedType("String", nil),
				},
				ObjectDefinition: fieldType,
			},
		}, field.SelectionSet...)
	})
	validator.Walk(schema, doc, &observers)
}

func (g *planState) validateOperation(op *ast.OperationDefinition) error {
	if op.Operation == ast.Subscription {
		return errorf(op.Position, "subscriptions are not supported by octoql")
	}

	_, err := g.baseTypeForOperation(op.Operation)
	if err != nil {
		return err
	}

	if op.Name == "" {
		return errorf(op.Position, "operations must have operation-names")
	}
	if goKeywords[op.Name] {
		return errorf(op.Position, "operation name must not be a go keyword")
	}
	return nil
}

func (g *planState) addOperation(op *ast.OperationDefinition) error {
	err := g.validateOperation(op)
	if err != nil {
		return err
	}

	queryDoc := &ast.QueryDocument{
		Operations: ast.OperationList{op},
		Fragments:  usedFragments(op),
	}
	preprocessQueryDocument(g.schema, queryDoc)

	var builder strings.Builder
	formatter.NewFormatter(&builder).FormatQueryDocument(queryDoc)

	opOptions := g.directives.forOperation(op)
	commentLines := precedingComment(op.Position)

	inputType, err := g.convertArguments(op, opOptions)
	if err != nil {
		return err
	}

	responseType, err := g.convertOperation(op, opOptions)
	if err != nil {
		return err
	}

	var docComment string
	if commentLines != "" {
		docComment = "// " + strings.ReplaceAll(commentLines, "\n", "\n// ")
	}

	g.operations = append(g.operations, &plannedOperation{
		Type:         op.Operation,
		Name:         op.Name,
		Doc:          docComment,
		Body:         "\n" + builder.String(),
		Input:        inputType,
		ResponseName: responseType.QualifiedRef(),
	})
	return nil
}

var clientRuntimeDeclarations = []string{
	"Client",
	"DefaultResponseSizeLimit",
	"Error",
	"Errors",
	"ErrorType",
	"Location",
	"NewClient",
	"Path",
	"RateLimit",
	"RateLimitError",
	"RateLimitKind",
	"RateLimitPrimary",
	"RateLimitSecondary",
	"ResponseError",
	"ResponseSizeLimitError",
}

var clientRuntimeMethods = map[string]bool{
	"AllowRedirects":       true,
	"RateLimit":            true,
	"ResponseSizeLimit":    true,
	"SetAllowRedirects":    true,
	"SetBearerToken":       true,
	"SetResponseSizeLimit": true,
}

func validateClientNames(typeMap map[string]goType, operations []*plannedOperation) error {
	const reservedRuntimePrefix = "_octoql"

	type symbol struct {
		declaration string
		isOperation bool
	}
	packageSymbols := make(map[string]symbol, len(typeMap)+len(operations))
	for name := range typeMap {
		packageSymbols[name] = symbol{declaration: "a generated GraphQL type"}
	}
	for _, op := range operations {
		entry := packageSymbols[op.Name]
		entry.isOperation = true
		packageSymbols[op.Name] = entry
		if op.Input != nil {
			entry = packageSymbols[op.Input.GoName]
			entry.declaration = ""
			packageSymbols[op.Input.GoName] = entry
		}
	}
	for _, typ := range typeMap {
		enumType, ok := typ.(*goEnumType)
		if !ok {
			continue
		}
		for _, value := range enumType.Values {
			entry := packageSymbols[value.GoName]
			entry.declaration = "a generated enum value"
			packageSymbols[value.GoName] = entry
		}
	}

	names := make([]string, 0, len(packageSymbols))
	for name := range packageSymbols {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, reservedRuntimePrefix) {
			return errorf(
				nil,
				"generated identifier %q uses reserved prefix %q",
				name,
				reservedRuntimePrefix,
			)
		}
	}

	for _, name := range clientRuntimeDeclarations {
		if typeMap[name] != nil {
			return errorf(
				nil,
				"generated runtime declaration %q conflicts with a generated GraphQL type",
				name,
			)
		}
		entry := packageSymbols[name]
		if entry.declaration == "a generated enum value" {
			return errorf(
				nil,
				"generated runtime declaration %q conflicts with a generated enum declaration",
				name,
			)
		}
		entry.declaration = fmt.Sprintf("runtime declaration %q", name)
		packageSymbols[name] = entry
	}

	for _, op := range operations {
		if clientRuntimeMethods[op.Name] {
			return errorf(
				nil,
				"generated Client method %q conflicts with operation %q",
				op.Name,
				op.Name,
			)
		}

		partialErrName := op.Name + "PartialDataError"
		entry := packageSymbols[partialErrName]
		if entry.declaration != "" {
			return errorf(
				nil,
				"generated partial data error %q conflicts with %s",
				partialErrName,
				entry.declaration,
			)
		}
		if entry.isOperation {
			return errorf(
				nil,
				"generated partial data error %q conflicts with operation %q",
				partialErrName,
				partialErrName,
			)
		}
		entry.declaration = fmt.Sprintf("partial data error %q", partialErrName)
		packageSymbols[partialErrName] = entry

		if op.Input == nil {
			continue
		}
		varsName := op.Input.GoName
		entry = packageSymbols[varsName]
		if entry.isOperation {
			return errorf(
				nil,
				"generated variables type %q conflicts with operation %q",
				varsName,
				varsName,
			)
		}
		if entry.declaration != "" {
			return errorf(
				nil,
				"generated variables type %q conflicts with %s",
				varsName,
				entry.declaration,
			)
		}
		entry.declaration = fmt.Sprintf("variables type %q", varsName)
		packageSymbols[varsName] = entry
	}

	return nil
}

func buildGenerationPlan(config *Config) (*planState, error) {
	operationFiles, err := config.resolveOperationFiles()
	if err != nil {
		return nil, err
	}

	schema, err := getSchema(config.Schema)
	if err != nil {
		return nil, err
	}

	document, err := getAndValidateQueries(
		config.baseDir,
		operationFiles,
		schema,
	)
	if err != nil {
		return nil, err
	}

	if len(document.Operations) == 0 {
		return nil, errorf(nil,
			"no queries found, looked in: %v (configure this in octoqlgen.yaml)",
			strings.Join(config.Operations, ", "))
	}

	directives, err := collectDirectives(document, schema)
	if err != nil {
		return nil, err
	}

	state := &planState{
		config:        config,
		typeMap:       map[string]goType{},
		typePositions: map[string]*ast.Position{},
		schema:        schema,
		directives:    directives,
	}

	for _, op := range document.Operations {
		err = state.addOperation(op)
		if err != nil {
			return nil, err
		}
	}

	err = checkGeneratedIdentifiers(state.typeMap, config.pkgPath)
	if err != nil {
		return nil, err
	}
	err = validateClientNames(state.typeMap, state.operations)
	if err != nil {
		return nil, err
	}

	sort.Slice(state.operations, func(i, j int) bool {
		return state.operations[i].Name < state.operations[j].Name
	})

	return state, nil
}

func reserveClientDeclarations(plan *planState, scope *importScope) {
	for _, name := range clientRuntimeDeclarations {
		scope.usedAliases[name] = true
	}
	for name, typ := range plan.typeMap {
		scope.usedAliases[name] = true
		enumType, ok := typ.(*goEnumType)
		if !ok {
			continue
		}
		for _, val := range enumType.Values {
			scope.usedAliases[val.GoName] = true
		}
	}
	for _, op := range plan.operations {
		scope.usedAliases[op.Name+"PartialDataError"] = true
	}
}

func renderClient(plan *planState) ([]byte, error) {
	r := newRenderer(plan.config.Package, plan.config.pkgPath)
	reserveClientDeclarations(plan, r.imports)
	registerBoundTypeImports(plan.typeMap, r.imports)

	var bodyBuf bytes.Buffer
	err := r.render("runtime.go.tmpl", &bodyBuf, nil)
	if err != nil {
		return nil, err
	}

	typeNames := make([]string, 0, len(plan.typeMap))
	for name := range plan.typeMap {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		err = plan.typeMap[name].WriteDefinition(&bodyBuf, r)
		if err != nil {
			return nil, err
		}
		bodyBuf.WriteString("\n\n")
	}

	for _, op := range plan.operations {
		err = r.render("operation.go.tmpl", &bodyBuf, op)
		if err != nil {
			return nil, err
		}
	}

	return r.formatGoFile(
		"// Code generated by octoqlgen, DO NOT EDIT.",
		"",
		bodyBuf.Bytes(),
	)
}

// Generate renders every configured output from one shared generation plan.
func Generate(config *Config) (map[string][]byte, error) {
	err := config.ValidateAndFillDefaults(".")
	if err != nil {
		return nil, err
	}
	plan, err := buildGenerationPlan(config)
	if err != nil {
		return nil, err
	}

	client, err := renderClient(plan)
	if err != nil {
		return nil, err
	}

	outputs := map[string][]byte{
		config.Generated: client,
	}
	if config.TestHandlerGenerated != "" {
		outputs[config.TestHandlerGenerated], err = renderTestHandler(plan)
		if err != nil {
			return nil, err
		}
	}

	return outputs, nil
}
