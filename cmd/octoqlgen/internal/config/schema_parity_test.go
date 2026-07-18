// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	schemaFixtureDir = "testdata/schema"
	schemaOutputPath = "../../../../schema/octoql.schema.json"
)

func TestJSONSchemaParity(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()

	schemaContent, err := os.ReadFile(schemaOutputPath)
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaContent))
	require.NoError(t, err)
	err = compiler.AddResource("octoql.schema.json", document)
	require.NoError(t, err)

	schema, err := compiler.Compile("octoql.schema.json")
	require.NoError(t, err)

	fixtures, err := os.ReadDir(schemaFixtureDir)
	require.NoError(t, err)
	for _, fixture := range fixtures {
		if fixture.IsDir() || filepath.Ext(fixture.Name()) != ".yaml" {
			continue
		}

		t.Run(strings.TrimSuffix(fixture.Name(), ".yaml"), func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(schemaFixtureDir, fixture.Name()))
			require.NoError(t, err)

			var yamlInstance any
			err = yaml.Unmarshal(content, &yamlInstance)
			require.NoError(t, err)
			jsonInstance, err := json.Marshal(yamlInstance)
			require.NoError(t, err)
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonInstance))
			require.NoError(t, err)
			schemaError := schema.Validate(instance)

			filename := filepath.Join(t.TempDir(), DefaultFilename)
			err = os.WriteFile(filename, content, 0o600)
			require.NoError(t, err)
			_, configError := Load(filename)

			isValid := strings.HasPrefix(fixture.Name(), "valid-")
			require.Equal(
				t,
				isValid,
				schemaError == nil,
				"JSON Schema result disagrees with fixture expectation: %v",
				schemaError,
			)
			require.Equal(
				t,
				isValid,
				configError == nil,
				"config loader result disagrees with fixture expectation: %v",
				configError,
			)
		})
	}
}

func TestURLPatternUsesECMAScriptSyntax(t *testing.T) {
	schemaContent, err := os.ReadFile(schemaOutputPath)
	require.NoError(t, err)

	var schemaDocument struct {
		Definitions map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}
	err = json.Unmarshal(schemaContent, &schemaDocument)
	require.NoError(t, err)

	urlPattern := schemaDocument.Definitions["url"].Pattern
	pattern, err := regexp2.Compile(urlPattern, regexp2.ECMAScript)
	require.NoError(t, err)

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{
			name:  "https URL",
			value: "https://example.com/schema.graphql",
			valid: true,
		},
		{
			name:  "uppercase HTTPS URL",
			value: "HTTPS://example.com/schema.graphql",
			valid: true,
		},
		{
			name:  "URL with whitespace",
			value: "https://example.com/schema graphql",
		},
		{
			name:  "URL with credentials",
			value: "https://user@example.com/schema.graphql",
		},
		{
			name:  "URL with empty host query",
			value: "https://?query",
		},
		{
			name:  "URL with empty host fragment",
			value: "https://#fragment",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matched, err := pattern.MatchString(test.value)
			require.NoError(t, err)
			require.Equal(t, test.valid, matched)
		})
	}
}
