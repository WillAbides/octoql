package integration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	githubclient "github.com/willabides/octoql/internal/generatefeatures/nocontext"
)

type integrationRoundTripFunc func(*http.Request) (*http.Response, error)

type graphqlErrorsFacet interface {
	error
	GraphQLErrorCount() int
	GraphQLError(int) error
}

type graphqlErrorFacet interface {
	error
	GraphQLMessage() string
	GraphQLPath() []any
	GraphQLType() string
}

type rateLimitFacet interface {
	error
	RateLimitKind() string
	RetryAt() time.Time
}

type responseErrorFacet interface {
	error
	GitHubRequestID() string
	HTTPStatusCode() int
}

func TestGeneratedErrorFacetsArePackageNeutral(t *testing.T) {
	graphqlErrors := githubclient.Errors{{
		Type:    "FORBIDDEN",
		Message: "field unavailable",
		Path:    githubclient.Path{"repository", "name"},
	}}
	var aggregate graphqlErrorsFacet
	require.ErrorAs(t, graphqlErrors, &aggregate)
	assert.Equal(t, 1, aggregate.GraphQLErrorCount())

	var graphqlError graphqlErrorFacet
	require.ErrorAs(t, aggregate.GraphQLError(0), &graphqlError)
	assert.Equal(t, "FORBIDDEN", graphqlError.GraphQLType())
	assert.Equal(t, "field unavailable", graphqlError.GraphQLMessage())
	assert.Equal(t, []any{"repository", "name"}, graphqlError.GraphQLPath())

	responseError := &githubclient.ResponseError{
		StatusCode: http.StatusForbidden,
		RequestID:  "request-123",
	}
	var response responseErrorFacet
	require.ErrorAs(t, responseError, &response)
	assert.Equal(t, http.StatusForbidden, response.HTTPStatusCode())
	assert.Equal(t, "request-123", response.GitHubRequestID())
}

func TestGeneratedRateLimitFacetRetryAt(t *testing.T) {
	primaryReset := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	secondaryRetryAt := time.Date(2026, time.July, 22, 11, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		kind      githubclient.RateLimitKind
		rateLimit githubclient.RateLimit
		want      time.Time
	}{
		{
			name: "primary uses reset",
			kind: githubclient.RateLimitPrimary,
			rateLimit: githubclient.RateLimit{
				Reset:   primaryReset,
				RetryAt: secondaryRetryAt,
			},
			want: primaryReset,
		},
		{
			name: "secondary uses retry at",
			kind: githubclient.RateLimitSecondary,
			rateLimit: githubclient.RateLimit{
				Reset:   primaryReset,
				RetryAt: secondaryRetryAt,
			},
			want: secondaryRetryAt,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rateLimitError := &githubclient.RateLimitError{
				Kind:      test.kind,
				RateLimit: test.rateLimit,
			}
			var rateLimit rateLimitFacet
			require.ErrorAs(t, rateLimitError, &rateLimit)
			assert.Equal(t, string(test.kind), rateLimit.RateLimitKind())
			assert.Equal(t, test.want, rateLimit.RetryAt())
		})
	}
}

func (f integrationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGeneratedQueryResponseSemantics(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *githubclient.GetRepositoryResponse, error)
		header     http.Header
		name       string
		body       string
		statusCode int
		wantData   bool
	}{
		{
			name:       "success ignores top-level extensions",
			statusCode: http.StatusOK,
			wantData:   true,
			header: http.Header{
				"X-GitHub-Request-ID": {"request-123"},
				"X-Test":              {"original"},
			},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"extensions":{"trace":"abc"}
			}`,
			check: func(t *testing.T, response *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
			},
		},
		{
			name:       "partial data and GraphQL errors",
			statusCode: http.StatusOK,
			wantData:   true,
			header:     http.Header{},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"errors":[{"type":"FORBIDDEN","message":"field unavailable","path":["repository","nameWithOwner"]}]
			}`,
			check: func(t *testing.T, response *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
				graphqlErrors, ok := errors.AsType[githubclient.Errors](err)
				require.True(t, ok)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, githubclient.Path{"repository", "nameWithOwner"}, graphqlErrors[0].Path)
			},
		},
		{
			name:       "primary rate limit",
			statusCode: http.StatusOK,
			wantData:   true,
			header: http.Header{
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Resource":  {"graphql"},
			},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]
			}`,
			check: func(t *testing.T, _ *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				rateLimitError, ok := errors.AsType[*githubclient.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, githubclient.RateLimitPrimary, rateLimitError.Kind)
				assert.Zero(t, rateLimitError.RateLimit.Remaining)
			},
		},
		{
			name:       "secondary rate limit at HTTP 403",
			statusCode: http.StatusForbidden,
			header: http.Header{
				"Retry-After": {"30"},
			},
			body: `{"errors":[{"type":"ABUSE_DETECTED","message":"slow down"}]}`,
			check: func(t *testing.T, _ *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				rateLimitError, ok := errors.AsType[*githubclient.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, githubclient.RateLimitSecondary, rateLimitError.Kind)
				assert.Equal(t, 30*time.Second, rateLimitError.RateLimit.RetryAfter)
				responseError, ok := errors.AsType[*githubclient.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
			},
		},
		{
			name:       "non-2xx with decodable data",
			statusCode: http.StatusForbidden,
			wantData:   true,
			header:     http.Header{},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/partial"}},
				"errors":[{"type":"FORBIDDEN","message":"request rejected"}]
			}`,
			check: func(t *testing.T, response *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "octo-org/partial", response.Repository.NameWithOwner)
				responseError, ok := errors.AsType[*githubclient.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
			},
		},
		{
			name:       "null data and GraphQL errors",
			statusCode: http.StatusOK,
			header:     http.Header{},
			body: `{
				"data":null,
				"errors":[{"type":"NOT_FOUND","message":"repository unavailable"}]
			}`,
			check: func(t *testing.T, _ *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				_, hasErrors := errors.AsType[githubclient.Errors](err)
				assert.True(t, hasErrors)
				_, hasPartial := errors.AsType[*githubclient.GetRepositoryPartialDataError](err)
				assert.False(t, hasPartial)
			},
		},
		{
			name:       "invalid generated data",
			statusCode: http.StatusOK,
			header:     http.Header{},
			body:       `{"data":{"repository":"not an object"}}`,
			check: func(t *testing.T, _ *githubclient.GetRepositoryResponse, err error) {
				t.Helper()
				_, hasDecodeError := errors.AsType[*json.UnmarshalTypeError](err)
				assert.True(t, hasDecodeError)
				_, hasPartial := errors.AsType[*githubclient.GetRepositoryPartialDataError](err)
				assert.False(t, hasPartial)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{
				Transport: integrationRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: test.statusCode,
						Header:     test.header,
						Body:       io.NopCloser(strings.NewReader(test.body)),
					}, nil
				}),
			}
			client := githubclient.NewClient("https://api.github.example/graphql", httpClient)
			response, err := client.GetRepository(githubclient.GetRepositoryVariables{
				Owner: "octo-org",
				Name:  "octo-repo",
			})

			if err == nil {
				assert.Equal(t, test.wantData, response != nil)
			}
			if err != nil {
				assert.Nil(t, response)
				partialErr, hasPartial := errors.AsType[*githubclient.GetRepositoryPartialDataError](err)
				assert.Equal(t, test.wantData, hasPartial)
				if hasPartial {
					response = partialErr.PartialData()
				}
			}
			test.check(t, response, err)
		})
	}
}

func TestGeneratedQueryTransportFailure(t *testing.T) {
	transportError := errors.New("transport failed")
	httpClient := &http.Client{
		Transport: integrationRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportError
		}),
	}
	client := githubclient.NewClient("https://api.github.example/graphql", httpClient)

	response, err := client.GetRepository(githubclient.GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})

	require.ErrorIs(t, err, transportError)
	assert.Nil(t, response)
}

func TestGeneratedQueryCloseFailureWithData(t *testing.T) {
	closeErr := errors.New("close failed")
	httpClient := &http.Client{
		Transport: integrationRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body: &integrationCloseErrorBody{
					Reader: strings.NewReader(
						`{"data":{"repository":{"nameWithOwner":"octo-org/partial"}}}`,
					),
					err: closeErr,
				},
			}, nil
		}),
	}
	client := githubclient.NewClient("https://api.github.example/graphql", httpClient)

	response, err := client.GetRepository(githubclient.GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "partial",
	})

	assert.Nil(t, response)
	assert.ErrorIs(t, err, closeErr)
	partialErr, ok := errors.AsType[*githubclient.GetRepositoryPartialDataError](err)
	require.True(t, ok)
	require.NotNil(t, partialErr.PartialData())
	assert.Equal(t, "octo-org/partial", partialErr.PartialData().Repository.NameWithOwner)
}

type integrationCloseErrorBody struct {
	io.Reader
	err error
}

func (b *integrationCloseErrorBody) Close() error {
	return b.err
}
