// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// Package config loads and validates octoql configuration files.
package config

import (
	"bytes"
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

type Config struct {
	Schema      Schema      `yaml:"schema"`
	Generated   string      `yaml:"generated"`
	TestHandler TestHandler `yaml:"test_handler"`
	baseDir     string
	Operations  []string `yaml:"operations"`
}

type Schema struct {
	Source Source `yaml:"source,omitempty"`
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256,omitempty"`
}

type Source struct {
	GitHubDocs       *GitHubDocs       `yaml:"github_docs,omitempty"`
	GitHubRepository *GitHubRepository `yaml:"github_repository,omitempty"`
	URL              *string           `yaml:"url,omitempty"`
}

type GitHubDocs struct {
	Version  string `yaml:"version"`
	Revision string `yaml:"revision"`
}

type GitHubRepository struct {
	Repository string `yaml:"repository"`
	Revision   string `yaml:"revision"`
	Path       string `yaml:"path"`
	Host       string `yaml:"host,omitempty"`
}

type TestHandler struct {
	Generated string `yaml:"generated"`
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

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)

	config := &Config{}
	err = decoder.Decode(config)
	if err != nil {
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	var extra any
	err = decoder.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decoding config file %q: multiple yaml documents are not allowed", filename)
		}
		return nil, fmt.Errorf("decoding config file %q: %w", filename, err)
	}

	config.baseDir = filepath.Dir(absoluteFilename)
	err = config.Validate()
	if err != nil {
		return nil, fmt.Errorf("validating config file %q: %w", filename, err)
	}

	return config, nil
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

	err := ValidateSource(c.Schema.Source, c.Schema.SHA256)
	if err != nil {
		return err
	}

	return nil
}

func ValidateSource(source Source, sha256 string) error {
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
	if sourceCount > 1 {
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
	return resolvePath(c.baseDir, c.Schema.Path)
}

func (c *Config) OperationPaths() []string {
	paths := make([]string, 0, len(c.Operations))
	for _, operation := range c.Operations {
		paths = append(paths, resolvePath(c.baseDir, operation))
	}
	return paths
}

func (c *Config) GeneratedPath() string {
	return resolvePath(c.baseDir, c.Generated)
}

func (c *Config) TestHandlerGeneratedPath() string {
	return resolvePath(c.baseDir, c.TestHandler.Generated)
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
