// Package schema verifies and materializes pinned GraphQL schemas.
package schema

import (
	"bytes"
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
	HTTPClient        httpDoer
	CommandRunner     commandRunner
	LookupEnvironment func(string) (string, bool)
	FileSystem        fileSystem
	GitHubAPIBaseURL  func(string) string
	MaxResponseBytes  int64
	Timeout           time.Duration
}

func NewMaterializer() *Materializer {
	return &Materializer{
		HTTPClient:        &httpClient{},
		CommandRunner:     execRunner{},
		LookupEnvironment: os.LookupEnv,
		FileSystem:        osFileSystem{},
		GitHubAPIBaseURL:  defaultGitHubAPIBaseURL,
		MaxResponseBytes:  DefaultMaxResponseBytes,
		Timeout:           DefaultTimeout,
	}
}

func (m *Materializer) Materialize(ctx context.Context, request Request) ([]byte, error) {
	sourceCount := sourceVariantCount(request.Source)
	if sourceCount > 1 {
		return nil, errors.New("schema source must not set multiple remote source variants")
	}
	if sourceCount == 1 && request.SHA256 == "" {
		return nil, errors.New("schema sha256 is required for remote sources")
	}

	deps := m.dependencies()
	if request.Path != "" {
		existing, readErr := deps.fileSystem.ReadFile(request.Path)
		if readErr == nil {
			err := verifyChecksum(existing, request.SHA256)
			if err != nil {
				return nil, err
			}
			err = validateSDL(existing)
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
		deps.fileSystem,
		request.Path,
		data,
		request.SHA256,
	)
	if err != nil {
		return nil, err
	}
	return data, nil
}

type dependencies struct {
	httpClient        httpDoer
	commandRunner     commandRunner
	lookupEnvironment func(string) (string, bool)
	fileSystem        fileSystem
	githubAPIBaseURL  func(string) string
	maxResponseBytes  int64
	timeout           time.Duration
}

func (m *Materializer) dependencies() dependencies {
	defaults := NewMaterializer()

	httpClient := m.HTTPClient
	if httpClient == nil {
		httpClient = defaults.HTTPClient
	}
	commandRunner := m.CommandRunner
	if commandRunner == nil {
		commandRunner = defaults.CommandRunner
	}
	lookupEnvironment := m.LookupEnvironment
	if lookupEnvironment == nil {
		lookupEnvironment = defaults.LookupEnvironment
	}
	fsys := m.FileSystem
	if fsys == nil {
		fsys = defaults.FileSystem
	}
	githubAPIBaseURL := m.GitHubAPIBaseURL
	if githubAPIBaseURL == nil {
		githubAPIBaseURL = defaults.GitHubAPIBaseURL
	}
	maxResponseBytes := m.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaults.MaxResponseBytes
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = defaults.Timeout
	}

	return dependencies{
		httpClient:        httpClient,
		commandRunner:     commandRunner,
		lookupEnvironment: lookupEnvironment,
		fileSystem:        fsys,
		githubAPIBaseURL:  githubAPIBaseURL,
		maxResponseBytes:  maxResponseBytes,
		timeout:           timeout,
	}
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
	return sourceVariantCount(source) != 0
}

func sourceVariantCount(source config.Source) int {
	count := 0
	if source.GithubDocs != nil {
		count++
	}
	if source.GithubRepository != nil {
		count++
	}
	if source.Url != nil {
		count++
	}
	return count
}

type fileSystem interface {
	ReadFile(string) ([]byte, error)
	MkdirAll(string, fs.FileMode) error
	CreateTemp(string, string) (tempFile, error)
	Link(string, string) error
	Remove(string) error
}

type tempFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type osFileSystem struct{}

func (osFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (osFileSystem) CreateTemp(dir, pattern string) (tempFile, error) {
	return os.CreateTemp(dir, pattern)
}

func (osFileSystem) Link(oldname, newname string) error {
	return os.Link(oldname, newname)
}

func (osFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func publish(fileSystem fileSystem, destination string, data []byte, expectedSHA256 string) (err error) {
	directory := filepath.Dir(destination)
	err = fileSystem.MkdirAll(directory, 0o755)
	if err != nil {
		return fmt.Errorf("creating schema directory %q: %w", directory, err)
	}

	temp, err := fileSystem.CreateTemp(directory, "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary schema file: %w", err)
	}
	isClosed := false
	defer func() {
		if !isClosed {
			err = errors.Join(err, temp.Close())
		}
		err = errors.Join(err, fileSystem.Remove(temp.Name()))
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

	err = fileSystem.Link(temp.Name(), destination)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("publishing schema file %q: %w", destination, err)
	}

	winner, readErr := fileSystem.ReadFile(destination)
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
