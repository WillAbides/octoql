// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package octoql_test

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
	"github.com/willabides/octoql"
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

func TestDoHTTPResponses(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, *octoql.Response[testData], error)
		name       string
		body       string
		requestID  string
		statusCode int
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			body:       `{"data":{"repository":{"name":"octoql"}},"extensions":{"trace":"abc"}}`,
			requestID:  "request-success",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, response)
				assert.Equal(t, "octoql", response.Data.Repository.Name)
				assert.Equal(t, "abc", response.Extensions["trace"])
			},
		},
		{
			name:       "partial data and errors",
			statusCode: http.StatusOK,
			body: `{
				"data":{"repository":{"name":"octoql"}},
				"errors":[{
					"type":"FORBIDDEN",
					"message":"field unavailable",
					"path":["repository","owner",0,"login"],
					"locations":[{"line":2,"column":3}],
					"extensions":{"code":"FORBIDDEN"}
				}],
				"extensions":{"trace":"partial"}
			}`,
			requestID: "request-partial",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "octoql", response.Data.Repository.Name)
				assert.Equal(t, "partial", response.Extensions["trace"])
				var graphqlErrors octoql.Errors
				require.ErrorAs(t, err, &graphqlErrors)
				require.Len(t, graphqlErrors, 1)
				got := graphqlErrors[0]
				assert.Equal(t, octoql.ErrorType("FORBIDDEN"), got.Type)
				assert.Equal(t, octoql.Path{"repository", "owner", 0, "login"}, got.Path)
				assert.Equal(t, []octoql.Location{{Line: 2, Column: 3}}, got.Locations)
			},
		},
		{
			name:       "unknown top-level error type",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"type":"A_FUTURE_GITHUB_ERROR","message":"future failure"}]}`,
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				require.NotNil(t, response)
				var graphqlErrors octoql.Errors
				require.ErrorAs(t, err, &graphqlErrors)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, octoql.ErrorType("A_FUTURE_GITHUB_ERROR"), graphqlErrors[0].Type)
				encoded, marshalErr := json.Marshal(graphqlErrors[0])
				require.NoError(t, marshalErr)
				assert.Contains(t, string(encoded), `"type":"A_FUTURE_GITHUB_ERROR"`)
			},
		},
		{
			name:       "non-2xx GraphQL response",
			statusCode: http.StatusBadRequest,
			body: `{
				"data":{"repository":{"name":"partial"}},
				"errors":[{"type":"BAD_REQUEST","message":"invalid selection"}],
				"extensions":{"trace":"rejected"}
			}`,
			requestID: "request-rejected",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				require.NotNil(t, response)
				assert.Equal(t, "partial", response.Data.Repository.Name)
				httpError, ok := errors.AsType[*octoql.HTTPError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusBadRequest, httpError.HTTP.StatusCode)
				assert.Equal(t, strings.TrimSpace(`{
				"data":{"repository":{"name":"partial"}},
				"errors":[{"type":"BAD_REQUEST","message":"invalid selection"}],
				"extensions":{"trace":"rejected"}
			}`), string(httpError.Body))
				graphqlErrors, found := errors.AsType[octoql.Errors](err)
				require.True(t, found)
				require.Len(t, graphqlErrors, 1)
				assert.Equal(t, octoql.ErrorType("BAD_REQUEST"), graphqlErrors[0].Type)
			},
		},
		{
			name:       "non-2xx invalid JSON",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"errors":[`,
			requestID:  "request-invalid-json",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				require.NotNil(t, response)
				httpError, ok := errors.AsType[*octoql.HTTPError](err)
				require.True(t, ok)
				assert.Equal(t, `{"errors":[`, string(httpError.Body))
				assert.Equal(t, http.StatusServiceUnavailable, httpError.HTTP.StatusCode)
				_, syntaxErrorFound := errors.AsType[*json.SyntaxError](err)
				assert.True(t, syntaxErrorFound)
			},
		},
		{
			name:       "malformed 2xx JSON",
			statusCode: http.StatusOK,
			body:       `{"data":`,
			requestID:  "request-malformed",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				require.NotNil(t, response)
				require.Error(t, err)
				var httpError *octoql.HTTPError
				assert.False(t, errors.As(err, &httpError))
				_, syntaxErrorFound := errors.AsType[*json.SyntaxError](err)
				assert.True(t, syntaxErrorFound)
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

			client := octoql.NewClient(server.URL, nil)
			operation := octoql.Operation{
				Name:  "Repository",
				Query: "query Repository { repository { name } }",
			}
			response, err := octoql.Do[testData](t.Context(), client, operation, nil)
			test.check(t, response, err)

			require.NotNil(t, response)
			assert.Equal(t, test.statusCode, response.HTTP.StatusCode)
			assert.Equal(t, test.requestID, response.HTTP.RequestID)
			assert.Equal(t, "original", response.HTTP.Header.Get("X-Test"))
		})
	}
}

func TestDoRequest(t *testing.T) {
	//nolint:govet // The fixture follows the request's presentation order.
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

	client := octoql.NewClient("https://github.example/api/graphql", httpClient)
	operation := octoql.Operation{
		Name:  "Repository",
		Query: "query Repository($owner: String!) { repository(owner: $owner) { name } }",
	}
	_, err := octoql.Do[struct{}](t.Context(), client, operation, map[string]any{"owner": "octo"})
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

	_, err = octoql.Do[struct{}](t.Context(), client, operation, nil)
	require.NoError(t, err)
	withoutVariables := <-requests
	var requestObject map[string]json.RawMessage
	err = json.Unmarshal(withoutVariables.body, &requestObject)
	require.NoError(t, err)
	assert.NotContains(t, requestObject, "variables")
}

func TestDoFailurePhases(t *testing.T) {
	readError := errors.New("body read failed")
	transportError := errors.New("transport failed")

	tests := []struct {
		client       *octoql.Client
		operation    octoql.Operation
		ctx          context.Context
		variables    any
		wantCause    error
		name         string
		wantResponse bool
		wantHTTP     bool
	}{
		{
			name:      "nil client",
			ctx:       t.Context(),
			operation: validOperation(),
		},
		{
			name:      "empty endpoint",
			ctx:       t.Context(),
			client:    octoql.NewClient("", nil),
			operation: validOperation(),
		},
		{
			name:      "invalid endpoint",
			ctx:       t.Context(),
			client:    octoql.NewClient("://bad endpoint", nil),
			operation: validOperation(),
		},
		{
			name:      "unsupported endpoint scheme",
			ctx:       t.Context(),
			client:    octoql.NewClient("ftp://github.example/graphql", nil),
			operation: validOperation(),
		},
		{
			name:      "empty operation name",
			ctx:       t.Context(),
			client:    octoql.NewClient("https://api.github.com/graphql", nil),
			operation: octoql.Operation{Query: "query Viewer { viewer { login } }"},
		},
		{
			name:      "empty operation query",
			ctx:       t.Context(),
			client:    octoql.NewClient("https://api.github.com/graphql", nil),
			operation: octoql.Operation{Name: "Viewer"},
		},
		{
			name:      "invalid operation name",
			ctx:       t.Context(),
			client:    octoql.NewClient("https://api.github.com/graphql", nil),
			operation: octoql.Operation{Name: "Not Valid", Query: "query Viewer { viewer { login } }"},
		},
		{
			name:      "nil context",
			client:    octoql.NewClient("https://api.github.com/graphql", nil),
			operation: validOperation(),
		},
		{
			name: "variables cannot be encoded",
			ctx:  t.Context(),
			client: octoql.NewClient(
				"https://api.github.com/graphql",
				&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					require.FailNow(t, "transport called for unencodable variables")
					return nil, nil
				})},
			),
			operation: validOperation(),
			variables: make(chan int),
		},
		{
			name:      "transport error",
			ctx:       t.Context(),
			operation: validOperation(),
			client: octoql.NewClient(
				"https://api.github.com/graphql",
				&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, transportError
				})},
			),
			wantCause: transportError,
		},
		{
			name:      "unreadable 2xx body",
			ctx:       t.Context(),
			operation: validOperation(),
			client: newStaticResponseClient(
				http.StatusOK,
				http.Header{"X-Github-Request-Id": {"request-read-2xx"}},
				&errorReadCloser{err: readError},
			),
			wantCause:    readError,
			wantResponse: true,
		},
		{
			name:      "unreadable non-2xx body",
			ctx:       t.Context(),
			operation: validOperation(),
			client: newStaticResponseClient(
				http.StatusBadGateway,
				http.Header{"X-Github-Request-Id": {"request-read-error"}},
				&errorReadCloser{err: readError},
			),
			wantCause:    readError,
			wantResponse: true,
			wantHTTP:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := octoql.Do[struct{}](test.ctx, test.client, test.operation, test.variables)
			require.Error(t, err)
			assert.Equal(t, test.wantResponse, response != nil)
			if test.wantCause != nil {
				assert.ErrorIs(t, err, test.wantCause)
			}
			_, isHTTPError := errors.AsType[*octoql.HTTPError](err)
			assert.Equal(t, test.wantHTTP, isHTTPError)
			if test.wantResponse {
				require.NotNil(t, response)
				assert.NotEmpty(t, response.HTTP.RequestID)
			}
		})
	}
}

func TestDoReturnsMetadataWithRedirectError(t *testing.T) {
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
	client := octoql.NewClient(server.URL, httpClient)

	response, err := octoql.Do[struct{}](t.Context(), client, validOperation(), nil)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusFound, response.HTTP.StatusCode)
	assert.Equal(t, "redirect-request", response.HTTP.RequestID)
	assert.ErrorIs(t, err, redirectError)
	_, ok := errors.AsType[*octoql.HTTPError](err)
	assert.True(t, ok)
}

func TestDoClonesResponseHeaders(t *testing.T) {
	transportHeader := http.Header{
		"X-GitHub-Request-ID": {"original-request"},
		"X-Test":              {"original-value"},
	}
	client := newStaticResponseClient(
		http.StatusBadRequest,
		transportHeader,
		io.NopCloser(strings.NewReader(`{"errors":[{"message":"rejected"}]}`)),
	)

	response, err := octoql.Do[struct{}](t.Context(), client, validOperation(), nil)
	require.NotNil(t, response)
	httpError, ok := errors.AsType[*octoql.HTTPError](err)
	require.True(t, ok)

	transportHeader.Set("X-GitHub-Request-ID", "mutated-request")
	transportHeader.Set("X-Test", "mutated-value")
	assert.Equal(t, "original-request", response.HTTP.RequestID)
	assert.Equal(t, "original-value", response.HTTP.Header.Get("X-Test"))
	assert.Equal(t, "original-request", httpError.HTTP.RequestID)
	assert.Equal(t, "original-value", httpError.HTTP.Header.Get("X-Test"))

	response.HTTP.Header.Set("X-Test", "response-mutated")
	assert.Equal(t, "original-value", httpError.HTTP.Header.Get("X-Test"))
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

	response, err := octoql.Do[testData](t.Context(), client, validOperation(), nil)
	require.NotNil(t, response)
	assert.Equal(t, "partial", response.Data.Repository.Name)
	assert.ErrorIs(t, err, closeError)
	_, graphqlErrorsFound := errors.AsType[octoql.Errors](err)
	assert.True(t, graphqlErrorsFound)

	response.HTTP.Header.Set("X-Test", "writable")
	assert.Equal(t, "writable", response.HTTP.Header.Get("X-Test"))
}

func validOperation() octoql.Operation {
	return octoql.Operation{
		Name:  "Viewer",
		Query: "query Viewer { viewer { login } }",
	}
}

func newStaticResponseClient(statusCode int, header http.Header, body io.ReadCloser) *octoql.Client {
	return octoql.NewClient(
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

func (body *errorReadCloser) Read([]byte) (int, error) {
	return 0, body.err
}

func (body *errorReadCloser) Close() error {
	return nil
}

type closeErrorReadCloser struct {
	io.Reader
	err error
}

func (body *closeErrorReadCloser) Close() error {
	return body.err
}
