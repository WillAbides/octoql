// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// Package config loads octoql configuration files and validates schema sources.
package config

//go:generate ../../../../script/jsonschematogo --package config --output model_gen.go ../../../../octoql.schema.yaml

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

const DefaultFilename = "octoql.yaml"

func Load(filename string) (*Config, error) {
	if filename == "" {
		filename = DefaultFilename
	}

	absoluteFilename, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}
	content, err := os.ReadFile(absoluteFilename)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", filename, err)
	}

	loaded, err := decodeConfig(content)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	loaded.resolvePaths(filepath.Dir(absoluteFilename))
	return loaded, nil
}

func decodeConfig(content []byte) (*Config, error) {
	loaded := &Config{}
	err := yaml.UnmarshalStrict(content, loaded)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func (c *Config) resolvePaths(baseDir string) {
	c.Schema.Path = resolvePath(baseDir, c.Schema.Path)
	for index, operation := range c.Operations {
		c.Operations[index] = resolvePath(baseDir, operation)
	}
	c.Generated = resolvePath(baseDir, c.Generated)
	c.TestHandler.Generated = resolvePath(baseDir, c.TestHandler.Generated)
}

func (c *Config) SchemaPath() string {
	return filepath.Clean(c.Schema.Path)
}

func (c *Config) OperationPaths() []string {
	return append([]string{}, c.Operations...)
}

func (c *Config) GeneratedPath() string {
	return filepath.Clean(c.Generated)
}

func (c *Config) TestHandlerGeneratedPath() string {
	return filepath.Clean(c.TestHandler.Generated)
}

func resolvePath(baseDir, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(baseDir, path)
}
