package octoql_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/willabides/octoql"
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

func ExampleNewClient() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(writer, `{"data":{"viewer":{"login":"octocat"}}}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := octoql.NewClient(server.URL, server.Client())
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

	client := octoql.NewClient(server.URL, server.Client())
	response, err := getRepository(context.Background(), client)

	var graphqlErrors octoql.Errors
	if errors.As(err, &graphqlErrors) {
		fmt.Println(response == nil)
		var partial *exampleRepositoryResponse
		if octoql.GetPartialData(err, &partial) {
			fmt.Println(partial.Repository.Name)
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

	client := octoql.NewClient(server.URL, server.Client())
	_, err := getViewer(context.Background(), client)

	rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
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

	client := octoql.NewClient(server.URL, server.Client())
	_, err := getViewer(context.Background(), client)

	responseError, ok := errors.AsType[*octoql.ResponseError](err)
	if !ok {
		fmt.Println("response error not found")
		return
	}
	graphqlErrors, ok := errors.AsType[octoql.Errors](err)
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

	client := octoql.NewClient(server.URL, server.Client())
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
	client *octoql.Client,
) (*exampleViewerResponse, error) {
	return executeExample[exampleViewerResponse](
		ctx,
		client,
		"Viewer",
		"query Viewer { viewer { login } }",
	)
}

func getRepository(
	ctx context.Context,
	client *octoql.Client,
) (*exampleRepositoryResponse, error) {
	return executeExample[exampleRepositoryResponse](
		ctx,
		client,
		"Repository",
		"query Repository { repository { name owner { login } } }",
	)
}

func executeExample[T any](
	ctx context.Context,
	client *octoql.Client,
	operationName string,
	query string,
) (*T, error) {
	response := new(T)
	hasData, err := client.Execute(ctx, octoql.Payload{
		OperationName: operationName,
		Query:         query,
	}, response)
	if !hasData {
		return nil, err
	}
	if err != nil {
		return nil, octoql.NewPartialDataError(response, err)
	}
	return response, nil
}
