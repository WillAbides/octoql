package generate

import (
	"bytes"
	"encoding/json"
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

// JSON tags in plannedOperation are for ExportOperations (see Config for details).
type plannedOperation struct {
	Type           ast.Operation `json:"-"`
	Name           string        `json:"operationName"`
	Doc            string        `json:"-"`
	Body           string        `json:"query"`
	Input          *goStructType `json:"-"`
	ResponseName   string        `json:"-"`
	SourceFilename string        `json:"sourceLocation"`
}

type operationTemplateData struct {
	*plannedOperation
	ContextType string
}

type exportedOperations struct {
	Operations []*plannedOperation `json:"operations"`
}

// generationPlan is the destination-neutral, read-only plan shared by client,
// exported-operations, and test-handler renderers.
type generationPlan struct {
	config     Config
	operations []*plannedOperation
	typeMap    map[string]goType
}

type planBuilder func(*Config) (*generationPlan, error)

type outputRenderer func(*generationPlan) ([]byte, error)

func renderTypeDefinitions(r *renderer, typeMap map[string]goType) ([]byte, error) {
	names := make([]string, 0, len(typeMap))
	for name := range typeMap {
		names = append(names, name)
	}
	sort.Strings(names)

	var buffer bytes.Buffer
	for _, name := range names {
		err := typeMap[name].WriteDefinition(&buffer, r)
		if err != nil {
			return nil, err
		}
		buffer.WriteString("\n\n")
	}
	return buffer.Bytes(), nil
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
		Type:           op.Operation,
		Name:           op.Name,
		Doc:            docComment,
		Body:           "\n" + builder.String(),
		Input:          inputType,
		ResponseName:   responseType.QualifiedRef(),
		SourceFilename: op.Position.Src.Name,
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

	operationNames := make(map[string]bool, len(operations))
	variablesTypeNames := make(map[string]bool, len(operations))
	for _, op := range operations {
		operationNames[op.Name] = true
		if op.Input != nil {
			variablesTypeNames[op.Input.GoName] = true
		}
	}

	enumValueNames := map[string]bool{}
	enumValuesVarNames := map[string]bool{}
	for _, typ := range typeMap {
		enumType, ok := typ.(*goEnumType)
		if !ok {
			continue
		}
		enumValuesVarNames["All"+enumType.GoName] = true
		for _, value := range enumType.Values {
			enumValueNames[value.GoName] = true
		}
	}

	seenPrefixCandidates := map[string]bool{}
	for _, set := range []map[string]bool{
		operationNames,
		enumValueNames,
		enumValuesVarNames,
	} {
		for name := range set {
			seenPrefixCandidates[name] = true
		}
	}
	for name := range typeMap {
		seenPrefixCandidates[name] = true
	}
	sortedCandidates := make([]string, 0, len(seenPrefixCandidates))
	for name := range seenPrefixCandidates {
		sortedCandidates = append(sortedCandidates, name)
	}
	sort.Strings(sortedCandidates)
	for _, name := range sortedCandidates {
		if strings.HasPrefix(name, reservedRuntimePrefix) {
			return errorf(
				nil,
				"generated identifier %q uses reserved prefix %q",
				name,
				reservedRuntimePrefix,
			)
		}
	}

	packageSymbols := make(map[string]string, len(typeMap)+len(enumValueNames)+len(enumValuesVarNames))
	for name := range typeMap {
		if variablesTypeNames[name] {
			continue
		}
		packageSymbols[name] = "a generated GraphQL type"
	}
	for name := range enumValueNames {
		packageSymbols[name] = "a generated enum value"
	}
	for name := range enumValuesVarNames {
		packageSymbols[name] = "a generated enum values variable"
	}

	for _, name := range clientRuntimeDeclarations {
		if typeMap[name] != nil {
			return errorf(
				nil,
				"generated runtime declaration %q conflicts with a generated GraphQL type",
				name,
			)
		}
		if enumValueNames[name] || enumValuesVarNames[name] {
			return errorf(
				nil,
				"generated runtime declaration %q conflicts with a generated enum declaration",
				name,
			)
		}
		packageSymbols[name] = fmt.Sprintf("runtime declaration %q", name)
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
		if conflict := packageSymbols[partialErrName]; conflict != "" {
			return errorf(
				nil,
				"generated partial data error %q conflicts with %s",
				partialErrName,
				conflict,
			)
		}
		if operationNames[partialErrName] {
			return errorf(
				nil,
				"generated partial data error %q conflicts with operation %q",
				partialErrName,
				partialErrName,
			)
		}
		packageSymbols[partialErrName] = fmt.Sprintf("partial data error %q", partialErrName)

		if op.Input == nil {
			continue
		}
		varsName := op.Input.GoName
		if operationNames[varsName] {
			return errorf(
				nil,
				"generated variables type %q conflicts with operation %q",
				varsName,
				varsName,
			)
		}
		if conflict := packageSymbols[varsName]; conflict != "" {
			return errorf(
				nil,
				"generated variables type %q conflicts with %s",
				varsName,
				conflict,
			)
		}
		packageSymbols[varsName] = fmt.Sprintf("variables type %q", varsName)
	}

	return nil
}

func buildGenerationPlan(config *Config) (*generationPlan, error) {
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

	plan := &generationPlan{
		config:     *config,
		operations: state.operations,
		typeMap:    state.typeMap,
	}
	err = validateTestHandlerNames(plan)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func renderClient(plan *generationPlan) ([]byte, error) {
	r := newRenderer(plan.config.Package, plan.config.pkgPath, "")
	err := registerBoundTypeImports(plan.typeMap, r.imports)
	if err != nil {
		return nil, err
	}

	typeDefinitions, err := renderTypeDefinitions(r, plan.typeMap)
	if err != nil {
		return nil, err
	}

	var bodyBuf bytes.Buffer
	if plan.config.ContextType != "-" && plan.config.ContextType != "context.Context" {
		ctxIface, ctxErr := r.imports.ref("context.Context")
		if ctxErr != nil {
			return nil, ctxErr
		}
		customCtx, customErr := r.imports.ref(plan.config.ContextType)
		if customErr != nil {
			return nil, customErr
		}
		fmt.Fprintf(
			&bodyBuf,
			"// Check that context_type from octoqlgen.yaml implements context.Context.\nvar _ %s = (%s)(nil)\n\n",
			ctxIface,
			customCtx,
		)
	}

	err = r.render("runtime.go.tmpl", &bodyBuf, nil)
	if err != nil {
		return nil, err
	}
	bodyBuf.WriteString("\n")
	err = r.render("runtime_facets.go.tmpl", &bodyBuf, nil)
	if err != nil {
		return nil, err
	}
	bodyBuf.Write(typeDefinitions)

	for _, op := range plan.operations {
		err = r.render("operation.go.tmpl", &bodyBuf, operationTemplateData{
			plannedOperation: op,
			ContextType:      plan.config.ContextType,
		})
		if err != nil {
			return nil, err
		}
	}

	return r.formatGoFile(
		"// Code generated by octoqlgen, DO NOT EDIT.",
		plan.config.Generated,
		"",
		bodyBuf.Bytes(),
	)
}

func renderExportedOperations(plan *generationPlan) ([]byte, error) {
	content, err := json.MarshalIndent(
		exportedOperations{Operations: plan.operations},
		"",
		"  ",
	)
	if err != nil {
		return nil, errorf(nil, "unable to export queries: %v", err)
	}
	return content, nil
}

func generateWith(
	config *Config,
	buildPlan planBuilder,
	clientRenderer outputRenderer,
	handlerRenderer outputRenderer,
) (map[string][]byte, error) {
	plan, err := buildPlan(config)
	if err != nil {
		return nil, err
	}

	client, err := clientRenderer(plan)
	if err != nil {
		return nil, err
	}

	outputs := map[string][]byte{
		config.Generated: client,
	}
	if config.ExportOperations != "" {
		outputs[config.ExportOperations], err = renderExportedOperations(plan)
		if err != nil {
			return nil, err
		}
	}

	if config.TestHandlerGenerated != "" {
		outputs[config.TestHandlerGenerated], err = handlerRenderer(plan)
		if err != nil {
			return nil, err
		}
	}

	return outputs, nil
}

// Generate renders every configured output from one shared generation plan.
func Generate(config *Config) (map[string][]byte, error) {
	return generateWith(
		config,
		buildGenerationPlan,
		renderClient,
		renderTestHandler,
	)
}
