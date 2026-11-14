package handlertest_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	githubapi "github.com/willabides/octoql/internal/handlertest/client"
	"github.com/willabides/octoql/internal/handlertest/githubapitest"
)

func TestGeneratedHandlerOperationsAndErrors(t *testing.T) {
	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := githubapi.NewClient(server.URL, http.DefaultClient)

	handler.ExpectViewer().Respond(githubapitest.ViewerResponse{
		Viewer: githubapitest.ViewerViewerUser{
			ViewerVariables: githubapitest.ViewerVariables{
				Id:    "U1",
				Login: "octocat",
			},
		},
	})
	viewerResponse, err := client.Viewer(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "octocat", viewerResponse.Viewer.Login)

	dynamicVariables := githubapitest.EchoPropertyVariables{
		Value: json.RawMessage(`"dynamic"`),
	}
	receivedVariables := make(chan githubapitest.EchoPropertyVariables, 1)
	handler.ExpectEchoProperty(dynamicVariables).Handle(func(
		variables githubapitest.EchoPropertyVariables,
		writer http.ResponseWriter,
	) {
		receivedVariables <- variables
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, writeErr := writer.Write([]byte(`{"data":{"echoProperty":"handled"}}`))
		assert.NoError(t, writeErr)
	})
	dynamicResponse, err := client.EchoProperty(t.Context(), dynamicVariables)
	require.NoError(t, err)
	assert.JSONEq(t, `"handled"`, string(dynamicResponse.EchoProperty))
	assert.JSONEq(t, string(dynamicVariables.Value), string((<-receivedVariables).Value))

	errorVariables := githubapitest.GetNodeVariables{Id: "missing"}
	handler.ExpectGetNode(errorVariables).RespondError(githubapi.Error{
		Type:    "NOT_FOUND",
		Message: "repository not found",
		Path:    githubapi.Path{"node"},
		Locations: []githubapi.Location{{
			Line:   2,
			Column: 3,
		}},
		Extensions: map[string]any{"code": "missing"},
	})
	nodeResponse, err := client.GetNode(t.Context(), errorVariables)
	assert.Nil(t, nodeResponse)
	graphqlErrors, ok := errors.AsType[githubapi.Errors](err)
	require.True(t, ok)
	require.Len(t, graphqlErrors, 1)
	assert.Equal(t, 2, graphqlErrors[0].Locations[0].Line)

	variables := repositoryVariables()
	handler.ExpectGetRepository(variables).RespondDataAndErrors(
		repositoryData(),
		githubapi.Error{
			Type:    "FORBIDDEN",
			Message: "one field is unavailable",
			Path:    githubapi.Path{"repository", "propertyValue"},
		},
	)
	partial, err := client.GetRepository(t.Context(), variables)
	require.Error(t, err)
	assert.Nil(t, partial)
	_, ok = errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
	require.True(t, ok)
	_, wrongOperation := errors.AsType[*githubapi.GetNodePartialDataError](err)
	assert.False(t, wrongOperation)
}

func TestGeneratedHandlerRateLimits(t *testing.T) {
	t.Run("primary rate limit at http 200", func(t *testing.T) {
		handler := githubapitest.NewTestHandler(t)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client := githubapi.NewClient(server.URL, http.DefaultClient)
		variables := githubapitest.GetNodeVariables{Id: "primary"}
		reset := time.Date(2026, time.July, 19, 13, 0, 0, 0, time.UTC)
		handler.ExpectGetNode(variables).RespondError(
			githubapi.Error{
				Type:    "RATE_LIMITED",
				Message: "quota exhausted",
			},
			githubapitest.WithPrimaryRateLimit(githubapi.RateLimit{
				Limit:     5000,
				Remaining: 0,
				Used:      5000,
				Reset:     reset,
				Resource:  "graphql",
			}),
		)

		response, err := client.GetNode(t.Context(), variables)
		assert.Nil(t, response)
		rateLimitError, ok := errors.AsType[*githubapi.RateLimitError](err)
		require.True(t, ok)
		assert.Equal(t, githubapi.RateLimitPrimary, rateLimitError.Kind)
		require.NotNil(t, rateLimitError.RateLimit)
		assert.Equal(t, reset, rateLimitError.RetryAt())
		rateLimit, known := client.RateLimit()
		require.True(t, known)
		assert.Equal(t, *rateLimitError.RateLimit, rateLimit)
	})

	t.Run("primary rate limit without headers leaves RateLimit nil", func(t *testing.T) {
		handler := githubapitest.NewTestHandler(t)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client := githubapi.NewClient(server.URL, http.DefaultClient)
		variables := githubapitest.GetNodeVariables{Id: "primary-no-headers"}
		handler.ExpectGetNode(variables).RespondError(
			githubapi.Error{
				Type:    "RATE_LIMITED",
				Message: "quota exhausted",
			},
		)

		response, err := client.GetNode(t.Context(), variables)
		assert.Nil(t, response)
		rateLimitError, ok := errors.AsType[*githubapi.RateLimitError](err)
		require.True(t, ok)
		assert.Equal(t, githubapi.RateLimitPrimary, rateLimitError.Kind)
		assert.Nil(t, rateLimitError.RateLimit)
		assert.True(t, rateLimitError.RetryAt().IsZero())
		_, known := client.RateLimit()
		assert.False(t, known)
	})

	for _, status := range []int{
		http.StatusForbidden,
		http.StatusTooManyRequests,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := githubapitest.NewTestHandler(t)
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			client := githubapi.NewClient(server.URL, http.DefaultClient)
			variables := githubapitest.GetNodeVariables{Id: http.StatusText(status)}
			handler.ExpectGetNode(variables).RespondError(
				githubapi.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
				githubapitest.WithSecondaryRateLimit(30*time.Second),
				githubapitest.WithStatus(status),
			)

			response, err := client.GetNode(t.Context(), variables)
			assert.Nil(t, response)
			rateLimitError, ok := errors.AsType[*githubapi.RateLimitError](err)
			require.True(t, ok)
			assert.Equal(t, githubapi.RateLimitSecondary, rateLimitError.Kind)
			assert.Equal(t, 30*time.Second, rateLimitError.RetryAfter)
			responseError, ok := errors.AsType[*githubapi.ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, status, responseError.StatusCode)
		})
	}
}

func TestGeneratedHandlerConcurrentRequests(t *testing.T) {
	const requestCount = 32

	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := githubapi.NewClient(server.URL, http.DefaultClient)
	variables := githubapitest.EchoPropertyVariables{
		Value: json.RawMessage(`"concurrent"`),
	}
	handler.ExpectEchoProperty(
		variables,
		githubapitest.Times(requestCount),
	).Respond(githubapitest.EchoPropertyResponse{
		EchoProperty: variables.Value,
	})

	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, requestCount)
	for range requestCount {
		waitGroup.Go(func() {
			_, err := client.EchoProperty(t.Context(), variables)
			errorsChannel <- err
		})
	}
	waitGroup.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		require.NoError(t, err)
	}
}

func repositoryVariables() githubapitest.GetRepositoryVariables {
	return githubapitest.GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
		First: 2,
		After: new("cursor-1"),
	}
}

func repositoryData() githubapitest.GetRepositoryResponse {
	return githubapitest.GetRepositoryResponse{
		Repository: new(githubapitest.GetRepositoryRepository{
			Id:            "R1",
			FullName:      "octo-org/octo-repo",
			UpdatedAt:     time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
			PropertyValue: json.RawMessage(`["red","blue"]`),
			Issues: githubapitest.GetRepositoryRepositoryIssuesIssueConnection{
				Nodes: []githubapitest.GetRepositoryRepositoryIssuesIssueConnectionNodesIssue{{
					Id:    "I1",
					Title: "bug",
				}},
				PageInfo: githubapitest.GetRepositoryRepositoryIssuesIssueConnectionPageInfo{
					HasNextPage: true,
					EndCursor:   new("cursor-2"),
				},
			},
		}),
	}
}

func requirePtrValue[T any](t *testing.T, value *T) T {
	t.Helper()
	require.NotNil(t, value)
	return *value
}
