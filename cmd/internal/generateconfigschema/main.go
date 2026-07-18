// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// generateconfigschema writes the committed octoql configuration JSON Schema.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/willabides/octoql/internal/configschema"
)

const (
	jsonOutputPath = "schema/octoql.schema.json"
	yamlOutputPath = "schema/octoql.schema.yaml"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	yamlContent, err := configschema.YAMLDocument()
	if err != nil {
		return err
	}
	outputs := []struct {
		path    string
		content []byte
	}{
		{
			path:    jsonOutputPath,
			content: configschema.JSONDocument(),
		},
		{
			path:    yamlOutputPath,
			content: yamlContent,
		},
	}

	err = os.MkdirAll(filepath.Dir(jsonOutputPath), 0o755)
	if err != nil {
		return fmt.Errorf("creating schema directory: %w", err)
	}
	for _, output := range outputs {
		err = writeOutput(output.path, output.content)
		if err != nil {
			return err
		}
	}
	return nil
}

func writeOutput(outputPath string, content []byte) error {
	existing, err := os.ReadFile(outputPath)
	if err == nil && bytes.Equal(existing, content) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", outputPath, err)
	}
	err = os.WriteFile(outputPath, content, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", outputPath, err)
	}
	return nil
}
