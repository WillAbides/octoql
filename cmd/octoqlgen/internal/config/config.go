// Package config loads octoqlgen configuration files.
package config

//go:generate ../../../../script/jsonschematogo --package config --output model_gen.go ../../../../octoqlgen.schema.yaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"
)

const DefaultFilename = "octoqlgen.yaml"

const (
	TestHandlerTypesClient = "client"
	TestHandlerTypesLocal  = "local"
)

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
	loaded, err := LoadBytes(absoluteFilename, content)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}
	return loaded, nil
}

// LoadBytes decodes configuration bytes using filename to resolve relative
// paths without rereading the file.
func LoadBytes(filename string, content []byte) (*Config, error) {
	if filename == "" {
		filename = DefaultFilename
	}
	absoluteFilename, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}
	loaded, err := Parse(content)
	if err != nil {
		return nil, err
	}
	loaded.resolvePaths(filepath.Dir(absoluteFilename))
	return loaded, nil
}

// Parse decodes one octoqlgen configuration document without resolving paths.
func Parse(content []byte) (*Config, error) {
	err := requireSingleYAMLDocument(content)
	if err != nil {
		return nil, err
	}
	return decodeConfig(content)
}

func requireSingleYAMLDocument(content []byte) error {
	decoder := yamlv3.NewDecoder(bytes.NewReader(content))
	var document yamlv3.Node
	err := decoder.Decode(&document)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}

	var extra yamlv3.Node
	err = decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("multiple YAML documents are not allowed")
}

func decodeConfig(content []byte) (*Config, error) {
	loaded := &Config{}
	err := yaml.UnmarshalStrict(content, loaded)
	if err != nil {
		return nil, err
	}
	if loaded.TestHandler != nil && loaded.TestHandler.Types != nil {
		switch *loaded.TestHandler.Types {
		case TestHandlerTypesClient, TestHandlerTypesLocal:
		default:
			return nil, fmt.Errorf(
				"test_handler.types must be one of %q or %q",
				TestHandlerTypesClient,
				TestHandlerTypesLocal,
			)
		}
	}
	return loaded, nil
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
	if c.ExportOperations != nil {
		resolved := resolvePath(baseDir, *c.ExportOperations)
		c.ExportOperations = &resolved
	}
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

func (c *Config) ExportOperationsPath() string {
	if c.ExportOperations == nil || *c.ExportOperations == "" {
		return ""
	}
	return filepath.Clean(*c.ExportOperations)
}

func (c *Config) TestHandlerGeneratedPath() string {
	if c.TestHandler == nil || c.TestHandler.Generated == "" {
		return ""
	}
	return filepath.Clean(c.TestHandler.Generated)
}

func (c *Config) TestHandlerTypesValue() string {
	if c.TestHandler == nil || c.TestHandler.Types == nil {
		return TestHandlerTypesClient
	}
	return *c.TestHandler.Types
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
