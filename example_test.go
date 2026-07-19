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

func ExampleDo() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(writer, `{"data":{"viewer":{"login":"octocat"}}}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	type viewerData struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}

	client := octoql.NewClient(server.URL, server.Client())
	var response viewerData
	err := octoql.Do(
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Viewer",
			Query: "query Viewer { viewer { login } }",
		},
		nil,
		&response,
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(response.Viewer.Login)
	// Output: octocat
}

func ExampleDo_partialData() {
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

	type repositoryData struct {
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
	}

	client := octoql.NewClient(server.URL, server.Client())
	var response repositoryData
	err := octoql.Do(
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Repository",
			Query: "query Repository { repository { name owner { login } } }",
		},
		nil,
		&response,
	)

	var graphqlErrors octoql.Errors
	if errors.As(err, &graphqlErrors) {
		fmt.Println(response.Repository.Name)
		fmt.Println(graphqlErrors[0].Type)
	}
	// Output:
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
	var response struct{}
	err := octoql.Do(
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Viewer",
			Query: "query Viewer { viewer { login } }",
		},
		nil,
		&response,
	)

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
	var response struct{}
	err := octoql.Do(
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Viewer",
			Query: "query Viewer { viewer { login } }",
		},
		nil,
		&response,
	)

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
	var response struct{}
	err := octoql.Do(
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Viewer",
			Query: "query Viewer { viewer { login } }",
		},
		nil,
		&response,
	)
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
