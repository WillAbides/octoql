// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package integration

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
	githubclient "github.com/willabides/octoql/internal/generatefeatures/nocontext"
)

type integrationRoundTripFunc func(*http.Request) (*http.Response, error)

func (function integrationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGeneratedQueryResponseSemantics(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *octoql.Response[githubclient.GetRepositoryResponse], error)
		header     http.Header
		name       string
		body       string
		statusCode int
	}{
		{
			name:       "extensions and HTTP metadata",
			statusCode: http.StatusOK,
			header: http.Header{
				"X-GitHub-Request-ID": {"request-123"},
				"X-Test":              {"original"},
			},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"extensions":{"trace":"abc"}
			}`,
			check: func(t *testing.T, response *octoql.Response[githubclient.GetRepositoryResponse], err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, "octo-org/octo-repo", response.Data.Repository.NameWithOwner)
				assert.Equal(t, "abc", response.Extensions["trace"])
				assert.Equal(t, "request-123", response.HTTP.RequestID)
				assert.Equal(t, "original", response.HTTP.Header.Get("X-Test"))
			},
		},
		{
			name:       "partial data and GraphQL errors",
			statusCode: http.StatusOK,
			header:     http.Header{},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"errors":[{"type":"FORBIDDEN","message":"field unavailable","path":["repository","nameWithOwner"]}]
			}`,
			check: func(t *testing.T, response *octoql.Response[githubclient.GetRepositoryResponse], err error) {
				t.Helper()
				assert.Equal(t, "octo-org/octo-repo", response.Data.Repository.NameWithOwner)
				graphqlErrors, ok := errors.AsType[octoql.Errors](err)
				require.True(t, ok)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, octoql.Path{"repository", "nameWithOwner"}, graphqlErrors[0].Path)
			},
		},
		{
			name:       "primary rate limit",
			statusCode: http.StatusOK,
			header: http.Header{
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Resource":  {"graphql"},
			},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]
			}`,
			check: func(t *testing.T, response *octoql.Response[githubclient.GetRepositoryResponse], err error) {
				t.Helper()
				rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, octoql.RateLimitPrimary, rateLimitError.Kind)
				assert.Equal(t, response.HTTP.RateLimit, rateLimitError.RateLimit)
			},
		},
		{
			name:       "secondary rate limit at HTTP 403",
			statusCode: http.StatusForbidden,
			header: http.Header{
				"Retry-After": {"30"},
			},
			body: `{"errors":[{"type":"ABUSE_DETECTED","message":"slow down"}]}`,
			check: func(t *testing.T, response *octoql.Response[githubclient.GetRepositoryResponse], err error) {
				t.Helper()
				rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, octoql.RateLimitSecondary, rateLimitError.Kind)
				assert.Equal(t, 30*time.Second, response.HTTP.RateLimit.RetryAfter)
				assert.Equal(t, http.StatusForbidden, response.HTTP.StatusCode)
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
			client := octoql.NewClient("https://api.github.example/graphql", httpClient)
			response, err := githubclient.GetRepository(client, "octo-org", "octo-repo")
			require.NotNil(t, response)
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
	client := octoql.NewClient("https://api.github.example/graphql", httpClient)

	response, err := githubclient.GetRepository(client, "octo-org", "octo-repo")

	require.ErrorIs(t, err, transportError)
	assert.Nil(t, response)
}
