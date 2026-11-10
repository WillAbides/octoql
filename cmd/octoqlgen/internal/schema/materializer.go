// Package schema verifies and materializes pinned GraphQL schemas.
package schema

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
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

	err = publish(
		request.Path,
		data,
		request.SHA256,
	)
	if err != nil {
		return nil, err
	}
	err = publishDirectiveFile(request.Path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func publishDirectiveFile(schemaPath string) error {
	path := filepath.Join(filepath.Dir(schemaPath), internal.DirectiveFilename)
	wanted := []byte(internal.DirectiveFileContents)

	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return publish(path, wanted, "")
	case err != nil:
		return fmt.Errorf("reading directive file %q: %w", path, err)
	}
	if bytes.Equal(existing, wanted) {
		return nil
	}
	err = os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("replacing directive file %q: %w", path, err)
	}
	return publish(path, wanted, "")
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

func publish(destination string, data []byte, expectedSHA256 string) (err error) {
	directory := filepath.Dir(destination)
	err = os.MkdirAll(directory, 0o755)
	if err != nil {
		return fmt.Errorf("creating schema directory %q: %w", directory, err)
	}

	temp, err := os.CreateTemp(directory, "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary schema file: %w", err)
	}
	isClosed := false
	defer func() {
		if !isClosed {
			err = errors.Join(err, temp.Close())
		}
		err = errors.Join(err, os.Remove(temp.Name()))
	}()

	_, err = io.Copy(temp, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("writing temporary schema file: %w", err)
	}
	err = temp.Sync()
	if err != nil {
		return fmt.Errorf("syncing temporary schema file: %w", err)
	}
	err = temp.Close()
	isClosed = true
	if err != nil {
		return fmt.Errorf("closing temporary schema file: %w", err)
	}

	err = os.Link(temp.Name(), destination)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("publishing schema file %q: %w", destination, err)
	}

	winner, readErr := os.ReadFile(destination)
	if readErr != nil {
		return fmt.Errorf("reading concurrently materialized schema file %q: %w", destination, readErr)
	}
	checksumErr := verifyChecksum(winner, expectedSHA256)
	if checksumErr != nil {
		return checksumErr
	}
	if !bytes.Equal(winner, data) {
		return errors.New("concurrently materialized schema has different bytes")
	}
	return nil
}
