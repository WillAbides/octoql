package schema

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/internal"
)

const schemaRevision = "45d83f459620340069df7c375a8867be62616d61"

var exactSchema = []byte("# preserved comment\n\ntype Query {\n  viewer: String\n}\n")

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
			_, err := fetchURL(t.Context(), client, fetchRequest{
				url:              test.requestURL,
				maxResponseBytes: 1024,
			})
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

func TestGitHubAnonymousFetchAndResolveAuth(t *testing.T) {
	t.Parallel()

	var authorization string
	materializer := testGitHubMaterializer(httpClientFunc(func(request *http.Request) (*http.Response, error) {
		authorization = request.Header.Get("Authorization")
		return newHTTPResponse(http.StatusOK, exactSchema), nil
	}))
	data, err := materializer.Materialize(t.Context(), &Request{
		SHA256: checksum(exactSchema),
		Source: config.Source{
			Repository: "octo-org/public-repo",
			Revision:   schemaRevision,
			Path:       "schema.graphql",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, exactSchema, data)
	assert.Empty(t, authorization)

	_, err = NewMaterializer().Resolve(t.Context(), config.Source{Path: "schema.graphql"})
	require.ErrorContains(t, err, "repository is required")
	_, err = NewMaterializer().Resolve(t.Context(), config.Source{Repository: "owner/repository"})
	require.ErrorContains(t, err, "path is required")

	unauthenticated := testGitHubMaterializer(httpClientFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected GitHub request")
		return nil, nil
	}))
	_, err = unauthenticated.Resolve(t.Context(), config.Source{
		Repository: "octo-owner/octo-repository",
		Path:       "schema.graphql",
	})
	require.EqualError(
		t,
		err,
		"github graphql authentication is required; set GH_TOKEN, GITHUB_TOKEN, or authenticate with gh",
	)
}

func TestDirectiveFileReplacesSymlinkAndHardLinkWithoutModifyingSchema(t *testing.T) {
	t.Parallel()

	const sdl = "type Query {\n  viewer: String\n}\n"

	t.Run("symlink companion", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		schemaPath := filepath.Join(dir, "schema.graphql")
		directivePath := filepath.Join(dir, internal.DirectiveFilename)
		require.NoError(t, os.WriteFile(schemaPath, []byte(sdl), 0o600))
		require.NoError(t, os.Symlink(schemaPath, directivePath))

		got, err := NewMaterializer().Materialize(t.Context(), &Request{
			Path:   schemaPath,
			SHA256: checksum([]byte(sdl)),
		})
		require.NoError(t, err)
		assert.Equal(t, sdl, string(got))

		schemaBytes, err := os.ReadFile(schemaPath)
		require.NoError(t, err)
		assert.Equal(t, sdl, string(schemaBytes))

		info, err := os.Lstat(directivePath)
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular())
		directiveBytes, err := os.ReadFile(directivePath)
		require.NoError(t, err)
		assert.Equal(t, internal.DirectiveFileContents, string(directiveBytes))
	})

	t.Run("hardlink companion", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		schemaPath := filepath.Join(dir, "schema.graphql")
		directivePath := filepath.Join(dir, internal.DirectiveFilename)
		require.NoError(t, os.WriteFile(schemaPath, []byte(sdl), 0o600))
		require.NoError(t, os.Link(schemaPath, directivePath))

		require.NoError(t, WriteDirectiveFile(WriteFile, schemaPath))

		schemaBytes, err := os.ReadFile(schemaPath)
		require.NoError(t, err)
		assert.Equal(t, sdl, string(schemaBytes))
	})
}

func checksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
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
	materializer.GitHubGraphQLEndpoint = func(string) string {
		return "https://api.example.test/graphql"
	}
	return materializer
}

type httpClientFunc func(*http.Request) (*http.Response, error)

func (f httpClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newHTTPResponse(status int, body []byte) *http.Response {
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
