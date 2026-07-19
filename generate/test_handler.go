package generate

import (
	"bytes"
	"go/format"
	"go/token"
	"sort"
	"strconv"

	"golang.org/x/tools/imports"
)

type testHandlerType struct {
	Name       string
	ClientName string
	EnumValues []goEnumValue
}

type testHandlerOperation struct {
	Name                string
	FieldName           string
	VariablesName       string
	ClientVariablesName string
	ResponseName        string
	ClientResponseName  string
	HasVariables        bool
}

type testHandlerTemplateData struct {
	Generator     *generator
	Package       string
	ClientPackage string
	Types         []testHandlerType
	Operations    []testHandlerOperation
}

var testHandlerReservedNames = []string{
	"ExpectOption",
	"MinTimes",
	"NewTestHandler",
	"ResponseOption",
	"TestHandler",
	"Times",
	"WithExtensions",
	"WithHeader",
	"WithHeaders",
	"WithPrimaryRateLimit",
	"WithSecondaryRateLimit",
	"WithStatus",
	"buildResponseOptions",
	"combineResponseOptions",
	"decodeVariables",
	"expectation",
	"expectationOptions",
	"expectationSet",
	"graphqlRequest",
	"operationResult",
	"responseOptions",
	"testTB",
	"variableKey",
	"variableKeyJSON",
	"writeGraphQLResponse",
	"writeRequestError",
}

func validateTestHandlerNames(plan *generationPlan) error {
	g := plan.generator
	if g.Config.TestHandlerGenerated == "" {
		return nil
	}

	identifiers := make(map[string]string)
	addIdentifier := func(name, source string) error {
		existing := identifiers[name]
		if existing != "" {
			return errorf(
				nil,
				"generated identifier %q for %s conflicts with %s",
				name,
				source,
				existing,
			)
		}
		identifiers[name] = source
		return nil
	}

	for _, name := range testHandlerReservedNames {
		err := addIdentifier(name, "test handler runtime")
		if err != nil {
			return err
		}
	}

	typeNames := make([]string, 0, len(g.typeMap))
	for name := range g.typeMap {
		if token.IsExported(name) {
			typeNames = append(typeNames, name)
		}
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		err := addIdentifier(name, "client type alias")
		if err != nil {
			return err
		}
		enumType, ok := g.typeMap[name].(*goEnumType)
		if !ok {
			continue
		}
		for _, enumValue := range enumType.Values {
			err = addIdentifier(enumValue.GoName, "client enum value alias")
			if err != nil {
				return err
			}
		}
	}

	clientIdentifiers := make(map[string]string)
	for name, generatedType := range g.typeMap {
		clientIdentifiers[name] = "generated client type"
		enumType, ok := generatedType.(*goEnumType)
		if !ok {
			continue
		}
		clientIdentifiers["All"+enumType.GoName] = "generated enum values variable"
		for _, enumValue := range enumType.Values {
			clientIdentifiers[enumValue.GoName] = "generated enum value"
		}
	}
	for _, operation := range g.Operations {
		clientIdentifiers[operation.Name] = "generated operation function"
		clientIdentifiers[operation.Name+"_Operation"] = "generated operation document"
	}

	for _, operation := range g.Operations {
		if !token.IsExported(operation.Name) {
			return errorf(
				nil,
				"test handler operation %q must begin with an uppercase letter",
				operation.Name,
			)
		}
		if !token.IsExported(operation.ResponseName) {
			return errorf(
				nil,
				"test handler response type %q must be exported",
				operation.ResponseName,
			)
		}

		variablesName := operation.Name + "Variables"
		if operation.Input != nil {
			clientSource := clientIdentifiers[variablesName]
			if clientSource != "" {
				return errorf(
					nil,
					"generated client variables alias %q conflicts with %s",
					variablesName,
					clientSource,
				)
			}
			clientIdentifiers[variablesName] = "generated client variables alias"
		}
		err := addIdentifier(variablesName, operation.Name+" variables")
		if err != nil {
			return err
		}

		expectationName := operation.Name + "Expectation"
		err = addIdentifier(expectationName, operation.Name+" expectation")
		if err != nil {
			return err
		}

		responseName := operation.Name + "Response"
		if responseName != operation.ResponseName {
			err = addIdentifier(responseName, operation.Name+" response alias")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func newTestHandlerRenderer(plan *generationPlan) (*generator, testHandlerTemplateData, error) {
	clientGenerator := plan.generator
	config := *clientGenerator.Config
	config.Generated = config.TestHandlerGenerated
	config.Package = config.testHandlerPackage
	config.pkgPath = config.testHandlerPkgPath

	handlerGenerator := newGenerator(&config, clientGenerator.schema, nil)
	data := testHandlerTemplateData{
		Generator:  handlerGenerator,
		Package:    config.Package,
		Types:      make([]testHandlerType, 0, len(clientGenerator.typeMap)),
		Operations: make([]testHandlerOperation, 0, len(clientGenerator.Operations)),
	}

	typeNames := make([]string, 0, len(clientGenerator.typeMap))
	for name := range clientGenerator.typeMap {
		if token.IsExported(name) {
			typeNames = append(typeNames, name)
		}
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		handlerType := testHandlerType{Name: name, ClientName: name}
		enumType, ok := clientGenerator.typeMap[name].(*goEnumType)
		if ok {
			handlerType.EnumValues = enumType.Values
		}
		data.Types = append(data.Types, handlerType)
		handlerGenerator.usedAliases[name] = true
	}

	for index, operation := range clientGenerator.Operations {
		responseName := operation.Name + "Response"
		if responseName != operation.ResponseName && !containsTestHandlerType(data.Types, responseName) {
			data.Types = append(data.Types, testHandlerType{
				Name:       responseName,
				ClientName: operation.ResponseName,
			})
		}
		handlerOperation := testHandlerOperation{
			Name:                operation.Name,
			FieldName:           operationFieldName(index),
			VariablesName:       operation.Name + "Variables",
			ClientVariablesName: operation.Name + "Variables",
			ResponseName:        responseName,
			ClientResponseName:  operation.ResponseName,
			HasVariables:        operation.Input != nil,
		}
		data.Operations = append(data.Operations, handlerOperation)
		handlerGenerator.usedAliases[handlerOperation.VariablesName] = true
		handlerGenerator.usedAliases[operation.Name+"Expectation"] = true
	}
	sort.Slice(data.Types, func(i, j int) bool {
		return data.Types[i].Name < data.Types[j].Name
	})

	for _, name := range testHandlerReservedNames {
		handlerGenerator.usedAliases[name] = true
	}

	clientPackage := allocateIdentifier(clientGenerator.Config.Package, handlerGenerator.usedAliases)
	handlerGenerator.imports[clientGenerator.Config.pkgPath] = clientPackage
	data.ClientPackage = clientPackage

	for _, reference := range []string{
		"bytes.NewReader",
		"encoding/json.Decoder",
		"errors.Is",
		"fmt.Errorf",
		"io.EOF",
		"net/http.Handler",
		"strconv.Itoa",
		"sync.Mutex",
		"testing.TB",
		"time.Duration",
		"github.com/willabides/octoql.Error",
	} {
		_, err := handlerGenerator.ref(reference)
		if err != nil {
			return nil, testHandlerTemplateData{}, err
		}
	}

	return handlerGenerator, data, nil
}

func containsTestHandlerType(types []testHandlerType, name string) bool {
	for _, handlerType := range types {
		if handlerType.Name == name {
			return true
		}
	}
	return false
}

func operationFieldName(index int) string {
	return "operation" + strconv.Itoa(index)
}

func renderTestHandler(plan *generationPlan) ([]byte, error) {
	handlerGenerator, data, err := newTestHandlerRenderer(plan)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	err = handlerGenerator.render("test_handler.go.tmpl", &buffer, data)
	if err != nil {
		return nil, err
	}

	unformatted := buffer.Bytes()
	formatted, err := format.Source(unformatted)
	if err != nil {
		return nil, goSourceError("gofmt test handler", unformatted, err)
	}
	imported, err := imports.Process(
		plan.generator.Config.TestHandlerGenerated,
		formatted,
		nil,
	)
	if err != nil {
		return nil, goSourceError("goimports test handler", formatted, err)
	}
	return imported, nil
}
