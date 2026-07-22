package nocontext

import (
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

	response, err := doOperation[responseData](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
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

	responseError, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusOK, responseError.StatusCode)
	assert.Equal(t, "request-partial", responseError.RequestID)
	assert.Empty(t, responseError.RawBody)

	graphqlErrors, ok := errors.AsType[Errors](err)
	require.True(t, ok)
	require.Len(t, graphqlErrors, 1)
	assert.Equal(t, ErrorType("FORBIDDEN"), graphqlErrors[0].Type)
	assert.Equal(t, "missing", graphqlErrors[0].Extensions["code"])

	_, ok = errors.AsType[*RateLimitError](err)
	assert.False(t, ok)
}

func TestDoNonSuccessfulResponseError(t *testing.T) {
	body := `{"errors":[{"type":"FORBIDDEN","message":"rejected"}]}`
	client := responseAPIClient(
		http.StatusForbidden,
		http.Header{"X-GitHub-Request-ID": {"request-forbidden"}},
		body,
	)

	response, err := responseAPIData[responseData](t, client)

	assert.Nil(t, response)
	responseError, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
	assert.Equal(t, "request-forbidden", responseError.RequestID)
	assert.Equal(t, body, string(responseError.RawBody))

	_, ok = errors.AsType[Errors](err)
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
		`{"data":{"repository":{"name":"`+
			strings.Repeat("x", int(responseSizeLimit))+
			`"}}}`,
	)

	assert.Equal(t, DefaultResponseSizeLimit, client.ResponseSizeLimit())
	err := client.SetResponseSizeLimit(responseSizeLimit)
	require.NoError(t, err)
	assert.Equal(t, responseSizeLimit, client.ResponseSizeLimit())

	response, err := responseAPIData[responseData](t, client)

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

func TestDoRejectsExtensionsOnlyResponse(t *testing.T) {
	body := `{"extensions":{"trace":"abc"}}`
	client := responseAPIClient(
		http.StatusOK,
		http.Header{},
		body,
	)

	response, err := doOperation[responseData](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
	assert.Nil(t, response)
	responseError, ok := errors.AsType[*ResponseError](err)
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

			response, err := doOperation[responseData](
				t.Context(),
				client,
				validOperationName,
				validOperationQuery,
				nil,
			)

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

	_, err := doOperation[struct{}](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
	require.NoError(t, err)
	rateLimit, known = client.RateLimit()
	require.True(t, known)
	assert.Equal(t, 5000, rateLimit.Limit)
	assert.Equal(t, 4999, rateLimit.Remaining)
	assert.Equal(t, 1, rateLimit.Used)
	assert.Equal(t, "graphql", rateLimit.Resource)
	assert.Zero(t, rateLimit.RetryAfter)
	assert.True(t, rateLimit.RetryAt.IsZero())

	_, err = doOperation[struct{}](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
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

func responseAPIData[T any](
	t *testing.T,
	client *Client,
) (*T, error) {
	return doOperation[T](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
}
