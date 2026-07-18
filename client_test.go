// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package octoql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
				if err != nil {
					t.Fatalf("Do() error = %v, want nil", err)
				}
				if response == nil {
					t.Fatal("Do() response = nil, want non-nil")
				}
				if response.Data.Repository.Name != "octoql" {
					t.Errorf("Do() data name = %q, want %q", response.Data.Repository.Name, "octoql")
				}
				if response.Extensions["trace"] != "abc" {
					t.Errorf("Do() extension trace = %#v, want %q", response.Extensions["trace"], "abc")
				}
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
				if response == nil {
					t.Fatal("Do() response = nil, want non-nil")
				}
				if response.Data.Repository.Name != "octoql" {
					t.Errorf("Do() partial data name = %q, want %q", response.Data.Repository.Name, "octoql")
				}
				if response.Extensions["trace"] != "partial" {
					t.Errorf("Do() extension trace = %#v, want %q", response.Extensions["trace"], "partial")
				}
				var graphqlErrors octoql.Errors
				if !errors.As(err, &graphqlErrors) {
					t.Fatalf("Do() error = %T, want octoql.Errors", err)
				}
				if len(graphqlErrors) != 1 {
					t.Fatalf("len(graphqlErrors) = %d, want 1", len(graphqlErrors))
				}
				got := graphqlErrors[0]
				if got.Type != octoql.ErrorType("FORBIDDEN") {
					t.Errorf("error type = %q, want %q", got.Type, "FORBIDDEN")
				}
				wantPath := octoql.Path{"repository", "owner", 0, "login"}
				if !pathsEqual(got.Path, wantPath) {
					t.Errorf("error path = %#v, want %#v", got.Path, wantPath)
				}
				wantLocations := []octoql.Location{{Line: 2, Column: 3}}
				if len(got.Locations) != 1 || got.Locations[0] != wantLocations[0] {
					t.Errorf("error locations = %#v, want %#v", got.Locations, wantLocations)
				}
			},
		},
		{
			name:       "unknown top-level error type",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"type":"A_FUTURE_GITHUB_ERROR","message":"future failure"}]}`,
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				if response == nil {
					t.Fatal("Do() response = nil, want non-nil")
				}
				var graphqlErrors octoql.Errors
				if !errors.As(err, &graphqlErrors) {
					t.Fatalf("Do() error = %T, want octoql.Errors", err)
				}
				if graphqlErrors[0].Type != octoql.ErrorType("A_FUTURE_GITHUB_ERROR") {
					t.Errorf("error type = %q, want unchanged unknown type", graphqlErrors[0].Type)
				}
				encoded, marshalErr := json.Marshal(graphqlErrors[0])
				if marshalErr != nil {
					t.Fatalf("json.Marshal(error) error = %v", marshalErr)
				}
				if !strings.Contains(string(encoded), `"type":"A_FUTURE_GITHUB_ERROR"`) {
					t.Errorf("marshaled error = %s, want unknown type", encoded)
				}
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
				if response == nil {
					t.Fatal("Do() response = nil, want non-nil")
				}
				if response.Data.Repository.Name != "partial" {
					t.Errorf("Do() decoded data name = %q, want %q", response.Data.Repository.Name, "partial")
				}
				httpError, ok := errors.AsType[*octoql.HTTPError](err)
				if !ok {
					t.Fatalf("Do() error = %T, want *octoql.HTTPError", err)
				}
				if httpError.HTTP.StatusCode != http.StatusBadRequest {
					t.Errorf("HTTPError status = %d, want %d", httpError.HTTP.StatusCode, http.StatusBadRequest)
				}
				if string(httpError.Body) != strings.TrimSpace(`{
				"data":{"repository":{"name":"partial"}},
				"errors":[{"type":"BAD_REQUEST","message":"invalid selection"}],
				"extensions":{"trace":"rejected"}
			}`) {
					t.Errorf("HTTPError body = %q, want raw payload", httpError.Body)
				}
				graphqlErrors, found := errors.AsType[octoql.Errors](err)
				if !found {
					t.Fatal("errors.AsType[octoql.Errors]() = false, want true")
				}
				if graphqlErrors[0].Type != octoql.ErrorType("BAD_REQUEST") {
					t.Errorf("decoded error type = %q, want %q", graphqlErrors[0].Type, "BAD_REQUEST")
				}
			},
		},
		{
			name:       "non-2xx invalid JSON",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"errors":[`,
			requestID:  "request-invalid-json",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				if response == nil {
					t.Fatal("Do() response = nil, want non-nil")
				}
				httpError, ok := errors.AsType[*octoql.HTTPError](err)
				if !ok {
					t.Fatalf("Do() error = %T, want *octoql.HTTPError", err)
				}
				if string(httpError.Body) != `{"errors":[` {
					t.Errorf("HTTPError body = %q, want raw invalid JSON", httpError.Body)
				}
				if httpError.HTTP.StatusCode != http.StatusServiceUnavailable {
					t.Errorf("HTTPError status = %d, want %d", httpError.HTTP.StatusCode, http.StatusServiceUnavailable)
				}
				_, syntaxErrorFound := errors.AsType[*json.SyntaxError](err)
				if !syntaxErrorFound {
					t.Errorf("errors.AsType[*json.SyntaxError]() = false, want true; error = %v", err)
				}
			},
		},
		{
			name:       "malformed 2xx JSON",
			statusCode: http.StatusOK,
			body:       `{"data":`,
			requestID:  "request-malformed",
			check: func(t *testing.T, response *octoql.Response[testData], err error) {
				t.Helper()
				if response == nil {
					t.Fatal("Do() response = nil, want non-nil")
				}
				if err == nil {
					t.Fatal("Do() error = nil, want decode error")
				}
				var httpError *octoql.HTTPError
				if errors.As(err, &httpError) {
					t.Errorf("Do() error = %T, do not want HTTPError for 2xx response", err)
				}
				_, syntaxErrorFound := errors.AsType[*json.SyntaxError](err)
				if !syntaxErrorFound {
					t.Errorf("errors.AsType[*json.SyntaxError]() = false, want true; error = %v", err)
				}
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

			if response == nil {
				return
			}
			if response.HTTP.StatusCode != test.statusCode {
				t.Errorf("response status = %d, want %d", response.HTTP.StatusCode, test.statusCode)
			}
			if response.HTTP.RequestID != test.requestID {
				t.Errorf("response request ID = %q, want %q", response.HTTP.RequestID, test.requestID)
			}
			if response.HTTP.Header.Get("X-Test") != "original" {
				t.Errorf("response header X-Test = %q, want %q", response.HTTP.Header.Get("X-Test"), "original")
			}
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
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}
	withVariables := <-requests
	if withVariables.method != http.MethodPost {
		t.Errorf("request method = %q, want POST", withVariables.method)
	}
	if withVariables.url != "https://github.example/api/graphql" {
		t.Errorf("request URL = %q, want GHES endpoint", withVariables.url)
	}
	if withVariables.header.Get("Content-Type") != "application/json" {
		t.Errorf("request Content-Type = %q, want application/json", withVariables.header.Get("Content-Type"))
	}
	wantBody := `{
		"query":"query Repository($owner: String!) { repository(owner: $owner) { name } }",
		"operationName":"Repository",
		"variables":{"owner":"octo"}
	}`
	if !jsonEqual(withVariables.body, []byte(wantBody)) {
		t.Errorf("request body = %s, want %s", withVariables.body, wantBody)
	}

	_, err = octoql.Do[struct{}](t.Context(), client, operation, nil)
	if err != nil {
		t.Fatalf("Do() without variables error = %v, want nil", err)
	}
	withoutVariables := <-requests
	var requestObject map[string]json.RawMessage
	err = json.Unmarshal(withoutVariables.body, &requestObject)
	if err != nil {
		t.Fatalf("json.Unmarshal(request body) error = %v", err)
	}
	if _, exists := requestObject["variables"]; exists {
		t.Errorf("request body = %s, want variables omitted", withoutVariables.body)
	}
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
					t.Fatal("transport called for unencodable variables")
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
			if err == nil {
				t.Fatal("Do() error = nil, want non-nil")
			}
			if (response != nil) != test.wantResponse {
				t.Errorf("Do() response presence = %t, want %t", response != nil, test.wantResponse)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Errorf("Do() error = %v, want cause %v", err, test.wantCause)
			}
			_, isHTTPError := errors.AsType[*octoql.HTTPError](err)
			if isHTTPError != test.wantHTTP {
				t.Errorf("Do() HTTPError presence = %t, want %t", isHTTPError, test.wantHTTP)
			}
			if response != nil && response.HTTP.RequestID == "" {
				t.Error("Do() response request ID is empty, want metadata populated")
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
	if response == nil {
		t.Fatal("Do() response = nil, want metadata-bearing redirect response")
	}
	if response.HTTP.StatusCode != http.StatusFound {
		t.Errorf("response status = %d, want %d", response.HTTP.StatusCode, http.StatusFound)
	}
	if response.HTTP.RequestID != "redirect-request" {
		t.Errorf("response request ID = %q, want %q", response.HTTP.RequestID, "redirect-request")
	}
	if !errors.Is(err, redirectError) {
		t.Errorf("Do() error = %v, want redirect cause", err)
	}
	_, ok := errors.AsType[*octoql.HTTPError](err)
	if !ok {
		t.Errorf("Do() error = %T, want *octoql.HTTPError", err)
	}
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
	if response == nil {
		t.Fatal("Do() response = nil, want non-nil")
	}
	httpError, ok := errors.AsType[*octoql.HTTPError](err)
	if !ok {
		t.Fatalf("Do() error = %T, want *octoql.HTTPError", err)
	}

	transportHeader.Set("X-GitHub-Request-ID", "mutated-request")
	transportHeader.Set("X-Test", "mutated-value")
	if response.HTTP.RequestID != "original-request" {
		t.Errorf("response request ID = %q, want original value", response.HTTP.RequestID)
	}
	if response.HTTP.Header.Get("X-Test") != "original-value" {
		t.Errorf("response header = %q, want original value", response.HTTP.Header.Get("X-Test"))
	}
	if httpError.HTTP.RequestID != "original-request" {
		t.Errorf("HTTPError request ID = %q, want original value", httpError.HTTP.RequestID)
	}
	if httpError.HTTP.Header.Get("X-Test") != "original-value" {
		t.Errorf("HTTPError header = %q, want original value", httpError.HTTP.Header.Get("X-Test"))
	}

	response.HTTP.Header.Set("X-Test", "response-mutated")
	if httpError.HTTP.Header.Get("X-Test") != "original-value" {
		t.Errorf("HTTPError header = %q after response mutation, want independent clone", httpError.HTTP.Header.Get("X-Test"))
	}
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
	if response == nil {
		t.Fatal("Do() response = nil, want decoded response")
	}
	if response.Data.Repository.Name != "partial" {
		t.Errorf("response data name = %q, want %q", response.Data.Repository.Name, "partial")
	}
	if !errors.Is(err, closeError) {
		t.Errorf("Do() error = %v, want close cause", err)
	}
	_, graphqlErrorsFound := errors.AsType[octoql.Errors](err)
	if !graphqlErrorsFound {
		t.Error("errors.AsType[octoql.Errors]() = false, want decoded errors")
	}

	response.HTTP.Header.Set("X-Test", "writable")
	if response.HTTP.Header.Get("X-Test") != "writable" {
		t.Errorf("response header = %q, want writable empty clone", response.HTTP.Header.Get("X-Test"))
	}
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

func pathsEqual(left, right octoql.Path) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func jsonEqual(left, right []byte) bool {
	var leftValue any
	err := json.Unmarshal(left, &leftValue)
	if err != nil {
		return false
	}
	var rightValue any
	err = json.Unmarshal(right, &rightValue)
	if err != nil {
		return false
	}
	leftJSON, err := json.Marshal(leftValue)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(rightValue)
	if err != nil {
		return false
	}
	return bytes.Equal(leftJSON, rightJSON)
}
