package nocontext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
)

type exampleViewerResponse struct {
	Viewer struct {
		Login string `json:"login"`
	} `json:"viewer"`
}

type exampleRepositoryResponse struct {
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
}

type exampleViewerPartialDataError struct {
	data *exampleViewerResponse
	err  error
}

func (e *exampleViewerPartialDataError) Error() string { return e.err.Error() }
func (e *exampleViewerPartialDataError) Unwrap() error { return e.err }
func (e *exampleViewerPartialDataError) PartialData() *exampleViewerResponse {
	return e.data
}

type exampleRepositoryPartialDataError struct {
	data *exampleRepositoryResponse
	err  error
}

func (e *exampleRepositoryPartialDataError) Error() string { return e.err.Error() }
func (e *exampleRepositoryPartialDataError) Unwrap() error { return e.err }
func (e *exampleRepositoryPartialDataError) PartialData() *exampleRepositoryResponse {
	return e.data
}

func ExampleNewClient() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(writer, `{"data":{"viewer":{"login":"octocat"}}}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	response, err := getViewer(context.Background(), client)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(response.Viewer.Login)
	// Output: octocat
}

func ExampleErrors_partialData() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(writer, `{
			"data":{"repository":{"name":"octoql"}},
			"errors":[{"type":"FORBIDDEN","message":"owner unavailable","path":["repository","owner"]}]
		}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	response, err := getRepository(context.Background(), client)

	graphqlErrors, ok := errors.AsType[Errors](err)
	if ok {
		fmt.Println(response == nil)
		partialErr, hasPartialData := errors.AsType[*exampleRepositoryPartialDataError](err)
		if hasPartialData {
			fmt.Println(partialErr.PartialData().Repository.Name)
		}
		fmt.Println(graphqlErrors[0].Type)
	}
	// Output:
	// true
	// octoql
	// FORBIDDEN
}

func ExampleRateLimitError() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "30")
		_, err := io.WriteString(writer, `{"errors":[{"message":"slow down"}]}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := getViewer(context.Background(), client)

	rateLimitError, ok := errors.AsType[*RateLimitError](err)
	fmt.Println(ok)
	fmt.Println(rateLimitError.Kind)
	// Output:
	// true
	// secondary
}

func ExampleResponseError() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-GitHub-Request-ID", "request-example")
		writer.WriteHeader(http.StatusForbidden)
		_, err := io.WriteString(
			writer,
			`{"errors":[{"type":"FORBIDDEN","message":"request rejected"}]}`,
		)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := getViewer(context.Background(), client)

	responseError, ok := errors.AsType[*ResponseError](err)
	if !ok {
		fmt.Println("response error not found")
		return
	}
	graphqlErrors, ok := errors.AsType[Errors](err)
	if !ok {
		fmt.Println("graphql errors not found")
		return
	}

	fmt.Println(responseError != nil)
	fmt.Println(responseError.StatusCode)
	fmt.Println(responseError.RequestID)
	fmt.Println(graphqlErrors[0].Type)
	// Output:
	// true
	// 403
	// request-example
	// FORBIDDEN
}

func ExampleClient_RateLimit() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", "4999")
		writer.Header().Set("X-RateLimit-Used", "1")
		_, err := io.WriteString(writer, `{"data":{}}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := getViewer(context.Background(), client)
	if err != nil {
		fmt.Println(err)
		return
	}

	rateLimit, known := client.RateLimit()
	fmt.Println(known)
	fmt.Println(rateLimit.Limit)
	fmt.Println(rateLimit.Remaining)
	// Output:
	// true
	// 5000
	// 4999
}

func getViewer(
	ctx context.Context,
	client *Client,
) (*exampleViewerResponse, error) {
	return executeExample[exampleViewerResponse](
		ctx,
		client,
		exampleOperation[exampleViewerResponse]{
			name:  "Viewer",
			query: "query Viewer { viewer { login } }",
			newPartialDataError: func(data *exampleViewerResponse, err error) error {
				return &exampleViewerPartialDataError{
					data: data,
					err:  err,
				}
			},
		},
	)
}

func getRepository(
	ctx context.Context,
	client *Client,
) (*exampleRepositoryResponse, error) {
	return executeExample[exampleRepositoryResponse](
		ctx,
		client,
		exampleOperation[exampleRepositoryResponse]{
			name:  "Repository",
			query: "query Repository { repository { name owner { login } } }",
			newPartialDataError: func(data *exampleRepositoryResponse, err error) error {
				return &exampleRepositoryPartialDataError{
					data: data,
					err:  err,
				}
			},
		},
	)
}

type exampleOperation[T any] struct {
	newPartialDataError func(*T, error) error
	name                string
	query               string
}

func executeExample[T any](
	ctx context.Context,
	client *Client,
	operation exampleOperation[T],
) (*T, error) {
	response := new(T)
	hasData, err := client._octoqlExecute(ctx, _octoqlPayload{
		OperationName: operation.name,
		Query:         operation.query,
	}, response)
	if !hasData {
		return nil, err
	}
	if err != nil {
		return nil, operation.newPartialDataError(response, err)
	}
	return response, nil
}
