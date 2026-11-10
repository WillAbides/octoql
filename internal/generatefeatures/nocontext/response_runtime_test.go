package nocontext

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type responseData struct {
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
	Count int `json:"count"`
}

type responseErrorFacet interface {
	error
	GitHubRequestID() string
	HTTPStatusCode() int
}

type partialDataFacet interface {
	error
	PartialDataValue() any
}

func TestGeneratedQueryResponseSemantics(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *getRepositoryResponse, error)
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
			},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}},
				"extensions":{"trace":"abc"}
			}`,
			check: func(t *testing.T, response *getRepositoryResponse, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
			},
		},
		{
			name:       "partial data and response error facets",
			statusCode: http.StatusOK,
			wantData:   true,
			header:     http.Header{"X-GitHub-Request-ID": {"request-partial"}},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/partial"}},
				"errors":[{
					"type":"FORBIDDEN",
					"message":"owner unavailable",
					"path":["repository","owner"],
					"extensions":{"code":"missing"}
				}]
			}`,
			check: func(t *testing.T, response *getRepositoryResponse, err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "octo-org/partial", response.Repository.NameWithOwner)

				responseError, ok := errors.AsType[*ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusOK, responseError.StatusCode)
				assert.Equal(t, "request-partial", responseError.RequestID)
				assert.Empty(t, responseError.RawBody)

				var facet responseErrorFacet
				require.ErrorAs(t, err, &facet)
				assert.Equal(t, http.StatusOK, facet.HTTPStatusCode())
				assert.Equal(t, "request-partial", facet.GitHubRequestID())

				var partialFacet partialDataFacet
				require.ErrorAs(t, err, &partialFacet)
				assert.Equal(t, response, partialFacet.PartialDataValue())

				graphqlErrors, ok := errors.AsType[Errors](err)
				require.True(t, ok)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, ErrorType("FORBIDDEN"), graphqlErrors[0].Type)
				assert.Equal(t, Path{"repository", "owner"}, graphqlErrors[0].Path)
				assert.Equal(t, "missing", graphqlErrors[0].Extensions["code"])

				_, ok = errors.AsType[*RateLimitError](err)
				assert.False(t, ok)
			},
		},
		{
			name:       "non-2xx with decodable data",
			statusCode: http.StatusForbidden,
			wantData:   true,
			header:     http.Header{"X-GitHub-Request-ID": {"request-forbidden-partial"}},
			body: `{
				"data":{"repository":{"nameWithOwner":"octo-org/partial"}},
				"errors":[{"type":"FORBIDDEN","message":"request rejected"}]
			}`,
			check: func(t *testing.T, response *getRepositoryResponse, err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "octo-org/partial", response.Repository.NameWithOwner)
				responseError, ok := errors.AsType[*ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
			},
		},
		{
			name:       "non-2xx without data",
			statusCode: http.StatusForbidden,
			header:     http.Header{"X-GitHub-Request-ID": {"request-forbidden"}},
			body:       `{"errors":[{"type":"FORBIDDEN","message":"rejected"}]}`,
			check: func(t *testing.T, _ *getRepositoryResponse, err error) {
				t.Helper()
				responseError, ok := errors.AsType[*ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
				assert.Equal(t, "request-forbidden", responseError.RequestID)
				assert.Equal(t, `{"errors":[{"type":"FORBIDDEN","message":"rejected"}]}`, string(responseError.RawBody))
				_, ok = errors.AsType[Errors](err)
				assert.True(t, ok)
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
			check: func(t *testing.T, _ *getRepositoryResponse, err error) {
				t.Helper()
				_, hasErrors := errors.AsType[Errors](err)
				assert.True(t, hasErrors)
			},
		},
		{
			name:       "invalid generated data",
			statusCode: http.StatusOK,
			header:     http.Header{},
			body:       `{"data":{"repository":"not an object"}}`,
			check: func(t *testing.T, _ *getRepositoryResponse, err error) {
				t.Helper()
				_, hasDecodeError := errors.AsType[*json.UnmarshalTypeError](err)
				assert.True(t, hasDecodeError)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := responseAPIClient(test.statusCode, test.header, test.body)
			response, err := invokeRepositoryQuery(client)

			if err == nil {
				assert.Equal(t, test.wantData, response != nil)
			}
			if err != nil {
				assert.Nil(t, response)
				partialErr, hasPartial := errors.AsType[*getRepositoryPartialDataError](err)
				assert.Equal(t, test.wantData, hasPartial)
				if hasPartial {
					response = partialErr.PartialData()
				}
			}
			test.check(t, response, err)
		})
	}
}

func TestDoPointerData(t *testing.T) {
	t.Run("object allocates inner pointer", func(t *testing.T) {
		client := responseAPIClient(
			http.StatusOK,
			http.Header{},
			`{"data":{"repository":{"name":"octoql"}}}`,
		)

		response, err := doOperation[*responseData](
			t.Context(),
			client,
			validOperationName,
			validOperationQuery,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, response)
		require.NotNil(t, *response)
		assert.Equal(t, "octoql", (*response).Repository.Name)
	})

	t.Run("null is not partial data", func(t *testing.T) {
		client := responseAPIClient(
			http.StatusOK,
			http.Header{},
			`{"data":null,"errors":[{"message":"no data"}]}`,
		)

		response, err := doOperation[*responseData](
			t.Context(),
			client,
			validOperationName,
			validOperationQuery,
			nil,
		)

		assert.Nil(t, response)
		_, hasErrors := errors.AsType[Errors](err)
		assert.True(t, hasErrors)
	})
}

func TestResponseErrorBoundsRawBody(t *testing.T) {
	body := strings.Repeat("x", 70*1024)
	client := responseAPIClient(
		http.StatusInternalServerError,
		http.Header{},
		body,
	)

	_, err := invokeRepositoryQuery(client)

	responseError, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok)
	assert.Len(t, responseError.RawBody, 64*1024)
	assert.True(t, responseError.RawBodyTruncated)
	assert.NotContains(t, responseError.Error(), body)
}

func TestClientResponseSizeLimit(t *testing.T) {
	const responseSizeLimit int64 = 64

	client := responseAPIClient(
		http.StatusOK,
		http.Header{
			"Retry-After":           {"30"},
			"X-GitHub-Request-ID":   {"request-too-large"},
			"X-RateLimit-Remaining": {"0"},
		},
		`{"data":{"repository":{"nameWithOwner":"`+
			strings.Repeat("x", int(responseSizeLimit))+
			`"}}}`,
	)

	assert.Equal(t, DefaultResponseSizeLimit, client.ResponseSizeLimit())
	err := client.SetResponseSizeLimit(responseSizeLimit)
	require.NoError(t, err)
	assert.Equal(t, responseSizeLimit, client.ResponseSizeLimit())

	response, err := invokeRepositoryQuery(client)

	require.Error(t, err)
	assert.Nil(t, response)

	limitError, ok := errors.AsType[*ResponseSizeLimitError](err)
	require.True(t, ok)
	assert.Equal(t, responseSizeLimit, limitError.Limit)

	responseError, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusOK, responseError.StatusCode)
	assert.Equal(t, "request-too-large", responseError.RequestID)
	assert.Len(t, responseError.RawBody, int(responseSizeLimit))
	assert.True(t, responseError.RawBodyTruncated)

	rateLimitError, ok := errors.AsType[*RateLimitError](err)
	require.True(t, ok)
	assert.Equal(t, RateLimitSecondary, rateLimitError.Kind)

	rateLimit, known := client.RateLimit()
	require.True(t, known)
	assert.Zero(t, rateLimit.Remaining)
}

func TestClientSetResponseSizeLimitRejectsNonpositiveValues(t *testing.T) {
	client := NewClient("https://api.github.com/graphql", nil)

	for _, limit := range []int64{0, -1} {
		err := client.SetResponseSizeLimit(limit)
		assert.EqualError(t, err, "octoql: response size limit must be greater than zero")
		assert.Equal(t, DefaultResponseSizeLimit, client.ResponseSizeLimit())
	}
}

func TestDoRejectsMissingDataAndErrorsResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "extensions only",
			body: `{"extensions":{"trace":"abc"}}`,
		},
		{
			name: "empty errors",
			body: `{"errors":[]}`,
		},
		{
			name: "null errors",
			body: `{"errors":null}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := responseAPIClient(
				http.StatusOK,
				http.Header{},
				test.body,
			)

			response, err := invokeRepositoryQuery(client)

			assert.Nil(t, response)
			responseError, ok := errors.AsType[*ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, test.body, string(responseError.RawBody))
		})
	}
}

func TestClientRateLimitSnapshot(t *testing.T) {
	headers := []http.Header{
		{
			"X-RateLimit-Limit":     {"5000"},
			"X-RateLimit-Remaining": {"4999"},
			"X-RateLimit-Used":      {"1"},
			"X-RateLimit-Resource":  {"graphql"},
		},
		{
			"X-RateLimit-Remaining": {"invalid"},
		},
	}
	var mu sync.Mutex
	request := 0
	httpClient := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			mu.Lock()
			header := headers[request]
			request++
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(`{"data":{}}`)),
			}, nil
		}),
	}
	client := NewClient("https://api.github.com/graphql", httpClient)

	rateLimit, known := client.RateLimit()
	assert.False(t, known)
	assert.Equal(t, RateLimit{}, rateLimit)

	_, err := invokeRepositoryQuery(client)
	require.NoError(t, err)
	rateLimit, known = client.RateLimit()
	require.True(t, known)
	assert.Equal(t, 5000, rateLimit.Limit)
	assert.Equal(t, 4999, rateLimit.Remaining)
	assert.Equal(t, 1, rateLimit.Used)
	assert.Equal(t, "graphql", rateLimit.Resource)
	assert.Zero(t, rateLimit.RetryAfter)
	assert.True(t, rateLimit.RetryAt.IsZero())

	_, err = invokeRepositoryQuery(client)
	require.NoError(t, err)
	preserved, known := client.RateLimit()
	require.True(t, known)
	assert.Equal(t, rateLimit, preserved)
}

func responseAPIClient(
	statusCode int,
	header http.Header,
	body string,
) *Client {
	return NewClient(
		"https://api.github.com/graphql",
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: statusCode,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	)
}

func invokeRepositoryQuery(client *Client) (*getRepositoryResponse, error) {
	return client.getRepository(getRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})
}
