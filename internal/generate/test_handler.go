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
	Name          string
	FieldName     string
	VariablesName string
	VariablesType string
	ResponseName  string
	HasVariables  bool
}

type testHandlerTemplateData struct {
	Generator            *generator
	Package              string
	ClientPackage        string
	LocalTypes           bool
	LocalTypeDefinitions string
	LocalAliases         []testHandlerType
	Types                []testHandlerType
	Operations           []testHandlerOperation
}

var testHandlerReservedNames = []string{
	"ExpectOption",
	"MinTimes",
	"NewTestHandler",
	"ResponseOption",
	"TestHandler",
	"Times",
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
	if plan.config.TestHandlerGenerated == "" {
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

	localTypes := plan.config.TestHandlerTypes == TestHandlerTypesLocal
	typeNames := make([]string, 0, len(plan.typeMap))
	for name := range plan.typeMap {
		if !localTypes && !token.IsExported(name) {
			continue
		}
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		typeSource := "client type alias"
		if localTypes {
			typeSource = "local type"
		}
		err := addIdentifier(name, typeSource)
		if err != nil {
			return err
		}
		enumType, ok := plan.typeMap[name].(*goEnumType)
		if !ok {
			continue
		}
		for _, enumValue := range enumType.Values {
			enumValueSource := "client enum value alias"
			if localTypes {
				enumValueSource = "local enum value"
			}
			err = addIdentifier(enumValue.GoName, enumValueSource)
			if err != nil {
				return err
			}
		}
	}

	for _, operation := range plan.operations {
		if !localTypes && !token.IsExported(operation.Name) {
			return errorf(
				nil,
				"test handler operation %q must begin with an uppercase letter",
				operation.Name,
			)
		}
		if !localTypes && !token.IsExported(operation.ResponseName) {
			return errorf(
				nil,
				"test handler response type %q must be exported",
				operation.ResponseName,
			)
		}

		expectationName := operation.Name + "Expectation"
		err := addIdentifier(expectationName, operation.Name+" expectation")
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
	config := plan.config
	config.Generated = config.TestHandlerGenerated
	config.Package = config.testHandlerPackage
	config.pkgPath = config.testHandlerPkgPath

	localTypes := config.TestHandlerTypes == TestHandlerTypesLocal
	forbiddenImportPath := ""
	if localTypes {
		forbiddenImportPath = plan.config.pkgPath
	}
	handlerGenerator, err := plan.newRenderer(
		&config,
		false,
		forbiddenImportPath,
	)
	if err != nil {
		return nil, testHandlerTemplateData{}, err
	}
	data := testHandlerTemplateData{
		Generator:  handlerGenerator,
		Package:    config.Package,
		LocalTypes: localTypes,
		LocalAliases: make(
			[]testHandlerType,
			0,
			len(plan.operations),
		),
		Types:      make([]testHandlerType, 0, len(plan.typeMap)),
		Operations: make([]testHandlerOperation, 0, len(plan.operations)),
	}

	switch {
	case localTypes:
		typeDefinitions, err := renderTypeDefinitions(handlerGenerator)
		if err != nil {
			return nil, testHandlerTemplateData{}, err
		}
		data.LocalTypeDefinitions = string(typeDefinitions)
	default:
		typeNames := make([]string, 0, len(plan.typeMap))
		for name := range plan.typeMap {
			if token.IsExported(name) {
				typeNames = append(typeNames, name)
			}
		}
		sort.Strings(typeNames)
		for _, name := range typeNames {
			handlerType := testHandlerType{Name: name, ClientName: name}
			enumType, ok := plan.typeMap[name].(*goEnumType)
			if ok {
				handlerType.EnumValues = enumType.Values
				for _, enumValue := range enumType.Values {
					handlerGenerator.usedAliases[enumValue.GoName] = true
				}
			}
			data.Types = append(data.Types, handlerType)
			handlerGenerator.usedAliases[name] = true
		}
	}

	for index, operation := range plan.operations {
		responseName := operation.Name + "Response"
		if responseName != operation.ResponseName {
			responseAlias := testHandlerType{
				Name:       responseName,
				ClientName: operation.ResponseName,
			}
			switch {
			case localTypes:
				data.LocalAliases = append(data.LocalAliases, responseAlias)
			case !containsTestHandlerType(data.Types, responseName):
				data.Types = append(data.Types, responseAlias)
			}
			handlerGenerator.usedAliases[responseName] = true
		}
		handlerOperation := testHandlerOperation{
			Name:          operation.Name,
			FieldName:     operationFieldName(index),
			VariablesName: operation.Name + "Variables",
			VariablesType: "struct{}",
			ResponseName:  responseName,
			HasVariables:  operation.Input != nil,
		}
		if handlerOperation.HasVariables {
			handlerOperation.VariablesType = handlerOperation.VariablesName
		}
		data.Operations = append(data.Operations, handlerOperation)
		if handlerOperation.HasVariables {
			handlerGenerator.usedAliases[handlerOperation.VariablesName] = true
		}
		handlerGenerator.usedAliases[operation.Name+"Expectation"] = true
		for _, prefix := range []string{"Expect", "Default", "Reset"} {
			handlerGenerator.usedAliases[prefix+operation.Name] = true
		}
	}
	sort.Slice(data.Types, func(i, j int) bool {
		return data.Types[i].Name < data.Types[j].Name
	})

	for _, name := range testHandlerReservedNames {
		handlerGenerator.usedAliases[name] = true
	}

	if !localTypes {
		clientPackage := allocateIdentifier(plan.config.Package, handlerGenerator.usedAliases)
		handlerGenerator.imports[plan.config.pkgPath] = clientPackage
		data.ClientPackage = clientPackage
	}

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
		plan.config.TestHandlerGenerated,
		formatted,
		nil,
	)
	if err != nil {
		return nil, goSourceError("goimports test handler", formatted, err)
	}
	return imported, nil
}
