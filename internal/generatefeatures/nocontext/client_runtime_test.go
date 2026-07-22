package nocontext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type testData struct {
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
}

const (
	validOperationName  = "Viewer"
	validOperationQuery = "query Viewer { viewer { login } }"
)

func TestDoHTTPResponses(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *testData, error)
		name       string
		body       string
		requestID  string
		statusCode int
	}{
		{
			name:       "unknown top-level error type",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"type":"A_FUTURE_GITHUB_ERROR","message":"future failure"}]}`,
			check: func(t *testing.T, _ *testData, err error) {
				t.Helper()
				var graphqlErrors Errors
				require.ErrorAs(t, err, &graphqlErrors)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, ErrorType("A_FUTURE_GITHUB_ERROR"), graphqlErrors[0].Type)
				encoded, marshalErr := json.Marshal(graphqlErrors[0])
				require.NoError(t, marshalErr)
				assert.Contains(t, string(encoded), `"type":"A_FUTURE_GITHUB_ERROR"`)
			},
		},
		{
			name:       "non-2xx invalid JSON",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"errors":[`,
			requestID:  "request-invalid-json",
			check: func(t *testing.T, _ *testData, err error) {
				t.Helper()
				responseError, ok := errors.AsType[*ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, `{"errors":[`, string(responseError.RawBody))
				assert.Equal(t, http.StatusServiceUnavailable, responseError.StatusCode)
				_, syntaxErrorFound := errors.AsType[*json.SyntaxError](err)
				assert.True(t, syntaxErrorFound)
			},
		},
		{
			name:       "malformed 2xx JSON",
			statusCode: http.StatusOK,
			body:       `{"data":`,
			requestID:  "request-malformed",
			check: func(t *testing.T, _ *testData, err error) {
				t.Helper()
				require.Error(t, err)
				_, responseErrorFound := errors.AsType[*ResponseError](err)
				assert.True(t, responseErrorFound)
				_, syntaxErrorFound := errors.AsType[*json.SyntaxError](err)
				assert.True(t, syntaxErrorFound)
			},
		},
		{
			name:       "2xx decode failure after GraphQL errors",
			statusCode: http.StatusOK,
			body: `{
				"errors":[{"type":"PARTIAL","message":"decoded before data"}],
				"data":{"repository":"not an object"}
			}`,
			check: func(t *testing.T, _ *testData, err error) {
				t.Helper()
				var graphqlErrors Errors
				require.ErrorAs(t, err, &graphqlErrors)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, ErrorType("PARTIAL"), graphqlErrors[0].Type)
				_, typeErrorFound := errors.AsType[*json.UnmarshalTypeError](err)
				assert.True(t, typeErrorFound)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("X-GitHub-Request-ID", test.requestID)
				writer.Header().Set("X-Test", "original")
				writer.WriteHeader(test.statusCode)
				_, err := io.WriteString(writer, test.body)
				if err != nil {
					panic(err)
				}
			}))
			defer server.Close()

			client := NewClient(server.URL, nil)
			response, err := doOperation[testData](
				t.Context(),
				client,
				"Repository",
				"query Repository { repository { name } }",
				nil,
			)
			assert.Nil(t, response)
			test.check(t, response, err)

			responseError, ok := errors.AsType[*ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, test.statusCode, responseError.StatusCode)
			assert.Equal(t, test.requestID, responseError.RequestID)
		})
	}
}

func TestDoRequest(t *testing.T) {
	type recordedRequest struct {
		header http.Header
		body   []byte
		method string
		url    string
	}

	requests := make(chan recordedRequest, 2)
	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			closeErr := request.Body.Close()
			if closeErr != nil {
				return nil, closeErr
			}
			requests <- recordedRequest{
				header: request.Header.Clone(),
				body:   body,
				method: request.Method,
				url:    request.URL.String(),
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"data":{}}`)),
			}, nil
		}),
	}

	client := NewClient("https://github.example/api/graphql", httpClient)
	operationName := "Repository"
	query := "query Repository($owner: String!) { repository(owner: $owner) { name } }"
	_, err := doOperation[struct{}](
		t.Context(),
		client,
		operationName,
		query,
		map[string]any{"owner": "octo"},
	)
	require.NoError(t, err)
	withVariables := <-requests
	assert.Equal(t, http.MethodPost, withVariables.method)
	assert.Equal(t, "https://github.example/api/graphql", withVariables.url)
	assert.Equal(t, "application/json", withVariables.header.Get("Content-Type"))
	wantBody := `{
		"query":"query Repository($owner: String!) { repository(owner: $owner) { name } }",
		"operationName":"Repository",
		"variables":{"owner":"octo"}
	}`
	assert.JSONEq(t, wantBody, string(withVariables.body))

	_, err = doOperation[struct{}](t.Context(), client, operationName, query, nil)
	require.NoError(t, err)
	withoutVariables := <-requests
	var requestObject map[string]json.RawMessage
	err = json.Unmarshal(withoutVariables.body, &requestObject)
	require.NoError(t, err)
	assert.NotContains(t, requestObject, "variables")
}

func TestClientBearerToken(t *testing.T) {
	authorization := make(chan string, 2)
	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			authorization <- request.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"data":{}}`)),
			}, nil
		}),
	}
	client := NewClient("https://github.example/api/graphql", httpClient)

	err := client.SetBearerToken("github_pat_first")
	require.NoError(t, err)
	_, err = doOperation[struct{}](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "Bearer github_pat_first", <-authorization)

	err = client.SetBearerToken("github_pat_second")
	require.NoError(t, err)
	_, err = doOperation[struct{}](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "Bearer github_pat_second", <-authorization)
}

func TestClientSetBearerTokenValidation(t *testing.T) {
	client := NewClient("https://github.example/api/graphql", nil)
	err := client.SetBearerToken("github_pat_valid")
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{name: "empty"},
		{name: "space", token: " "},
		{name: "padding only", token: "=="},
		{name: "embedded space", token: "github token"},
		{name: "embedded padding", token: "github_pat_valid=invalid"},
		{name: "header injection", token: "github_pat_valid\ninjected"},
		{name: "non-ASCII", token: "日本語"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := client.SetBearerToken(test.token)
			assert.EqualError(t, err, "octoql: invalid bearer token")
		})
	}
}

func TestNilClientSetBearerToken(t *testing.T) {
	var client *Client
	err := client.SetBearerToken("github_pat_valid")
	assert.EqualError(t, err, "octoql: client is nil")
}

func TestClientExecuteNilClient(t *testing.T) {
	var client *Client
	var response testData

	hasData, err := client._octoqlExecute(
		t.Context(),
		_octoqlPayload{
			OperationName: validOperationName,
			Query:         validOperationQuery,
		},
		&response,
	)

	assert.False(t, hasData)
	assert.EqualError(t, err, "octoql: client is nil")
}

func TestDoFailurePhases(t *testing.T) {
	readError := errors.New("body read failed")
	transportError := errors.New("transport failed")

	tests := []struct {
		client            *Client
		ctx               context.Context
		variables         any
		wantCause         error
		name              string
		wantResponseError bool
	}{
		{
			name: "variables cannot be encoded",
			ctx:  t.Context(),
			client: NewClient(
				"https://api.github.com/graphql",
				&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					require.FailNow(t, "transport called for unencodable variables")
					return nil, nil
				})},
			),
			variables: make(chan int),
		},
		{
			name: "transport error",
			ctx:  t.Context(),
			client: NewClient(
				"https://api.github.com/graphql",
				&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, transportError
				})},
			),
			wantCause: transportError,
		},
		{
			name: "unreadable 2xx body",
			ctx:  t.Context(),
			client: newStaticResponseClient(
				http.StatusOK,
				http.Header{"X-Github-Request-Id": {"request-read-2xx"}},
				&errorReadCloser{err: readError},
			),
			wantCause:         readError,
			wantResponseError: true,
		},
		{
			name: "unreadable non-2xx body",
			ctx:  t.Context(),
			client: newStaticResponseClient(
				http.StatusBadGateway,
				http.Header{"X-Github-Request-Id": {"request-read-error"}},
				&errorReadCloser{err: readError},
			),
			wantCause:         readError,
			wantResponseError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := doOperation[struct{}](
				test.ctx,
				test.client,
				validOperationName,
				validOperationQuery,
				test.variables,
			)
			require.Error(t, err)
			assert.Nil(t, response)
			if test.wantCause != nil {
				assert.ErrorIs(t, err, test.wantCause)
			}
			responseError, hasResponseError := errors.AsType[*ResponseError](err)
			assert.Equal(t, test.wantResponseError, hasResponseError)
			if test.wantResponseError {
				require.NotNil(t, responseError)
				assert.NotEmpty(t, responseError.RequestID)
			}
		})
	}
}

func TestDoReturnsResponseErrorWithRedirectError(t *testing.T) {
	redirectError := errors.New("redirect rejected")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			_, err := io.WriteString(writer, `{"data":{}}`)
			if err != nil {
				panic(err)
			}
			return
		}
		writer.Header().Set("Location", "/redirected")
		writer.Header().Set("X-GitHub-Request-ID", "redirect-request")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return redirectError
	}
	client := NewClient(server.URL, httpClient)

	response, err := doOperation[struct{}](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
	assert.Nil(t, response)
	assert.ErrorIs(t, err, redirectError)
	responseError, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusFound, responseError.StatusCode)
	assert.Equal(t, "redirect-request", responseError.RequestID)
}

func TestDoDecodesBodyBeforeReturningCloseError(t *testing.T) {
	closeError := errors.New("body close failed")
	client := newStaticResponseClient(
		http.StatusOK,
		nil,
		&closeErrorReadCloser{
			Reader: strings.NewReader(`{
				"data":{"repository":{"name":"partial"}},
				"errors":[{"message":"field failed"}]
			}`),
			err: closeError,
		},
	)

	response, err := doOperation[testData](
		t.Context(),
		client,
		validOperationName,
		validOperationQuery,
		nil,
	)
	require.NotNil(t, response)
	assert.Equal(t, "partial", response.Repository.Name)
	assert.ErrorIs(t, err, closeError)
	_, graphqlErrorsFound := errors.AsType[Errors](err)
	assert.True(t, graphqlErrorsFound)
	_, responseErrorFound := errors.AsType[*ResponseError](err)
	assert.True(t, responseErrorFound)
}

func doOperation[T any](
	ctx context.Context,
	client *Client,
	operationName string,
	query string,
	variables any,
) (*T, error) {
	response := new(T)
	hasData, err := client._octoqlExecute(ctx, _octoqlPayload{
		OperationName: operationName,
		Query:         query,
		Variables:     variables,
	}, response)
	if !hasData {
		return nil, err
	}
	return response, err
}

func newStaticResponseClient(statusCode int, header http.Header, body io.ReadCloser) *Client {
	return NewClient(
		"https://api.github.com/graphql",
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: statusCode,
				Header:     header,
				Body:       body,
			}, nil
		})},
	)
}

type errorReadCloser struct {
	err error
}

func (b *errorReadCloser) Read([]byte) (int, error) {
	return 0, b.err
}

func (b *errorReadCloser) Close() error {
	return nil
}

type closeErrorReadCloser struct {
	io.Reader
	err error
}

func (b *closeErrorReadCloser) Close() error {
	return b.err
}
