// Package schema verifies and materializes pinned GraphQL schemas.
package schema

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/internal"
)

const (
	DefaultMaxResponseBytes int64 = 32 << 20
	DefaultTimeout                = 30 * time.Second
)

type Request struct {
	Source config.Source
	Path   string
	SHA256 string
}

type Materializer struct {
	HTTPClient            httpDoer
	CommandRunner         commandRunner
	LookupEnvironment     func(string) (string, bool)
	GitHubAPIBaseURL      func(string) string
	GitHubGraphQLEndpoint func(string) string
	MaxResponseBytes      int64
	Timeout               time.Duration
}

func NewMaterializer() *Materializer {
	return &Materializer{
		HTTPClient:            &httpClient{},
		CommandRunner:         execRunner{},
		LookupEnvironment:     os.LookupEnv,
		GitHubAPIBaseURL:      defaultGitHubAPIBaseURL,
		GitHubGraphQLEndpoint: defaultGitHubGraphQLEndpoint,
		MaxResponseBytes:      DefaultMaxResponseBytes,
		Timeout:               DefaultTimeout,
	}
}

func (m *Materializer) Materialize(ctx context.Context, request *Request) ([]byte, error) {
	if isRemote(request.Source) && request.SHA256 == "" {
		return nil, errors.New("schema sha256 is required for remote sources")
	}

	deps := m.dependencies()
	if request.Path != "" {
		existing, readErr := os.ReadFile(request.Path)
		if readErr == nil {
			err := verifyChecksum(existing, request.SHA256)
			if err != nil {
				return nil, err
			}
			err = validateSDL(existing)
			if err != nil {
				return nil, err
			}
			err = publishDirectiveFile(request.Path)
			if err != nil {
				return nil, err
			}
			return existing, nil
		}
		if !errors.Is(readErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("reading schema file %q: %w", request.Path, readErr)
		}
	}

	if !isRemote(request.Source) {
		path := request.Path
		if path == "" {
			path = "<unspecified>"
		}
		return nil, fmt.Errorf("local schema file %q does not exist", path)
	}

	timeout := deps.timeout
	fetchContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	data, err := m.fetch(fetchContext, request.Source, &deps)
	if err != nil {
		return nil, err
	}
	err = verifyChecksum(data, request.SHA256)
	if err != nil {
		return nil, err
	}
	err = validateSDL(data)
	if err != nil {
		return nil, err
	}

	if request.Path == "" {
		return data, nil
	}

	err = WriteFile(request.Path, data)
	if err != nil {
		return nil, err
	}
	err = publishDirectiveFile(request.Path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// WriteFile creates destination's parent directory if needed and writes data.
func WriteFile(destination string, data []byte) error {
	directory := filepath.Dir(destination)
	err := os.MkdirAll(directory, 0o755)
	if err != nil {
		return fmt.Errorf("creating directory %q: %w", directory, err)
	}
	err = os.WriteFile(destination, data, 0o644)
	if err != nil {
		return fmt.Errorf("writing file %q: %w", destination, err)
	}
	return nil
}

// WriteDirectiveFile writes octoqlgen's companion directive declaration beside
// schemaPath using write.
func WriteDirectiveFile(write func(string, []byte) error, schemaPath string) error {
	path := filepath.Join(filepath.Dir(schemaPath), internal.DirectiveFilename)
	err := write(path, []byte(internal.DirectiveFileContents))
	if err != nil {
		return fmt.Errorf("writing directive file %q: %w", path, err)
	}
	return nil
}

func publishDirectiveFile(schemaPath string) error {
	return WriteDirectiveFile(writeIfChanged, schemaPath)
}

func writeIfChanged(path string, wanted []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, wanted) {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading existing file %q: %w", path, err)
	}
	return WriteFile(path, wanted)
}

type dependencies struct {
	httpClient            httpDoer
	commandRunner         commandRunner
	lookupEnvironment     func(string) (string, bool)
	githubAPIBaseURL      func(string) string
	githubGraphQLEndpoint func(string) string
	maxResponseBytes      int64
	timeout               time.Duration
}

func (m *Materializer) dependencies() dependencies {
	defaults := NewMaterializer()
	deps := dependencies{
		httpClient:            cmp.Or(m.HTTPClient, defaults.HTTPClient),
		commandRunner:         cmp.Or(m.CommandRunner, defaults.CommandRunner),
		lookupEnvironment:     m.LookupEnvironment,
		githubAPIBaseURL:      m.GitHubAPIBaseURL,
		githubGraphQLEndpoint: m.GitHubGraphQLEndpoint,
		maxResponseBytes:      m.MaxResponseBytes,
		timeout:               cmp.Or(m.Timeout, defaults.Timeout),
	}
	if deps.lookupEnvironment == nil {
		deps.lookupEnvironment = defaults.LookupEnvironment
	}
	if deps.githubAPIBaseURL == nil {
		deps.githubAPIBaseURL = defaults.GitHubAPIBaseURL
	}
	if deps.githubGraphQLEndpoint == nil {
		deps.githubGraphQLEndpoint = defaults.GitHubGraphQLEndpoint
	}
	if deps.maxResponseBytes <= 0 {
		deps.maxResponseBytes = defaults.MaxResponseBytes
	}
	return deps
}

func verifyChecksum(data []byte, expected string) error {
	if expected == "" {
		return nil
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != expected {
		return fmt.Errorf("schema checksum mismatch: expected %s, actual %s", expected, actual)
	}
	return nil
}

func validateSDL(data []byte) error {
	source := &ast.Source{
		Name:  "schema.graphql",
		Input: string(data),
	}
	_, err := gqlparser.LoadSchema(source)
	if err != nil {
		return fmt.Errorf("invalid graphql schema: %w", err)
	}
	return nil
}

func isRemote(source config.Source) bool {
	return source.Repository != "" || source.Path != "" || source.Revision != ""
}
