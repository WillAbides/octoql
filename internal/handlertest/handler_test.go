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
	"github.com/willabides/octoql"
	githubapi "github.com/willabides/octoql/internal/handlertest/client"
	"github.com/willabides/octoql/internal/handlertest/githubapitest"
)

func TestGeneratedHandlerSuccessMutationAndScalars(t *testing.T) {
	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := octoql.NewClient(server.URL, http.DefaultClient)

	handler.ExpectViewer().Respond(githubapitest.ViewerResponse{
		Viewer: githubapitest.ViewerViewerUser{
			ViewerVariables: githubapitest.ViewerVariables{
				Id:    "U1",
				Login: "octocat",
			},
		},
	})
	viewerResponse, err := githubapi.Viewer(t.Context(), client)
	require.NoError(t, err)
	assert.Equal(t, "octocat", viewerResponse.Data.Viewer.Login)

	variables := repositoryVariables()
	data := repositoryData()
	handler.ExpectGetRepository(variables).Respond(data)

	response, err := githubapi.GetRepository(
		t.Context(),
		client,
		variables.Owner,
		variables.Name,
		variables.First,
		variables.After,
	)
	require.NoError(t, err)
	assert.Equal(t, "octo-org/octo-repo", response.Data.Repository.FullName)
	assert.Equal(t, time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC), response.Data.Repository.UpdatedAt)
	assert.JSONEq(t, `["red","blue"]`, string(response.Data.Repository.PropertyValue))
	require.Len(t, response.Data.Repository.Issues.Nodes, 1)
	assert.Equal(t, "bug", response.Data.Repository.Issues.Nodes[0].Title)
	assert.Equal(t, "cursor-2", response.Data.Repository.Issues.PageInfo.EndCursor)

	input := githubapitest.CreateRepositoryInput{
		Name:             "created",
		OwnerId:          "O1",
		Visibility:       githubapitest.RepositoryVisibilityPrivate,
		ClientMutationId: "mutation-1",
	}
	mutationData := githubapitest.CreateRepositoryResponse{
		CreateRepository: githubapitest.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
			Repository: githubapitest.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
				Id:            "R2",
				NameWithOwner: "octo-org/created",
				UpdatedAt:     time.Date(2026, time.July, 19, 13, 0, 0, 0, time.UTC),
			},
			ClientMutationId: "mutation-1",
		},
	}
	handler.ExpectCreateRepository(githubapitest.CreateRepositoryVariables{
		Input: input,
	}).Respond(mutationData)

	mutationResponse, err := githubapi.CreateRepository(t.Context(), client, input)
	require.NoError(t, err)
	assert.Equal(t, "octo-org/created", mutationResponse.Data.CreateRepository.Repository.NameWithOwner)
	assert.Equal(t, "mutation-1", mutationResponse.Data.CreateRepository.ClientMutationId)

	property := json.RawMessage(`["one","two"]`)
	handler.ExpectEchoProperty(githubapitest.EchoPropertyVariables{
		Value: property,
	}).Respond(githubapitest.EchoPropertyResponse{
		EchoProperty: property,
	})

	propertyResponse, err := githubapi.EchoProperty(t.Context(), client, property)
	require.NoError(t, err)
	assert.JSONEq(t, string(property), string(propertyResponse.Data.EchoProperty))

	temporalValue := time.Now()
	handler.ExpectEchoAt(githubapitest.EchoAtVariables{
		Value: temporalValue,
	}).Respond(githubapitest.EchoAtResponse{
		EchoAt: temporalValue,
	})
	temporalResponse, err := githubapi.EchoAt(t.Context(), client, temporalValue)
	require.NoError(t, err)
	assert.True(t, temporalValue.Equal(temporalResponse.Data.EchoAt))

	const largeInteger = int64(9_007_199_254_740_993)
	handler.ExpectEchoAny(githubapitest.EchoAnyVariables{
		Value: largeInteger,
	}).Respond(githubapitest.EchoAnyResponse{
		EchoAny: largeInteger,
	})
	_, err = githubapi.EchoAny(t.Context(), client, largeInteger)
	require.NoError(t, err)
}

func TestGeneratedHandlerGraphQLErrorsAndPartialData(t *testing.T) {
	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := octoql.NewClient(server.URL, http.DefaultClient)

	errorVariables := githubapitest.GetNodeVariables{Id: "missing"}
	handler.ExpectGetNode(errorVariables).RespondError(octoql.Error{
		Type:    "NOT_FOUND",
		Message: "repository not found",
		Path:    octoql.Path{"node"},
		Locations: []octoql.Location{{
			Line:   2,
			Column: 3,
		}},
		Extensions: map[string]any{"code": "missing"},
	})

	response, err := githubapi.GetNode(t.Context(), client, errorVariables.Id)
	require.NotNil(t, response)
	graphqlErrors, ok := errors.AsType[octoql.Errors](err)
	require.True(t, ok)
	require.Len(t, graphqlErrors, 1)
	assert.Equal(t, octoql.ErrorType("NOT_FOUND"), graphqlErrors[0].Type)
	assert.Equal(t, octoql.Path{"node"}, graphqlErrors[0].Path)
	assert.Equal(t, "missing", graphqlErrors[0].Extensions["code"])
	require.Len(t, graphqlErrors[0].Locations, 1)
	assert.Equal(t, 2, graphqlErrors[0].Locations[0].Line)

	variables := repositoryVariables()
	data := repositoryData()
	handler.ExpectGetRepository(variables).RespondDataAndErrors(
		data,
		octoql.Error{
			Type:    "FORBIDDEN",
			Message: "one field is unavailable",
			Path:    octoql.Path{"repository", "propertyValue"},
		},
	)

	partial, err := githubapi.GetRepository(
		t.Context(),
		client,
		variables.Owner,
		variables.Name,
		variables.First,
		variables.After,
	)
	require.Error(t, err)
	require.NotNil(t, partial)
	assert.Equal(t, "octo-org/octo-repo", partial.Data.Repository.FullName)
}

func TestGeneratedHandlerResponseOptionsAndRateLimits(t *testing.T) {
	t.Run("headers status and extensions", func(t *testing.T) {
		handler := githubapitest.NewTestHandler(t)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client := octoql.NewClient(server.URL, http.DefaultClient)
		variables := repositoryVariables()
		headers := http.Header{
			"X-GitHub-Request-ID": {"request-123"},
			"X-Test":              {"original"},
		}
		extensions := map[string]any{"trace": "abc"}
		handler.ExpectGetRepository(variables).Respond(
			repositoryData(),
			githubapitest.WithStatus(http.StatusAccepted),
			githubapitest.WithHeaders(headers),
			githubapitest.WithExtensions(extensions),
		)
		headers.Set("X-Test", "mutated")
		extensions["trace"] = "mutated"

		response, err := githubapi.GetRepository(
			t.Context(),
			client,
			variables.Owner,
			variables.Name,
			variables.First,
			variables.After,
		)
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, response.HTTP.StatusCode)
		assert.Equal(t, "request-123", response.HTTP.RequestID)
		assert.Equal(t, "original", response.HTTP.Header.Get("X-Test"))
		assert.Equal(t, "abc", response.Extensions["trace"])
	})

	t.Run("primary rate limit at http 200", func(t *testing.T) {
		handler := githubapitest.NewTestHandler(t)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client := octoql.NewClient(server.URL, http.DefaultClient)
		variables := githubapitest.GetNodeVariables{Id: "primary"}
		reset := time.Date(2026, time.July, 19, 13, 0, 0, 0, time.UTC)
		handler.ExpectGetNode(variables).RespondError(
			octoql.Error{Type: "RATE_LIMITED", Message: "quota exhausted"},
			githubapitest.WithPrimaryRateLimit(octoql.RateLimit{
				Limit:     5000,
				Remaining: 0,
				Used:      5000,
				Reset:     reset,
				Resource:  "graphql",
			}),
		)

		response, err := githubapi.GetNode(t.Context(), client, variables.Id)
		require.NotNil(t, response)
		rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
		require.True(t, ok)
		assert.Equal(t, octoql.RateLimitPrimary, rateLimitError.Kind)
		assert.Equal(t, 5000, response.HTTP.RateLimit.Limit)
		assert.Equal(t, reset, response.HTTP.RateLimit.Reset)
		assert.Equal(t, "graphql", response.HTTP.RateLimit.Resource)
	})

	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := githubapitest.NewTestHandler(t)
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			client := octoql.NewClient(server.URL, http.DefaultClient)
			variables := githubapitest.GetNodeVariables{Id: http.StatusText(status)}
			handler.ExpectGetNode(variables).RespondError(
				octoql.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
				githubapitest.WithSecondaryRateLimit(30*time.Second),
				githubapitest.WithStatus(status),
			)

			response, err := githubapi.GetNode(t.Context(), client, variables.Id)
			require.NotNil(t, response)
			rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
			require.True(t, ok)
			assert.Equal(t, octoql.RateLimitSecondary, rateLimitError.Kind)
			assert.Equal(t, 30*time.Second, response.HTTP.RateLimit.RetryAfter)
			assert.Equal(t, status, response.HTTP.StatusCode)
		})
	}
}

func TestGeneratedHandlerDynamicAndAbstractResponses(t *testing.T) {
	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := octoql.NewClient(server.URL, http.DefaultClient)

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
		_, err := writer.Write([]byte(`{"data":{"echoProperty":"handled"}}`))
		assert.NoError(t, err)
	})

	dynamicResponse, err := githubapi.EchoProperty(
		t.Context(),
		client,
		dynamicVariables.Value,
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, dynamicResponse.HTTP.StatusCode)
	assert.JSONEq(t, `"handled"`, string(dynamicResponse.Data.EchoProperty))
	assert.JSONEq(t, string(dynamicVariables.Value), string((<-receivedVariables).Value))

	repositoryVariables := githubapitest.GetNodeVariables{Id: "repository"}
	handler.ExpectGetNode(repositoryVariables).Respond(githubapitest.GetNodeResponse{
		Node: &githubapitest.GetNodeNodeRepository{
			Id:            "R1",
			NameWithOwner: "octo-org/octo-repo",
		},
	})
	repositoryResponse, err := githubapi.GetNode(t.Context(), client, repositoryVariables.Id)
	require.NoError(t, err)
	repository, ok := repositoryResponse.Data.Node.(*githubapitest.GetNodeNodeRepository)
	require.True(t, ok)
	assert.Equal(t, "Repository", repository.Typename)

	otherVariables := githubapitest.GetNodeVariables{Id: "user"}
	handler.ExpectGetNode(otherVariables).Respond(githubapitest.GetNodeResponse{
		Node: &githubapitest.GetNodeNodeOctoqlOther{
			Typename: "User",
			Id:       "U1",
		},
	})
	otherResponse, err := githubapi.GetNode(t.Context(), client, otherVariables.Id)
	require.NoError(t, err)
	other, ok := otherResponse.Data.Node.(*githubapitest.GetNodeNodeOctoqlOther)
	require.True(t, ok)
	assert.Equal(t, "User", other.Typename)

	searchVariables := githubapitest.SearchVariables{Query: "octo"}
	handler.ExpectSearch(searchVariables).Respond(githubapitest.SearchResponse{
		Search: []githubapitest.SearchSearchSearchResultItem{
			&githubapitest.SearchSearchRepository{
				Id:            "R1",
				NameWithOwner: "octo-org/octo-repo",
			},
			&githubapitest.SearchSearchIssue{
				Id:    "I1",
				Title: "bug",
			},
			&githubapitest.SearchSearchSearchResultItemOctoqlOther{
				Typename: "User",
			},
		},
	})
	searchResponse, err := githubapi.Search(t.Context(), client, searchVariables.Query)
	require.NoError(t, err)
	require.Len(t, searchResponse.Data.Search, 3)
	_, ok = searchResponse.Data.Search[0].(*githubapitest.SearchSearchRepository)
	assert.True(t, ok)
	_, ok = searchResponse.Data.Search[1].(*githubapitest.SearchSearchIssue)
	assert.True(t, ok)
	searchOther, ok := searchResponse.Data.Search[2].(*githubapitest.SearchSearchSearchResultItemOctoqlOther)
	require.True(t, ok)
	assert.Equal(t, "User", searchOther.Typename)
}

func TestGeneratedHandlerConcurrentRequests(t *testing.T) {
	const requestCount = 32

	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := octoql.NewClient(server.URL, http.DefaultClient)
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
			_, err := githubapi.EchoProperty(t.Context(), client, variables.Value)
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
		After: "cursor-1",
	}
}

func repositoryData() githubapitest.GetRepositoryResponse {
	return githubapitest.GetRepositoryResponse{
		Repository: githubapitest.GetRepositoryRepository{
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
					EndCursor:   "cursor-2",
				},
			},
		},
	}
}
