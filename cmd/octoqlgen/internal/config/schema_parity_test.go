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

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/internal/configschema"
	"gopkg.in/yaml.v3"
)

const schemaFixtureDir = "testdata/schema"

func TestJSONSchemaParity(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(configschema.JSONDocument()))
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
