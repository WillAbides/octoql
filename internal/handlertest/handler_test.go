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

func TestGeneratedHandlerSuccessMutationAndScalars(t *testing.T) {
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

	variables := repositoryVariables()
	data := repositoryData()
	handler.ExpectGetRepository(variables).Respond(data)

	response, err := client.GetRepository(
		t.Context(),

		variables)

	require.NoError(t, err)
	require.NotNil(t, response.Repository)
	assert.Equal(t, "octo-org/octo-repo", response.Repository.FullName)
	assert.Equal(t, time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC), response.Repository.UpdatedAt)
	assert.JSONEq(t, `["red","blue"]`, string(response.Repository.PropertyValue))
	require.Len(t, response.Repository.Issues.Nodes, 1)
	assert.Equal(t, "bug", response.Repository.Issues.Nodes[0].Title)
	assert.Equal(t, "cursor-2", requirePtrValue(t, response.Repository.Issues.PageInfo.EndCursor))

	input := githubapitest.CreateRepositoryInput{
		Name:             "created",
		OwnerId:          ptr("O1"),
		Visibility:       ptr(githubapitest.RepositoryVisibilityPrivate),
		ClientMutationId: ptr("mutation-1"),
	}
	mutationData := githubapitest.CreateRepositoryResponse{
		CreateRepository: githubapitest.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
			Repository: ptr(githubapitest.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
				Id:            "R2",
				NameWithOwner: "octo-org/created",
				UpdatedAt:     time.Date(2026, time.July, 19, 13, 0, 0, 0, time.UTC),
			}),
			ClientMutationId: ptr("mutation-1"),
		},
	}
	handler.ExpectCreateRepository(githubapitest.CreateRepositoryVariables{
		Input: input,
	}).Respond(mutationData)

	mutationResponse, err := client.CreateRepository(t.Context(), githubapitest.CreateRepositoryVariables{
		Input: input,
	})

	require.NoError(t, err)
	require.NotNil(t, mutationResponse.CreateRepository.Repository)
	assert.Equal(t, "octo-org/created", mutationResponse.CreateRepository.Repository.NameWithOwner)
	assert.Equal(t, "mutation-1", requirePtrValue(t, mutationResponse.CreateRepository.ClientMutationId))

	property := json.RawMessage(`["one","two"]`)
	handler.ExpectEchoProperty(githubapitest.EchoPropertyVariables{
		Value: property,
	}).Respond(githubapitest.EchoPropertyResponse{
		EchoProperty: property,
	})

	propertyResponse, err := client.EchoProperty(t.Context(), githubapitest.EchoPropertyVariables{
		Value: property,
	})

	require.NoError(t, err)
	assert.JSONEq(t, string(property), string(propertyResponse.EchoProperty))

	temporalValue := time.Now()
	handler.ExpectEchoAt(githubapitest.EchoAtVariables{
		Value: temporalValue,
	}).Respond(githubapitest.EchoAtResponse{
		EchoAt: temporalValue,
	})
	temporalResponse, err := client.EchoAt(t.Context(), githubapitest.EchoAtVariables{
		Value: temporalValue,
	})

	require.NoError(t, err)
	assert.True(t, temporalValue.Equal(temporalResponse.EchoAt))

	const largeInteger = int64(9_007_199_254_740_993)
	handler.ExpectEchoAny(githubapitest.EchoAnyVariables{
		Value: largeInteger,
	}).Respond(githubapitest.EchoAnyResponse{
		EchoAny: largeInteger,
	})
	_, err = client.EchoAny(t.Context(), githubapitest.EchoAnyVariables{
		Value: largeInteger,
	})

	require.NoError(t, err)
}

func TestGeneratedHandlerGraphQLErrorsAndPartialData(t *testing.T) {
	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := githubapi.NewClient(server.URL, http.DefaultClient)

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

	response, err := client.GetNode(t.Context(), errorVariables)
	assert.Nil(t, response)
	graphqlErrors, ok := errors.AsType[githubapi.Errors](err)
	require.True(t, ok)
	require.Len(t, graphqlErrors, 1)
	assert.Equal(t, githubapi.ErrorType("NOT_FOUND"), graphqlErrors[0].Type)
	assert.Equal(t, githubapi.Path{"node"}, graphqlErrors[0].Path)
	assert.Equal(t, "missing", graphqlErrors[0].Extensions["code"])
	require.Len(t, graphqlErrors[0].Locations, 1)
	assert.Equal(t, 2, graphqlErrors[0].Locations[0].Line)

	variables := repositoryVariables()
	data := repositoryData()
	handler.ExpectGetRepository(variables).RespondDataAndErrors(
		data,
		githubapi.Error{
			Type:    "FORBIDDEN",
			Message: "one field is unavailable",
			Path:    githubapi.Path{"repository", "propertyValue"},
		},
	)

	partial, err := client.GetRepository(
		t.Context(),

		variables)

	require.Error(t, err)
	assert.Nil(t, partial)
	partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
	require.True(t, ok)
	require.NotNil(t, partialErr.PartialData())
	assert.Equal(t, "octo-org/octo-repo", partialErr.PartialData().Repository.FullName)
	_, wrongOperation := errors.AsType[*githubapi.GetNodePartialDataError](err)
	assert.False(t, wrongOperation)
}

func TestGeneratedHandlerResponseOptionsAndRateLimits(t *testing.T) {
	t.Run("successful custom status", func(t *testing.T) {
		handler := githubapitest.NewTestHandler(t)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client := githubapi.NewClient(server.URL, http.DefaultClient)
		variables := repositoryVariables()
		handler.ExpectGetRepository(variables).Respond(
			repositoryData(),
			githubapitest.WithStatus(http.StatusAccepted),
		)

		response, err := client.GetRepository(
			t.Context(),

			variables)

		require.NoError(t, err)
		assert.Equal(t, "octo-org/octo-repo", response.Repository.FullName)
	})

	t.Run("primary rate limit at http 200", func(t *testing.T) {
		handler := githubapitest.NewTestHandler(t)
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client := githubapi.NewClient(server.URL, http.DefaultClient)
		variables := githubapitest.GetNodeVariables{Id: "primary"}
		reset := time.Date(2026, time.July, 19, 13, 0, 0, 0, time.UTC)
		handler.ExpectGetNode(variables).RespondError(
			githubapi.Error{Type: "RATE_LIMITED", Message: "quota exhausted"},
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
		assert.Equal(t, 5000, rateLimitError.RateLimit.Limit)
		assert.Equal(t, reset, rateLimitError.RateLimit.Reset)
		assert.Equal(t, "graphql", rateLimitError.RateLimit.Resource)
		rateLimit, known := client.RateLimit()
		require.True(t, known)
		assert.Equal(t, rateLimitError.RateLimit, rateLimit)
	})

	for _, status := range []int{
		http.StatusOK,
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
			assert.Equal(t, 30*time.Second, rateLimitError.RateLimit.RetryAfter)
			responseError, ok := errors.AsType[*githubapi.ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, status, responseError.StatusCode)
		})
	}
}

func TestGeneratedHandlerDynamicAndAbstractResponses(t *testing.T) {
	handler := githubapitest.NewTestHandler(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := githubapi.NewClient(server.URL, http.DefaultClient)

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

	dynamicResponse, err := client.EchoProperty(
		t.Context(),

		dynamicVariables)

	require.NoError(t, err)
	assert.JSONEq(t, `"handled"`, string(dynamicResponse.EchoProperty))
	assert.JSONEq(t, string(dynamicVariables.Value), string((<-receivedVariables).Value))

	repositoryVariables := githubapitest.GetNodeVariables{Id: "repository"}
	repositoryNode := &githubapitest.GetNodeNodeRepository{
		Id:            "R1",
		NameWithOwner: "octo-org/octo-repo",
	}
	handler.ExpectGetNode(repositoryVariables).Respond(githubapitest.GetNodeResponse{
		Node: repositoryNode,
	})
	repositoryResponse, err := client.GetNode(t.Context(), repositoryVariables)
	require.NoError(t, err)
	require.NotNil(t, repositoryResponse.Node)
	repository, ok := repositoryResponse.Node.(*githubapitest.GetNodeNodeRepository)
	require.True(t, ok)
	assert.Equal(t, "Repository", repository.Typename)

	otherVariables := githubapitest.GetNodeVariables{Id: "user"}
	otherNode := &githubapitest.GetNodeNodeOctoqlOther{
		Typename: "User",
		Id:       "U1",
	}
	handler.ExpectGetNode(otherVariables).Respond(githubapitest.GetNodeResponse{
		Node: otherNode,
	})
	otherResponse, err := client.GetNode(t.Context(), otherVariables)
	require.NoError(t, err)
	require.NotNil(t, otherResponse.Node)
	other, ok := otherResponse.Node.(*githubapitest.GetNodeNodeOctoqlOther)
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
	searchResponse, err := client.Search(t.Context(), searchVariables)
	require.NoError(t, err)
	require.Len(t, searchResponse.Search, 3)
	_, ok = searchResponse.Search[0].(*githubapitest.SearchSearchRepository)
	assert.True(t, ok)
	_, ok = searchResponse.Search[1].(*githubapitest.SearchSearchIssue)
	assert.True(t, ok)
	searchOther, ok := searchResponse.Search[2].(*githubapitest.SearchSearchSearchResultItemOctoqlOther)
	require.True(t, ok)
	assert.Equal(t, "User", searchOther.Typename)
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
		After: ptr("cursor-1"),
	}
}

func repositoryData() githubapitest.GetRepositoryResponse {
	return githubapitest.GetRepositoryResponse{
		Repository: ptr(githubapitest.GetRepositoryRepository{
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
					EndCursor:   ptr("cursor-2"),
				},
			},
		}),
	}
}

func ptr[T any](value T) *T {
	return &value
}

func requirePtrValue[T any](t *testing.T, value *T) T {
	t.Helper()
	require.NotNil(t, value)
	return *value
}
