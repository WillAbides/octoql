package generate

import (
	"bytes"
	"go/token"
	"sort"
	"strconv"
)

type testHandlerOperation struct {
	Name          string
	FieldName     string
	VariablesName string
	VariablesType string
	ResponseName  string
	HasVariables  bool
}

type testHandlerTemplateData struct {
	ErrorType            string
	RateLimitType        string
	LocalTypes           bool
	LocalTypeDefinitions string
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
	"_octoqlNoUnmarshalJSON",
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

func newTestHandlerRenderer(plan *generationPlan) (*renderer, testHandlerTemplateData, error) {
	localTypes := plan.config.TestHandlerTypes == TestHandlerTypesLocal
	forbiddenPkgPath := ""
	if localTypes {
		forbiddenPkgPath = plan.config.pkgPath
	}
	r := newRenderer(
		plan.config.testHandlerPackage,
		plan.config.testHandlerPkgPath,
		forbiddenPkgPath,
	)

	data := testHandlerTemplateData{
		ErrorType:     "Error",
		RateLimitType: "RateLimit",
		LocalTypes:    localTypes,
		Operations:    make([]testHandlerOperation, 0, len(plan.operations)),
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
		r.imports.usedAliases[name] = true
		return nil
	}
	for _, name := range testHandlerReservedNames {
		err := addIdentifier(name, "test handler runtime")
		if err != nil {
			return nil, testHandlerTemplateData{}, err
		}
	}
	if localTypes {
		for _, name := range []string{"Error", "ErrorType", "Location", "Path", "RateLimit"} {
			err := addIdentifier(name, "test handler runtime")
			if err != nil {
				return nil, testHandlerTemplateData{}, err
			}
		}
		typeNames := make([]string, 0, len(plan.typeMap))
		for name, typ := range plan.typeMap {
			if token.IsExported(name) {
				typeNames = append(typeNames, name)
				continue
			}
			r.imports.usedAliases[name] = true
			enumType, ok := typ.(*goEnumType)
			if ok {
				for _, enumValue := range enumType.Values {
					r.imports.usedAliases[enumValue.GoName] = true
				}
			}
		}
		sort.Strings(typeNames)
		for _, name := range typeNames {
			err := addIdentifier(name, "test handler type")
			if err != nil {
				return nil, testHandlerTemplateData{}, err
			}
			enumType, ok := plan.typeMap[name].(*goEnumType)
			if ok {
				for _, enumValue := range enumType.Values {
					err = addIdentifier(enumValue.GoName, "test handler enum value")
					if err != nil {
						return nil, testHandlerTemplateData{}, err
					}
				}
			}
		}
	}

	for _, op := range plan.operations {
		if !token.IsExported(op.Name) {
			return nil, testHandlerTemplateData{}, errorf(
				nil, "test handler operation %q must begin with an uppercase letter", op.Name,
			)
		}
		if !token.IsExported(op.ResponseName) {
			return nil, testHandlerTemplateData{}, errorf(
				nil, "test handler response type %q must be exported", op.ResponseName,
			)
		}
		err := addIdentifier(op.Name+"Expectation", op.Name+" expectation")
		if err != nil {
			return nil, testHandlerTemplateData{}, err
		}
		for _, prefix := range []string{"Expect", "Default", "Reset"} {
			r.imports.usedAliases[prefix+op.Name] = true
		}
	}

	typePrefix := ""
	if localTypes {
		err := registerBoundTypeImports(plan.typeMap, r.imports)
		if err != nil {
			return nil, testHandlerTemplateData{}, err
		}
		typeDefinitions, err := renderTypeDefinitions(r, plan.typeMap)
		if err != nil {
			return nil, testHandlerTemplateData{}, err
		}
		data.LocalTypeDefinitions = string(typeDefinitions)
	}
	if !localTypes {
		clientPackage := allocateIdentifier(plan.config.Package, r.imports.usedAliases)
		r.imports.imports[plan.config.pkgPath] = clientPackage
		typePrefix = clientPackage + "."
		data.ErrorType = typePrefix + "Error"
		data.RateLimitType = typePrefix + "RateLimit"
	}

	for index, op := range plan.operations {
		variablesName := typePrefix + op.Name + "Variables"
		handlerOperation := testHandlerOperation{
			Name:          op.Name,
			FieldName:     "operation" + strconv.Itoa(index),
			VariablesName: variablesName,
			VariablesType: "struct{}",
			ResponseName:  typePrefix + op.ResponseName,
			HasVariables:  op.Input != nil,
		}
		if handlerOperation.HasVariables {
			handlerOperation.VariablesType = variablesName
		}
		data.Operations = append(data.Operations, handlerOperation)
	}

	return r, data, nil
}

func renderTestHandler(plan *generationPlan) ([]byte, error) {
	r, data, err := newTestHandlerRenderer(plan)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	err = r.render("test_handler.go.tmpl", &buffer, data)
	if err != nil {
		return nil, err
	}

	return r.formatGoFile(
		"// Code generated by octoqlgen. DO NOT EDIT.",
		" test handler",
		buffer.Bytes(),
	)
}
