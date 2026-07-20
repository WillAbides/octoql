package schema

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
)

const schemaRevision = "45d83f459620340069df7c375a8867be62616d61"

var exactSchema = []byte("# preserved comment\n\ntype Query {\n  viewer: String\n}\n")

func TestMaterializerExistingFile(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "schema.graphql")
	err := os.WriteFile(destination, exactSchema, 0o600)
	require.NoError(t, err)

	materializer := NewMaterializer()
	data, err := materializer.Materialize(t.Context(), Request{
		Path:   destination,
		SHA256: checksum(exactSchema),
	})
	require.NoError(t, err)
	assert.Equal(t, exactSchema, data)
}

func TestMaterializerExistingFileMismatch(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "schema.graphql")
	err := os.WriteFile(destination, exactSchema, 0o600)
	require.NoError(t, err)
	expected := checksum([]byte("different"))
	actual := checksum(exactSchema)

	materializer := NewMaterializer()
	_, err = materializer.Materialize(t.Context(), Request{
		Path:   destination,
		SHA256: expected,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected "+expected)
	assert.Contains(t, err.Error(), "actual "+actual)

	after, readErr := os.ReadFile(destination)
	require.NoError(t, readErr)
	assert.Equal(t, exactSchema, after)
}

func TestMaterializerReadError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("read interrupted")
	materializer := NewMaterializer()
	materializer.FileSystem = &readErrorFileSystem{err: expectedErr}

	_, err := materializer.Materialize(t.Context(), Request{
		Path: filepath.Join(t.TempDir(), "schema.graphql"),
	})
	require.ErrorIs(t, err, expectedErr)
	assert.Contains(t, err.Error(), "reading schema file")
}

func TestMaterializerMissingLocalFile(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "schema.graphql")
	materializer := NewMaterializer()
	_, err := materializer.Materialize(t.Context(), Request{Path: destination})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local schema file")
	assert.Contains(t, err.Error(), "does not exist")
}

func TestMaterializerURLFetch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Empty(t, request.Header.Get("Accept"))
		_, _ = response.Write(exactSchema)
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "nested", "schema.graphql")
	materializer := NewMaterializer()
	data, err := materializer.Materialize(t.Context(), Request{
		Path:   destination,
		SHA256: checksum(exactSchema),
		Source: config.Source{Url: new(server.URL + "/schema.graphql")},
	})
	require.NoError(t, err)
	assert.Equal(t, exactSchema, data)

	written, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, exactSchema, written)
	assert.Empty(t, temporaryFiles(t, destination))
}

func TestFetchURLSanitizesErrors(t *testing.T) {
	t.Parallel()

	const (
		userSecret           = "unmistakable-user-secret"
		closingQuerySecret   = "unmistakable-closing-query-secret"
		quotedQuerySecret    = "unmistakable-quoted-query-secret"
		spacedQuerySecret    = "unmistakable-spaced-query-secret"
		directFragmentSecret = "unmistakable-direct-fragment-secret"
		nestedFragmentSecret = "unmistakable-nested-fragment-secret"
	)
	requestBaseURL := "https://" + userSecret + "@example.test/schema.graphql?" +
		"closing=)" + closingQuerySecret +
		"&quoted='" + quotedQuerySecret +
		"&spaced=%20" + spacedQuerySecret
	requestURL := requestBaseURL + "#" + directFragmentSecret
	nestedURL := requestBaseURL + "#" + nestedFragmentSecret
	outerSecret := "unmistakable-outer-secret"
	innerSecret := "unmistakable-inner-secret"
	outerURL := "https://example.test/schema?signature=" + outerSecret
	innerURL := outerURL + "&redirect=)" + innerSecret
	tests := []struct {
		transportError func() error
		name           string
		requestURL     string
		want           []string
		secrets        []string
	}{
		{
			name:       "nested URLs",
			requestURL: requestURL,
			transportError: func() error {
				inner := &url.Error{
					Op:  "dial",
					URL: nestedURL,
					Err: errors.New("connection refused"),
				}
				return fmt.Errorf("transport retry: %w", &url.Error{
					Op:  "Get",
					URL: requestURL,
					Err: inner,
				})
			},
			want: []string{"example.test/schema.graphql", "connection refused"},
			secrets: []string{
				userSecret,
				closingQuerySecret,
				quotedQuerySecret,
				spacedQuerySecret,
				directFragmentSecret,
				nestedFragmentSecret,
			},
		},
		{
			name:       "prefix-related URLs",
			requestURL: outerURL,
			transportError: func() error {
				inner := &url.Error{
					Op:  "redirect",
					URL: innerURL,
					Err: errors.New("redirect refused"),
				}
				return &url.Error{
					Op:  "Get",
					URL: outerURL,
					Err: inner,
				}
			},
			want:    []string{"example.test/schema", "redirect refused"},
			secrets: []string{outerSecret, innerSecret},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var receivedURL string
			client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
				receivedURL = request.URL.String()
				return nil, test.transportError()
			})
			_, err := fetchURL(t.Context(), client, test.requestURL, "", false, 1024)
			require.Error(t, err)
			assert.Equal(t, test.requestURL, receivedURL)
			for _, value := range test.want {
				assert.Contains(t, err.Error(), value)
			}
			for _, secret := range test.secrets {
				assert.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestMaterializerDownloadFailures(t *testing.T) {
	tests := []struct {
		handler       http.Handler
		name          string
		sha256        string
		expectedError string
		maxBytes      int64
		timeout       time.Duration
	}{
		{
			name:          "missing checksum",
			handler:       http.NotFoundHandler(),
			expectedError: "sha256 is required for remote sources",
		},
		{
			name: "checksum mismatch",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write(exactSchema)
			}),
			sha256:        checksum([]byte("different")),
			expectedError: "schema checksum mismatch",
		},
		{
			name: "invalid sdl",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(response, "not graphql")
			}),
			sha256:        checksum([]byte("not graphql")),
			expectedError: "invalid graphql schema",
		},
		{
			name: "http error",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				http.Error(response, "not found", http.StatusNotFound)
			}),
			sha256:        checksum(exactSchema),
			expectedError: "http status 404",
		},
		{
			name: "timeout",
			handler: http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				<-request.Context().Done()
			}),
			timeout:       10 * time.Millisecond,
			sha256:        checksum(exactSchema),
			expectedError: "context deadline exceeded",
		},
		{
			name: "oversized response",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(response, strings.Repeat("x", 65))
			}),
			maxBytes:      64,
			sha256:        checksum(exactSchema),
			expectedError: "response exceeds 64-byte limit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(test.handler)
			t.Cleanup(server.Close)
			destination := filepath.Join(t.TempDir(), "schema.graphql")
			materializer := NewMaterializer()
			if test.maxBytes != 0 {
				materializer.MaxResponseBytes = test.maxBytes
			}
			if test.timeout != 0 {
				materializer.Timeout = test.timeout
			}

			_, err := materializer.Materialize(t.Context(), Request{
				Path:   destination,
				SHA256: test.sha256,
				Source: config.Source{Url: new(server.URL)},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
			_, statErr := os.Stat(destination)
			require.ErrorIs(t, statErr, fs.ErrNotExist)
			assert.Empty(t, temporaryFiles(t, destination))
		})
	}
}

func TestMaterializerRejectsConflictingSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source config.Source
		name   string
	}{
		{
			name: "url and github docs",
			source: config.Source{
				Url:        new("https://example.test/schema.graphql"),
				GithubDocs: &config.GithubDocs{},
			},
		},
		{
			name: "url and github repository",
			source: config.Source{
				Url:              new("https://example.test/schema.graphql"),
				GithubRepository: &config.GithubRepository{},
			},
		},
		{
			name: "github docs and github repository",
			source: config.Source{
				GithubDocs:       &config.GithubDocs{},
				GithubRepository: &config.GithubRepository{},
			},
		},
		{
			name: "all variants",
			source: config.Source{
				Url:              new("https://example.test/schema.graphql"),
				GithubDocs:       &config.GithubDocs{},
				GithubRepository: &config.GithubRepository{},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewMaterializer().Materialize(t.Context(), Request{
				Source: test.source,
				SHA256: "checksum",
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "multiple remote source variants")
		})
	}
}

func TestMaterializerGitHubDocsPaths(t *testing.T) {
	tests := []struct {
		version      string
		expectedPath string
	}{
		{
			version:      "fpt",
			expectedPath: "/repos/github/docs/contents/src/graphql/data/fpt/schema.docs.graphql",
		},
		{
			version:      "ghec",
			expectedPath: "/repos/github/docs/contents/src/graphql/data/ghec/schema.docs.graphql",
		},
		{
			version:      "ghes-3.21",
			expectedPath: "/repos/github/docs/contents/src/graphql/data/ghes-3.21/schema.docs-enterprise.graphql",
		},
	}

	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			var requestedPath string
			var requestedRevision string
			client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
				requestedPath = request.URL.Path
				requestedRevision = request.URL.Query().Get("ref")
				assert.Equal(t, "application/vnd.github.raw+json", request.Header.Get("Accept"))
				return response(http.StatusOK, exactSchema), nil
			})
			materializer := testGitHubMaterializer(client)

			data, err := materializer.Materialize(t.Context(), Request{
				SHA256: checksum(exactSchema),
				Source: config.Source{
					GithubDocs: &config.GithubDocs{
						Version:  test.version,
						Revision: schemaRevision,
					},
				},
			})
			require.NoError(t, err)
			assert.Equal(t, exactSchema, data)
			assert.Equal(t, test.expectedPath, requestedPath)
			assert.Equal(t, schemaRevision, requestedRevision)
		})
	}
}

func TestGitHubRepositoryRequestEscaping(t *testing.T) {
	t.Parallel()

	requestURL, err := githubContentsURL(
		"https://github.example.com/api/v3",
		config.GithubRepository{
			Repository: "octo-org/octo.repo",
			Revision:   schemaRevision,
			Path:       "schema dir/schema#one.graphql",
			Host:       new("github.example.com"),
		},
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		"https://github.example.com/api/v3/repos/octo-org/octo.repo/contents/"+
			"schema%20dir/schema%23one.graphql?ref="+schemaRevision,
		requestURL,
	)

	_, err = githubContentsURL(
		"https://github.example.com/api/v3",
		config.GithubRepository{Repository: "invalid"},
	)
	require.Error(t, err)
}

func TestGitHubTokenPrecedence(t *testing.T) {
	tests := []struct {
		environment   map[string]string
		runner        *stubCommandRunner
		name          string
		expectedToken string
		expectedError string
		expectedCalls int
	}{
		{
			name: "gh token first",
			environment: map[string]string{
				"GH_TOKEN":     "gh-token",
				"GITHUB_TOKEN": "github-token",
			},
			runner:        &stubCommandRunner{stdout: []byte("cli-token\n")},
			expectedToken: "gh-token",
		},
		{
			name: "github token second",
			environment: map[string]string{
				"GITHUB_TOKEN": "github-token",
			},
			runner:        &stubCommandRunner{stdout: []byte("cli-token\n")},
			expectedToken: "github-token",
		},
		{
			name:          "gh fallback",
			environment:   map[string]string{},
			runner:        &stubCommandRunner{stdout: []byte("cli-token\n")},
			expectedToken: "cli-token",
			expectedCalls: 1,
		},
		{
			name:          "gh missing permits anonymous",
			environment:   map[string]string{},
			runner:        &stubCommandRunner{err: exec.ErrNotFound},
			expectedCalls: 1,
		},
		{
			name:          "gh not authenticated permits anonymous",
			environment:   map[string]string{},
			runner:        &stubCommandRunner{stderr: []byte("not logged into github.com"), err: errors.New("exit 1")},
			expectedCalls: 1,
		},
		{
			name:          "gh execution error is returned",
			environment:   map[string]string{},
			runner:        &stubCommandRunner{stderr: []byte("unexpected failure"), err: errors.New("exit 2")},
			expectedCalls: 1,
			expectedError: "discovering github token with gh",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			token, err := discoverToken(
				t.Context(),
				"github.com",
				environmentLookup(test.environment),
				test.runner,
			)
			if test.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expectedToken, token)
			assert.Equal(t, test.expectedCalls, test.runner.Calls())
		})
	}
}

func TestGitHubAnonymousAndRejectedToken(t *testing.T) {
	tests := []struct {
		environment   map[string]string
		name          string
		expectedAuth  string
		expectedError string
		status        int
	}{
		{
			name:        "anonymous public fetch",
			environment: map[string]string{},
			status:      http.StatusOK,
		},
		{
			name:          "missing private access",
			environment:   map[string]string{},
			status:        http.StatusNotFound,
			expectedError: "http status 404",
		},
		{
			name: "rejected present token does not retry anonymously",
			environment: map[string]string{
				"GH_TOKEN": "secret-token",
			},
			status:        http.StatusUnauthorized,
			expectedAuth:  "Bearer secret-token",
			expectedError: "http status 401",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			var authorization string
			client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				authorization = request.Header.Get("Authorization")
				return response(test.status, exactSchema), nil
			})
			materializer := testGitHubMaterializer(client)
			materializer.LookupEnvironment = environmentLookup(test.environment)

			data, err := materializer.Materialize(t.Context(), Request{
				SHA256: checksum(exactSchema),
				Source: config.Source{
					GithubRepository: &config.GithubRepository{
						Repository: "octo-org/private-repo",
						Revision:   schemaRevision,
						Path:       "schema.graphql",
					},
				},
			})
			if test.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, exactSchema, data)
			}
			assert.Equal(t, int32(1), calls.Load())
			assert.Equal(t, test.expectedAuth, authorization)
			if test.expectedAuth != "" {
				assert.NotContains(t, err.Error(), "secret-token")
			}
		})
	}
}

func TestMaterializerAtomicWriteFailures(t *testing.T) {
	tests := []struct {
		name          string
		wrap          func(tempFile) tempFile
		expectedError string
	}{
		{
			name: "interrupted write",
			wrap: func(file tempFile) tempFile {
				return &failingTempFile{
					tempFile: file,
					writeErr: errors.New("write interrupted"),
				}
			},
			expectedError: "writing temporary schema file",
		},
		{
			name: "failed sync",
			wrap: func(file tempFile) tempFile {
				return &failingTempFile{
					tempFile: file,
					syncErr:  errors.New("sync failed"),
				}
			},
			expectedError: "syncing temporary schema file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write(exactSchema)
			}))
			t.Cleanup(server.Close)
			destination := filepath.Join(t.TempDir(), "schema.graphql")
			materializer := NewMaterializer()
			materializer.FileSystem = &wrappingFileSystem{wrap: test.wrap}

			_, err := materializer.Materialize(t.Context(), Request{
				Path:   destination,
				SHA256: checksum(exactSchema),
				Source: config.Source{Url: new(server.URL)},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
			_, statErr := os.Stat(destination)
			require.ErrorIs(t, statErr, fs.ErrNotExist)
			assert.Empty(t, temporaryFiles(t, destination))
		})
	}
}

func TestMaterializerConcurrent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(exactSchema)
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "schema.graphql")
	const workers = 20
	errorsByWorker := make([]error, workers)
	results := make([][]byte, workers)
	var waitGroup sync.WaitGroup
	for worker := range workers {
		waitGroup.Go(func() {
			materializer := NewMaterializer()
			results[worker], errorsByWorker[worker] = materializer.Materialize(t.Context(), Request{
				Path:   destination,
				SHA256: checksum(exactSchema),
				Source: config.Source{Url: new(server.URL)},
			})
		})
	}
	waitGroup.Wait()

	for worker := range workers {
		require.NoError(t, errorsByWorker[worker])
		assert.Equal(t, exactSchema, results[worker])
	}
	written, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, exactSchema, written)
	assert.Empty(t, temporaryFiles(t, destination))
}

func TestMaterializerConcurrentDifferentBytes(t *testing.T) {
	t.Parallel()

	firstSchema := []byte("type Query { first: String }\n")
	secondSchema := []byte("type Query { second: String }\n")
	firstServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(firstSchema)
	}))
	t.Cleanup(firstServer.Close)
	secondServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(secondSchema)
	}))
	t.Cleanup(secondServer.Close)

	destination := filepath.Join(t.TempDir(), "schema.graphql")
	requests := []Request{
		{
			Path:   destination,
			SHA256: checksum(firstSchema),
			Source: config.Source{Url: new(firstServer.URL)},
		},
		{
			Path:   destination,
			SHA256: checksum(secondSchema),
			Source: config.Source{Url: new(secondServer.URL)},
		},
	}
	results := make([][]byte, len(requests))
	resultErrors := make([]error, len(requests))
	var waitGroup sync.WaitGroup
	for index, request := range requests {
		waitGroup.Go(func() {
			materializer := NewMaterializer()
			results[index], resultErrors[index] = materializer.Materialize(t.Context(), request)
		})
	}
	waitGroup.Wait()

	successCount := 0
	expectedResults := [][]byte{firstSchema, secondSchema}
	for index := range requests {
		if resultErrors[index] == nil {
			successCount++
			assert.Equal(t, expectedResults[index], results[index])
			continue
		}
		assert.Contains(t, resultErrors[index].Error(), "checksum mismatch")
	}
	assert.Equal(t, 1, successCount)

	written, err := os.ReadFile(destination)
	require.NoError(t, err)
	isExpectedWinner := bytes.Equal(written, firstSchema) || bytes.Equal(written, secondSchema)
	assert.True(t, isExpectedWinner)
	assert.Empty(t, temporaryFiles(t, destination))
}

func TestMaterializerRecoversInterruptedSchemaUpdate(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	originalConfig := []byte("schema:\n  path: schema.graphql\n")
	originalSchema := []byte("type Query { old: String }\n")
	updatedSchema := []byte("type Query { new: String }\n")
	err := os.WriteFile(configPath, originalConfig, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, originalSchema, 0o600)
	require.NoError(t, err)
	_, err = BeginUpdate(schemaPath, configPath)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, updatedSchema, 0o600)
	require.NoError(t, err)

	materializer := NewMaterializer()
	data, err := materializer.Materialize(t.Context(), Request{
		Path:   schemaPath,
		SHA256: checksum(originalSchema),
	})
	require.NoError(t, err)
	assert.Equal(t, originalSchema, data)
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, originalConfig, configData)
	_, err = os.Stat(schemaPath + updateJournalSuffix)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func checksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func temporaryFiles(t *testing.T, destination string) []string {
	files, err := filepath.Glob(filepath.Join(
		filepath.Dir(destination),
		"."+filepath.Base(destination)+".tmp-*",
	))
	require.NoError(t, err)
	return files
}

func environmentLookup(environment map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := environment[name]
		return value, found
	}
}

func testGitHubMaterializer(client httpDoer) *Materializer {
	materializer := NewMaterializer()
	materializer.HTTPClient = client
	materializer.CommandRunner = &stubCommandRunner{
		stderr: []byte("not logged into github.com"),
		err:    errors.New("exit 1"),
	}
	materializer.LookupEnvironment = environmentLookup(map[string]string{})
	materializer.GitHubAPIBaseURL = func(string) string {
		return "https://api.example.test"
	}
	return materializer
}

type httpClientFunc func(*http.Request) (*http.Response, error)

func (f httpClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func response(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(strings.NewReader(string(body))),
		ContentLength: int64(len(body)),
		Header:        http.Header{},
	}
}

type stubCommandRunner struct {
	err    error
	stdout []byte
	stderr []byte
	calls  atomic.Int32
}

func (r *stubCommandRunner) Run(
	_ context.Context,
	_ string,
	_ ...string,
) ([]byte, []byte, error) {
	r.calls.Add(1)
	return r.stdout, r.stderr, r.err
}

func (r *stubCommandRunner) Calls() int {
	return int(r.calls.Load())
}

type readErrorFileSystem struct {
	osFileSystem
	err error
}

func (f *readErrorFileSystem) ReadFile(string) ([]byte, error) {
	return nil, f.err
}

type wrappingFileSystem struct {
	osFileSystem
	wrap func(tempFile) tempFile
}

func (f *wrappingFileSystem) CreateTemp(dir, pattern string) (tempFile, error) {
	file, err := f.osFileSystem.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return f.wrap(file), nil
}

type failingTempFile struct {
	tempFile
	writeErr error
	syncErr  error
}

func (f *failingTempFile) Write(data []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.tempFile.Write(data)
}

func (f *failingTempFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.tempFile.Sync()
}
