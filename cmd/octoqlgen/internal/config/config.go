// Package config loads octoqlgen configuration files.
package config

//go:generate ../../../../script/jsonschematogo --yaml-tags --package config --output model_gen.go ../../../../octoqlgen.schema.yaml
//go:generate go run ../../../../internal/configdocgen --schema ../../../../octoqlgen.schema.yaml --output ../../../../docs/configuration.md

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"
)

const DefaultFilename = "octoqlgen.yaml"

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

	loaded, err := Parse(content)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	loaded.resolvePaths(filepath.Dir(absoluteFilename))
	return loaded, nil
}

// Parse decodes one octoqlgen configuration document without resolving paths.
func Parse(content []byte) (*Config, error) {
	loaded := &Config{}
	err := yaml.Load(content, loaded, yaml.WithKnownFields())
	if err != nil {
		return nil, err
	}
	err = validateRequiredFields(content, loaded)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func validateRequiredFields(content []byte, loaded *Config) error {
	var fields map[string]any
	err := yaml.Load(content, &fields)
	if err != nil {
		return err
	}
	_, hasSchema := fields["schema"]
	if !hasSchema {
		return errors.New("schema is required")
	}
	if loaded.Schema.Path == "" {
		return errors.New("schema.path is required")
	}
	if loaded.Operations == nil {
		return errors.New("operations is required")
	}
	if loaded.Generated == "" {
		return errors.New("generated is required")
	}
	if loaded.TestHandler != nil && loaded.TestHandler.Generated == "" {
		return errors.New("test_handler.generated is required")
	}
	if loaded.Bindings != nil {
		for name, goType := range *loaded.Bindings {
			if goType == "" {
				return fmt.Errorf("bindings.%s must not be empty", name)
			}
		}
	}
	source := loaded.Schema.Source
	if source == nil {
		return nil
	}
	if source.Repository == "" {
		return errors.New("schema.source.repository is required")
	}
	if source.Revision == "" {
		return errors.New("schema.source.revision is required")
	}
	if source.Path == "" {
		return errors.New("schema.source.path is required")
	}
	return nil
}

func (s *Schema) SHA256Value() string {
	if s.Sha256 == nil {
		return ""
	}
	return *s.Sha256
}

func (s *Schema) SourceValue() Source {
	if s.Source == nil {
		return Source{}
	}
	return *s.Source
}

func (c *Config) resolvePaths(baseDir string) {
	c.Schema.Path = resolvePath(baseDir, c.Schema.Path)
	for index, operation := range c.Operations {
		c.Operations[index] = resolvePath(baseDir, operation)
	}
	c.Generated = resolvePath(baseDir, c.Generated)
	if c.TestHandler != nil {
		c.TestHandler.Generated = resolvePath(baseDir, c.TestHandler.Generated)
	}
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
	if c.TestHandler == nil || c.TestHandler.Generated == "" {
		return ""
	}
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
