// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

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
	"github.com/willabides/octoql"
)

type integrationRoundTripFunc func(*http.Request) (*http.Response, error)

func (function integrationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGeneratedQueryResponseSemantics(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *octoql.Response[simpleQueryResponse], error)
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
				"data":{"me":{"id":"1","name":"octoql"}},
				"extensions":{"trace":"abc"}
			}`,
			check: func(t *testing.T, response *octoql.Response[simpleQueryResponse], err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, response)
				assert.Equal(t, "1", response.Data.Me.Id)
				assert.Equal(t, "abc", response.Extensions["trace"])
				assert.Equal(t, http.StatusOK, response.HTTP.StatusCode)
				assert.Equal(t, "request-123", response.HTTP.RequestID)
				assert.Equal(t, "original", response.HTTP.Header.Get("X-Test"))
			},
		},
		{
			name:       "partial data and GraphQL errors",
			statusCode: http.StatusOK,
			header:     http.Header{},
			body: `{
				"data":{"me":{"id":"1","name":"partial"}},
				"errors":[{"type":"FORBIDDEN","message":"field unavailable","path":["me","name"]}]
			}`,
			check: func(t *testing.T, response *octoql.Response[simpleQueryResponse], err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "1", response.Data.Me.Id)
				graphqlErrors, ok := errors.AsType[octoql.Errors](err)
				require.True(t, ok)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, octoql.ErrorType("FORBIDDEN"), graphqlErrors[0].Type)
				graphqlError, ok := errors.AsType[*octoql.Error](err)
				require.True(t, ok)
				assert.Equal(t, octoql.Path{"me", "name"}, graphqlError.Path)
			},
		},
		{
			name:       "non-2xx GraphQL response",
			statusCode: http.StatusForbidden,
			header:     http.Header{},
			body: `{
				"data":{"me":{"id":"1"}},
				"errors":[{"type":"FORBIDDEN","message":"denied"}]
			}`,
			check: func(t *testing.T, response *octoql.Response[simpleQueryResponse], err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "1", response.Data.Me.Id)
				httpError, ok := errors.AsType[*octoql.HTTPError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, httpError.HTTP.StatusCode)
				_, ok = errors.AsType[octoql.Errors](err)
				assert.True(t, ok)
			},
		},
		{
			name:       "non-2xx invalid response",
			statusCode: http.StatusBadGateway,
			header:     http.Header{},
			body:       `{"errors":[`,
			check: func(t *testing.T, response *octoql.Response[simpleQueryResponse], err error) {
				t.Helper()
				require.NotNil(t, response)
				httpError, ok := errors.AsType[*octoql.HTTPError](err)
				require.True(t, ok)
				assert.Equal(t, `{"errors":[`, string(httpError.Body))
				_, ok = errors.AsType[*json.SyntaxError](err)
				assert.True(t, ok)
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
				"data":{"me":{"id":"1"}},
				"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]
			}`,
			check: func(t *testing.T, response *octoql.Response[simpleQueryResponse], err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "1", response.Data.Me.Id)
				rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, octoql.RateLimitPrimary, rateLimitError.Kind)
				assert.Equal(t, response.HTTP.RateLimit, rateLimitError.RateLimit)
			},
		},
		{
			name:       "secondary rate limit",
			statusCode: http.StatusForbidden,
			header: http.Header{
				"Retry-After": {"30"},
			},
			body: `{"errors":[{"message":"slow down"}]}`,
			check: func(t *testing.T, response *octoql.Response[simpleQueryResponse], err error) {
				t.Helper()
				require.NotNil(t, response)
				rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, octoql.RateLimitSecondary, rateLimitError.Kind)
				assert.Equal(t, 30*time.Second, response.HTTP.RateLimit.RetryAfter)
				assert.Equal(t, response.HTTP.RateLimit, rateLimitError.RateLimit)
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

			response, err := simpleQuery(t.Context(), client)
			test.check(t, response, err)

			test.header.Set("X-Test", "mutated")
			assert.NotEqual(t, "mutated", response.HTTP.Header.Get("X-Test"))
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

	response, err := simpleQuery(t.Context(), client)

	require.ErrorIs(t, err, transportError)
	assert.Nil(t, response)
}
