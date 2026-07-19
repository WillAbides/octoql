package octoql_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
)

type responseData struct {
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
	Count int `json:"count"`
}

func TestDoReturnsConcreteData(t *testing.T) {
	client := responseAPIClient(
		http.StatusOK,
		http.Header{},
		`{"data":{"repository":{"name":"octoql"}}}`,
	)

	response, err := responseAPIData[responseData](t, client)

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "octoql", response.Repository.Name)
}

func TestDoIgnoresTopLevelExtensions(t *testing.T) {
	client := responseAPIClient(
		http.StatusOK,
		http.Header{},
		`{
			"data":{"repository":{"name":"octoql"}},
			"extensions":{"trace":"abc"}
		}`,
	)

	response, err := octoql.Do[responseData](
		t.Context(),
		client,
		validOperation(),
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "octoql", response.Repository.Name)
}

func TestDoPointerData(t *testing.T) {
	t.Run("object allocates inner pointer", func(t *testing.T) {
		client := responseAPIClient(
			http.StatusOK,
			http.Header{},
			`{"data":{"repository":{"name":"octoql"}}}`,
		)

		response, err := octoql.Do[*responseData](
			t.Context(),
			client,
			validOperation(),
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, response)
		require.NotNil(t, *response)
		assert.Equal(t, "octoql", (*response).Repository.Name)
	})

	t.Run("null preserves nil inner pointer", func(t *testing.T) {
		client := responseAPIClient(
			http.StatusOK,
			http.Header{},
			`{"data":null}`,
		)

		response, err := octoql.Do[*responseData](
			t.Context(),
			client,
			validOperation(),
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, response)
		assert.Nil(t, *response)
	})
}

func TestDoReturnsResponseErrorFacets(t *testing.T) {
	client := responseAPIClient(
		http.StatusOK,
		http.Header{"X-GitHub-Request-ID": {"request-partial"}},
		`{
			"data":{"repository":{"name":"partial"}},
			"errors":[{
				"type":"FORBIDDEN",
				"message":"owner unavailable",
				"path":["repository","owner"],
				"extensions":{"code":"missing"}
			}]
		}`,
	)

	response, err := responseAPIData[responseData](t, client)

	require.NotNil(t, response)
	assert.Equal(t, "partial", response.Repository.Name)

	responseError, ok := errors.AsType[*octoql.ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusOK, responseError.StatusCode)
	assert.Equal(t, "request-partial", responseError.RequestID)
	assert.Empty(t, responseError.RawBody)

	graphqlErrors, ok := errors.AsType[octoql.Errors](err)
	require.True(t, ok)
	require.Len(t, graphqlErrors, 1)
	assert.Equal(t, octoql.ErrorType("FORBIDDEN"), graphqlErrors[0].Type)
	assert.Equal(t, "missing", graphqlErrors[0].Extensions["code"])

	_, ok = errors.AsType[*octoql.RateLimitError](err)
	assert.False(t, ok)
}

func TestDoDoesNotPublishFailedDataDecode(t *testing.T) {
	body := `{"data":{"repository":{"name":"must not escape"},"count":"invalid"}}`
	client := responseAPIClient(
		http.StatusOK,
		http.Header{"X-GitHub-Request-ID": {"request-decode"}},
		body,
	)

	response, err := responseAPIData[responseData](t, client)

	require.NotNil(t, response)
	assert.Empty(t, response.Repository.Name)
	assert.Zero(t, response.Count)

	responseError, ok := errors.AsType[*octoql.ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, body, string(responseError.RawBody))
}

func TestDoNonSuccessfulResponseError(t *testing.T) {
	body := `{"errors":[{"type":"FORBIDDEN","message":"rejected"}]}`
	client := responseAPIClient(
		http.StatusForbidden,
		http.Header{"X-GitHub-Request-ID": {"request-forbidden"}},
		body,
	)

	response, err := responseAPIData[responseData](t, client)

	require.NotNil(t, response)
	responseError, ok := errors.AsType[*octoql.ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
	assert.Equal(t, "request-forbidden", responseError.RequestID)
	assert.Equal(t, body, string(responseError.RawBody))

	_, ok = errors.AsType[octoql.Errors](err)
	assert.True(t, ok)
}

func TestResponseErrorBoundsRawBody(t *testing.T) {
	body := strings.Repeat("x", 70*1024)
	client := responseAPIClient(
		http.StatusInternalServerError,
		http.Header{},
		body,
	)

	_, err := responseAPIData[responseData](t, client)

	responseError, ok := errors.AsType[*octoql.ResponseError](err)
	require.True(t, ok)
	assert.Len(t, responseError.RawBody, 64*1024)
	assert.True(t, responseError.RawBodyTruncated)
	assert.NotContains(t, responseError.Error(), body)
}

func TestDoRejectsExtensionsOnlyResponse(t *testing.T) {
	body := `{"extensions":{"trace":"abc"}}`
	client := responseAPIClient(
		http.StatusOK,
		http.Header{},
		body,
	)

	response, err := octoql.Do[responseData](
		t.Context(),
		client,
		validOperation(),
		nil,
	)
	require.NotNil(t, response)
	require.NotNil(t, response)
	responseError, ok := errors.AsType[*octoql.ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, body, string(responseError.RawBody))
}

func TestDoRejectsEmptyErrorOnlyResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
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

			response, err := octoql.Do[responseData](
				t.Context(),
				client,
				validOperation(),
				nil,
			)

			require.NotNil(t, response)
			responseError, ok := errors.AsType[*octoql.ResponseError](err)
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
	client := octoql.NewClient("https://api.github.com/graphql", httpClient)

	rateLimit, known := client.RateLimit()
	assert.False(t, known)
	assert.Equal(t, octoql.RateLimit{}, rateLimit)

	_, err := octoql.Do[struct{}](t.Context(), client, validOperation(), nil)
	require.NoError(t, err)
	rateLimit, known = client.RateLimit()
	require.True(t, known)
	assert.Equal(t, 5000, rateLimit.Limit)
	assert.Equal(t, 4999, rateLimit.Remaining)
	assert.Equal(t, 1, rateLimit.Used)
	assert.Equal(t, "graphql", rateLimit.Resource)
	assert.Zero(t, rateLimit.RetryAfter)
	assert.True(t, rateLimit.RetryAt.IsZero())

	_, err = octoql.Do[struct{}](t.Context(), client, validOperation(), nil)
	require.NoError(t, err)
	preserved, known := client.RateLimit()
	require.True(t, known)
	assert.Equal(t, rateLimit, preserved)
}

func responseAPIClient(
	statusCode int,
	header http.Header,
	body string,
) *octoql.Client {
	return octoql.NewClient(
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

func responseAPIData[T any](
	t *testing.T,
	client *octoql.Client,
) (*T, error) {
	return octoql.Do[T](
		t.Context(),
		client,
		validOperation(),
		nil,
	)
}
