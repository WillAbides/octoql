// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// Package config loads and validates octoql configuration files.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultFilename = "octoql.yaml"

var (
	canonicalSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern          = regexp.MustCompile(`^[0-9a-f]{40}$`)
	githubVersionPattern   = regexp.MustCompile(`^(fpt|ghec|ghes-\d+\.\d+)$`)
	repositoryPartPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

//nolint:govet // Keep fields grouped by the configuration document shape.
type Config struct {
	Schema      Schema      `json:"schema"`
	Operations  []string    `json:"operations"`
	TestHandler TestHandler `json:"test_handler"`
	Generated   string      `json:"generated"`
	state       *configState
}

type configState struct {
	baseDir       string
	sourcePresent bool
}

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

	jsonContent, err := yamlToJSON(content)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	config := &Config{
		state: &configState{},
	}
	decoder := json.NewDecoder(bytes.NewReader(jsonContent))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(config)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	config.state.baseDir = filepath.Dir(absoluteFilename)
	sourcePresent, err := schemaSourcePresent(content)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	config.state.sourcePresent = sourcePresent
	err = config.Validate()
	if err != nil {
		return nil, fmt.Errorf("validating config file %q: %w", filename, err)
	}

	return config, nil
}

func yamlToJSON(content []byte) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(content))

	var document any
	err := decoder.Decode(&document)
	if err != nil {
		return nil, err
	}

	var extra any
	err = decoder.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple yaml documents are not allowed")
		}
		return nil, err
	}

	jsonContent, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("converting YAML to JSON: %w", err)
	}
	return jsonContent, nil
}

func (c *Config) Validate() error {
	if c.Schema.Path == "" {
		return errors.New("schema.path is required")
	}
	if len(c.Operations) == 0 {
		return errors.New("operations must contain at least one glob")
	}
	if slices.Contains(c.Operations, "") {
		return errors.New("operations must not contain an empty glob")
	}
	if c.Generated == "" {
		return errors.New("generated is required")
	}
	if c.TestHandler.Generated == "" {
		return errors.New("test_handler.generated is required")
	}

	sourcePresent := c.state != nil && c.state.sourcePresent
	err := validateSource(c.Schema.Source, c.Schema.SHA256, sourcePresent)
	if err != nil {
		return err
	}

	return nil
}

func ValidateSource(source Source, sha256 string) error {
	return validateSource(source, sha256, false)
}

func validateSource(source Source, sha256 string, sourcePresent bool) error {
	sourceCount := 0
	if source.GitHubDocs != nil {
		sourceCount++
	}
	if source.GitHubRepository != nil {
		sourceCount++
	}
	if source.URL != nil {
		sourceCount++
	}
	if sourceCount > 1 || (sourcePresent && sourceCount == 0) {
		return errors.New("schema.source must set exactly one remote source variant")
	}

	isRemote := sourceCount == 1
	if isRemote && sha256 == "" {
		return errors.New("schema.sha256 is required for remote sources")
	}
	if sha256 != "" && !canonicalSHA256Pattern.MatchString(sha256) {
		return errors.New("schema.sha256 must be a canonical 64-character lowercase hexadecimal sha-256")
	}

	if source.GitHubDocs != nil {
		err := source.GitHubDocs.Validate()
		if err != nil {
			return fmt.Errorf("schema.source.github_docs: %w", err)
		}
	}
	if source.GitHubRepository != nil {
		err := source.GitHubRepository.Validate()
		if err != nil {
			return fmt.Errorf("schema.source.github_repository: %w", err)
		}
	}
	if source.URL != nil {
		err := validateURL(*source.URL)
		if err != nil {
			return fmt.Errorf("schema.source.url: %w", err)
		}
	}

	return nil
}

func schemaSourcePresent(content []byte) (bool, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	err := decoder.Decode(&document)
	if err != nil {
		return false, err
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return false, nil
	}

	root := document.Content[0]
	schema := effectiveMappingValue(root, "schema")
	if schema == nil {
		return false, nil
	}
	return effectiveMappingValue(schema, "source") != nil, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	for index := 0; index < len(node.Content); index += 2 {
		mappingKey := resolveAlias(node.Content[index])
		if mappingKey != nil && mappingKey.Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func effectiveMappingValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	value := mappingValue(node, key)
	if value != nil {
		return value
	}

	for index := 0; index < len(node.Content); index += 2 {
		mappingKey := resolveAlias(node.Content[index])
		if mappingKey == nil || mappingKey.Value != "<<" {
			continue
		}

		value = effectiveMergeValue(node.Content[index+1], key)
		if value != nil {
			return value
		}
	}
	return nil
}

func effectiveMergeValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveAlias(node)
	if node == nil {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			value := effectiveMappingValue(item, key)
			if value != nil {
				return value
			}
		}
		return nil
	}
	return effectiveMappingValue(node, key)
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.AliasNode {
		return node.Alias
	}
	return node
}

func (g GitHubDocs) Validate() error {
	if !githubVersionPattern.MatchString(g.Version) {
		return errors.New("version must be fpt, ghec, or ghes-X.Y")
	}
	if !commitPattern.MatchString(g.Revision) {
		return errors.New("revision must be a full lowercase hexadecimal commit sha")
	}
	return nil
}

func (g *GitHubRepository) Validate() error {
	parts := strings.Split(g.Repository, "/")
	if len(parts) != 2 ||
		!repositoryPartPattern.MatchString(parts[0]) ||
		!repositoryPartPattern.MatchString(parts[1]) ||
		parts[0] == "." ||
		parts[0] == ".." ||
		parts[1] == "." ||
		parts[1] == ".." {
		return errors.New("repository must be an owner/name pair")
	}
	if !commitPattern.MatchString(g.Revision) {
		return errors.New("revision must be a full lowercase hexadecimal commit sha")
	}

	cleanPath := filepath.ToSlash(filepath.Clean(g.Path))
	isInvalidPath := g.Path == "" ||
		strings.HasPrefix(g.Path, "/") ||
		cleanPath == "." ||
		cleanPath == ".." ||
		strings.HasPrefix(cleanPath, "../") ||
		cleanPath != g.Path
	if isInvalidPath {
		return errors.New("path must be a repository-relative file path without traversal")
	}
	if strings.Contains(g.Path, `\`) {
		return errors.New("path must use forward slashes")
	}

	if g.Host == "" {
		g.Host = "github.com"
	}
	err := validateHost(g.Host)
	if err != nil {
		return fmt.Errorf("host: %w", err)
	}

	return nil
}

func (c *Config) SchemaPath() string {
	return resolvePath(c.baseDir(), c.Schema.Path)
}

func (c *Config) OperationPaths() []string {
	paths := make([]string, 0, len(c.Operations))
	for _, operation := range c.Operations {
		paths = append(paths, resolvePath(c.baseDir(), operation))
	}
	return paths
}

func (c *Config) GeneratedPath() string {
	return resolvePath(c.baseDir(), c.Generated)
}

func (c *Config) TestHandlerGeneratedPath() string {
	return resolvePath(c.baseDir(), c.TestHandler.Generated)
}

func (c *Config) baseDir() string {
	if c.state == nil {
		return ""
	}
	return c.state.baseDir
}

func validateURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("host is required")
	}
	if parsed.User != nil {
		return errors.New("credentials in urls are not allowed")
	}
	return nil
}

func validateHost(host string) error {
	parsed, err := url.Parse("https://" + host)
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}
	if parsed.Host != host || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" {
		return errors.New("must contain only a hostname and optional port")
	}
	return nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(baseDir, path)
}
