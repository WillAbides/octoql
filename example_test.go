// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

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
	response, err := octoql.Do[viewerData](
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Viewer",
			Query: "query Viewer { viewer { login } }",
		},
		nil,
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(response.Data.Viewer.Login)
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
	response, err := octoql.Do[repositoryData](
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Repository",
			Query: "query Repository { repository { name owner { login } } }",
		},
		nil,
	)

	var graphqlErrors octoql.Errors
	if errors.As(err, &graphqlErrors) {
		fmt.Println(response.Data.Repository.Name)
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
	_, err := octoql.Do[struct{}](
		context.Background(),
		client,
		octoql.Operation{
			Name:  "Viewer",
			Query: "query Viewer { viewer { login } }",
		},
		nil,
	)

	rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
	fmt.Println(ok)
	fmt.Println(rateLimitError.Kind)
	// Output:
	// true
	// secondary
}
