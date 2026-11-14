package nocontext

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDoHTTPResponses(t *testing.T) {
	tests := []struct {
		check      func(*testing.T, error)
		name       string
		body       string
		requestID  string
		statusCode int
	}{
		{
			name:       "unknown top-level error type",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"type":"A_FUTURE_GITHUB_ERROR","message":"future failure"}]}`,
			check: func(t *testing.T, err error) {
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
			check: func(t *testing.T, err error) {
				t.Helper()
				responseError, ok := errors.AsType[*ResponseError](err)
				require.True(t, ok)
				truncatedBody := []byte(`{"errors":[`)
				assert.Equal(t, truncatedBody, responseError.RawBody)
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
			check: func(t *testing.T, err error) {
				t.Helper()
				require.Error(t, err)
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
			check: func(t *testing.T, err error) {
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
				writer.WriteHeader(test.statusCode)
				_, err := io.WriteString(writer, test.body)
				if err != nil {
					panic(err)
				}
			}))
			defer server.Close()

			client := NewClient(server.URL, nil)
			response, err := invokeRepositoryQuery(client)
			assert.Nil(t, response)
			test.check(t, err)

			responseError, ok := errors.AsType[*ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, test.statusCode, responseError.StatusCode)
			assert.Equal(t, test.requestID, responseError.RequestID)
		})
	}
}

func TestDoRequestAndAuth(t *testing.T) {
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
	err := client.SetBearerToken("github_pat_first")
	require.NoError(t, err)
	_, err = invokeRepositoryQuery(client)
	require.NoError(t, err)
	withVariables := <-requests
	assert.Equal(t, http.MethodPost, withVariables.method)
	assert.Equal(t, "https://github.example/api/graphql", withVariables.url)
	assert.Equal(t, "application/json", withVariables.header.Get("Content-Type"))
	assert.Equal(t, "Bearer github_pat_first", withVariables.header.Get("Authorization"))
	var withVariablesPayload map[string]json.RawMessage
	err = json.Unmarshal(withVariables.body, &withVariablesPayload)
	require.NoError(t, err)
	assert.JSONEq(t, `"getRepository"`, string(withVariablesPayload["operationName"]))
	assert.JSONEq(t, `{"owner":"octo-org","name":"octo-repo"}`, string(withVariablesPayload["variables"]))

	err = client.SetBearerToken("github_pat_second")
	require.NoError(t, err)
	_, err = client.wireSemantics()
	require.NoError(t, err)
	withoutVariables := <-requests
	assert.Equal(t, "Bearer github_pat_second", withoutVariables.header.Get("Authorization"))
	var withoutVariablesPayload map[string]json.RawMessage
	err = json.Unmarshal(withoutVariables.body, &withoutVariablesPayload)
	require.NoError(t, err)
	assert.NotContains(t, withoutVariablesPayload, "variables")

	for _, invalid := range []string{
		"",
		" ",
		"==",
		"github token",
		"github_pat_valid=invalid",
		"github_pat_valid\ninjected",
		"日本語",
	} {
		err = client.SetBearerToken(invalid)
		require.EqualError(t, err, "octoql: invalid bearer token")
	}

	var nilClient *Client
	require.EqualError(t, nilClient.SetBearerToken("github_pat_valid"), "octoql: client is nil")
	require.EqualError(t, nilClient.SetAllowRedirects(true), "octoql: client is nil")
	assert.False(t, nilClient.AllowRedirects())
	response, err := invokeRepositoryQuery(nilClient)
	assert.Nil(t, response)
	require.EqualError(t, err, "octoql: client is nil")
}

func TestDoFailurePhases(t *testing.T) {
	readError := errors.New("body read failed")
	transportError := errors.New("transport failed")
	closeError := errors.New("body close failed")

	tests := []struct {
		client            *Client
		wantCause         error
		name              string
		wantPartialOwner  string
		wantResponseError bool
	}{
		{
			name: "transport error",
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
			client: newStaticResponseClient(
				http.StatusBadGateway,
				http.Header{"X-Github-Request-Id": {"request-read-error"}},
				&errorReadCloser{err: readError},
			),
			wantCause:         readError,
			wantResponseError: true,
		},
		{
			name: "body close error preserves decoded partial data",
			client: newStaticResponseClient(
				http.StatusOK,
				http.Header{"X-Github-Request-Id": {"request-close"}},
				&closeErrorReadCloser{
					Reader: strings.NewReader(`{
						"data":{"repository":{"nameWithOwner":"octo-org/partial"}},
						"errors":[{"message":"field failed"}]
					}`),
					err: closeError,
				},
			),
			wantCause:         closeError,
			wantPartialOwner:  "octo-org/partial",
			wantResponseError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := invokeRepositoryQuery(test.client)
			require.Error(t, err)
			assert.Nil(t, response)
			require.ErrorIs(t, err, test.wantCause)
			responseError, hasResponseError := errors.AsType[*ResponseError](err)
			assert.Equal(t, test.wantResponseError, hasResponseError)
			if test.wantResponseError {
				require.NotNil(t, responseError)
				assert.NotEmpty(t, responseError.RequestID)
			}
			if test.wantPartialOwner != "" {
				partialErr, ok := errors.AsType[*getRepositoryPartialDataError](err)
				require.True(t, ok)
				assert.Equal(t, test.wantPartialOwner, partialErr.PartialData().Repository.NameWithOwner)
				_, graphqlErrorsFound := errors.AsType[Errors](err)
				assert.True(t, graphqlErrorsFound)
			}
		})
	}
}

func TestClientRedirectPolicy(t *testing.T) {
	t.Run("refuses redirects by default and on zero-value client", func(t *testing.T) {
		targetRequests := make(chan struct{}, 1)
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			targetRequests <- struct{}{}
		}))
		defer target.Close()

		for _, location := range []string{
			strings.Replace(target.URL, "127.0.0.1", "localhost", 1),
			target.URL,
		} {
			origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Location", location)
				writer.Header().Set("X-GitHub-Request-ID", "redirect-request")
				writer.WriteHeader(http.StatusFound)
			}))
			client := NewClient(origin.URL, nil)
			response, err := client.wireSemantics()
			origin.Close()

			require.Error(t, err)
			assert.Nil(t, response)
			require.ErrorContains(t, err, "redirect refused")
			require.ErrorContains(t, err, location)
			responseError, ok := errors.AsType[*ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, http.StatusFound, responseError.StatusCode)
			assert.Equal(t, "redirect-request", responseError.RequestID)
			assert.Nil(t, responseError.RawBody)
		}

		zeroOrigin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Location", "/redirected")
			writer.WriteHeader(http.StatusFound)
		}))
		defer zeroOrigin.Close()
		zeroClient := Client{endpoint: zeroOrigin.URL}
		_, err := zeroClient.wireSemantics()
		require.ErrorContains(t, err, "redirect refused")

		select {
		case <-targetRequests:
			assert.Fail(t, "redirect target received a request")
		default:
		}
	})

	t.Run("follows same-origin redirects overriding caller policy and caps hops", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/loop" {
				writer.Header().Set("Location", "/loop")
				writer.WriteHeader(http.StatusFound)
				return
			}
			if request.URL.Path == "/redirected" {
				_, err := io.WriteString(writer, `{"data":{}}`)
				if err != nil {
					panic(err)
				}
				return
			}
			writer.Header().Set("Location", "/redirected")
			writer.WriteHeader(http.StatusFound)
		}))
		defer server.Close()

		httpClient := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("caller redirect policy")
			},
		}
		client := NewClient(server.URL, httpClient)
		assert.NotSame(t, httpClient, client.httpClient)
		require.NotNil(t, httpClient.CheckRedirect)
		require.Nil(t, http.DefaultClient.CheckRedirect)
		err := client.SetAllowRedirects(true)
		require.NoError(t, err)
		assert.True(t, client.AllowRedirects())

		response, err := client.wireSemantics()
		require.NoError(t, err)
		assert.NotNil(t, response)

		loopClient := NewClient(server.URL+"/loop", nil)
		require.NoError(t, loopClient.SetAllowRedirects(true))
		_, err = loopClient.wireSemantics()
		require.ErrorContains(t, err, "stopped after 10 redirects")
	})

	t.Run("strips bearer token across origins and multi-hop redirects", func(t *testing.T) {
		for _, targetLocation := range []func(*httptest.Server) string{
			func(server *httptest.Server) string {
				return server.URL + "/final"
			},
			func(server *httptest.Server) string {
				return strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
			},
		} {
			authorization := make(chan string, 1)
			target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/final" {
					writer.Header().Set("Location", "/final")
					writer.WriteHeader(http.StatusFound)
					return
				}
				authorization <- request.Header.Get("Authorization")
				_, err := io.WriteString(writer, `{"data":{}}`)
				if err != nil {
					panic(err)
				}
			}))

			origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Location", targetLocation(target))
				writer.WriteHeader(http.StatusFound)
			}))

			client := NewClient(origin.URL, nil)
			require.NoError(t, client.SetBearerToken("github_pat_token"))
			require.NoError(t, client.SetAllowRedirects(true))
			response, err := client.wireSemantics()
			origin.Close()
			target.Close()

			require.NoError(t, err)
			assert.NotNil(t, response)
			assert.Empty(t, <-authorization)
		}
	})
}

func TestClientSetAllowRedirectsConcurrentWithRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			_, err := io.WriteString(writer, `{"data":{}}`)
			if err != nil {
				panic(err)
			}
			return
		}
		writer.Header().Set("Location", "/redirected")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	results := make(chan error, 200)
	var waitGroup sync.WaitGroup
	for i := range 100 {
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			results <- client.SetAllowRedirects(i%2 == 0)
		}()
		go func() {
			defer waitGroup.Done()
			_, err := client.wireSemantics()
			results <- err
		}()
	}
	waitGroup.Wait()
	close(results)

	for err := range results {
		if err == nil {
			continue
		}
		require.ErrorContains(t, err, "redirect refused")
	}
}

func newResponseClient(statusCode int, header http.Header, body string) *Client {
	return newStaticResponseClient(statusCode, header, io.NopCloser(strings.NewReader(body)))
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
