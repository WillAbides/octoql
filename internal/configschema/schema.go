// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// Package configschema converts the hand-maintained octoql YAML Schema to JSON.
package configschema

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// JSONDocument converts a YAML JSON Schema document to indented JSON.
func JSONDocument(yamlDocument []byte) ([]byte, error) {
	var document any
	err := yaml.Unmarshal(yamlDocument, &document)
	if err != nil {
		return nil, fmt.Errorf("decoding YAML Schema: %w", err)
	}

	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding JSON Schema: %w", err)
	}
	return append(content, '\n'), nil
}
