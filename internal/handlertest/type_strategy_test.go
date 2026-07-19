package handlertest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
	githubapi "github.com/willabides/octoql/internal/handlertest/client"
	clienttypes "github.com/willabides/octoql/internal/handlertest/githubapitest"
	localtypes "github.com/willabides/octoql/internal/handlertest/localfixture/githubapitest"
)

type wireParityCase struct {
	configureClient func(*clienttypes.TestHandler)
	configureLocal  func(*localtypes.TestHandler)
	variables       any
	name            string
	operation       string
}

func TestHandlerTypeStrategiesWireParity(t *testing.T) {
	// These internal fixtures deliberately export generated client and handler
	// types so this external-package test can compile both type strategies.
	updatedAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	tests := []wireParityCase{
		{
			name:      "repository query aliases lists pagination scalars and response metadata",
			operation: "GetRepository",
			variables: map[string]any{
				"owner": "octo-org",
				"name":  "octo-repo",
				"first": 2,
				"after": "cursor-1",
			},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetRepository(clienttypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "octo-repo",
					First: 2,
					After: "cursor-1",
				}).Respond(
					clientRepositoryResponse(updatedAt),
					clienttypes.WithStatus(http.StatusAccepted),
					clienttypes.WithHeader("X-GitHub-Request-ID", "request-1"),
					clienttypes.WithPrimaryRateLimit(octoql.RateLimit{
						Limit:     5000,
						Remaining: 4999,
						Used:      1,
						Reset:     updatedAt,
						Resource:  "graphql",
					}),
					clienttypes.WithExtensions(map[string]any{"trace": "client-local"}),
				)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetRepository(localtypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "octo-repo",
					First: 2,
					After: "cursor-1",
				}).Respond(
					localRepositoryResponse(updatedAt),
					localtypes.WithStatus(http.StatusAccepted),
					localtypes.WithHeader("X-GitHub-Request-ID", "request-1"),
					localtypes.WithPrimaryRateLimit(octoql.RateLimit{
						Limit:     5000,
						Remaining: 4999,
						Used:      1,
						Reset:     updatedAt,
						Resource:  "graphql",
					}),
					localtypes.WithExtensions(map[string]any{"trace": "client-local"}),
				)
			},
		},
		{
			name:      "mutation input and enum",
			operation: "CreateRepository",
			variables: map[string]any{
				"input": map[string]any{
					"name":             "created",
					"ownerId":          "O1",
					"visibility":       "PRIVATE",
					"clientMutationId": "mutation-1",
				},
			},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectCreateRepository(clienttypes.CreateRepositoryVariables{
					Input: clienttypes.CreateRepositoryInput{
						Name:             "created",
						OwnerId:          "O1",
						Visibility:       clienttypes.RepositoryVisibilityPrivate,
						ClientMutationId: "mutation-1",
					},
				}).Respond(clienttypes.CreateRepositoryResponse{
					CreateRepository: clienttypes.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
						Repository: clienttypes.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
							Id:            "R2",
							NameWithOwner: "octo-org/created",
							UpdatedAt:     updatedAt,
						},
						ClientMutationId: "mutation-1",
					},
				})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectCreateRepository(localtypes.CreateRepositoryVariables{
					Input: localtypes.CreateRepositoryInput{
						Name:             "created",
						OwnerId:          "O1",
						Visibility:       localtypes.RepositoryVisibilityPrivate,
						ClientMutationId: "mutation-1",
					},
				}).Respond(localtypes.CreateRepositoryResponse{
					CreateRepository: localtypes.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
						Repository: localtypes.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
							Id:            "R2",
							NameWithOwner: "octo-org/created",
							UpdatedAt:     updatedAt,
						},
						ClientMutationId: "mutation-1",
					},
				})
			},
		},
		{
			name:      "node catch all actual typename",
			operation: "GetNode",
			variables: map[string]any{"id": "user"},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetNode(clienttypes.GetNodeVariables{Id: "user"}).
					Respond(clienttypes.GetNodeResponse{
						Node: &clienttypes.GetNodeNodeOctoqlOther{
							Typename: "User",
							Id:       "U1",
						},
					})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "user"}).
					Respond(localtypes.GetNodeResponse{
						Node: &localtypes.GetNodeNodeOctoqlOther{
							Typename: "User",
							Id:       "U1",
						},
					})
			},
		},
		{
			name:      "search union list and catch all",
			operation: "Search",
			variables: map[string]any{"query": "octo"},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectSearch(clienttypes.SearchVariables{Query: "octo"}).
					Respond(clienttypes.SearchResponse{
						Search: []clienttypes.SearchSearchSearchResultItem{
							&clienttypes.SearchSearchRepository{
								Id:            "R1",
								NameWithOwner: "octo-org/octo-repo",
							},
							&clienttypes.SearchSearchIssue{Id: "I1", Title: "bug"},
							&clienttypes.SearchSearchSearchResultItemOctoqlOther{
								Typename: "User",
							},
						},
					})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectSearch(localtypes.SearchVariables{Query: "octo"}).
					Respond(localtypes.SearchResponse{
						Search: []localtypes.SearchSearchSearchResultItem{
							&localtypes.SearchSearchRepository{
								Id:            "R1",
								NameWithOwner: "octo-org/octo-repo",
							},
							&localtypes.SearchSearchIssue{Id: "I1", Title: "bug"},
							&localtypes.SearchSearchSearchResultItemOctoqlOther{
								Typename: "User",
							},
						},
					})
			},
		},
		{
			name:      "custom property value array",
			operation: "EchoProperty",
			variables: map[string]any{"value": []any{"one", "two"}},
			configureClient: func(handler *clienttypes.TestHandler) {
				value := json.RawMessage(`["one","two"]`)
				handler.ExpectEchoProperty(clienttypes.EchoPropertyVariables{Value: value}).
					Respond(clienttypes.EchoPropertyResponse{EchoProperty: value})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				value := json.RawMessage(`["one","two"]`)
				handler.ExpectEchoProperty(localtypes.EchoPropertyVariables{Value: value}).
					Respond(localtypes.EchoPropertyResponse{EchoProperty: value})
			},
		},
		{
			name:      "date time binding",
			operation: "EchoAt",
			variables: map[string]any{"value": updatedAt},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectEchoAt(clienttypes.EchoAtVariables{Value: updatedAt}).
					Respond(clienttypes.EchoAtResponse{EchoAt: updatedAt})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectEchoAt(localtypes.EchoAtVariables{Value: updatedAt}).
					Respond(localtypes.EchoAtResponse{EchoAt: updatedAt})
			},
		},
		{
			name:      "partial data graphql error and per error extensions",
			operation: "GetRepository",
			variables: map[string]any{
				"owner": "octo-org",
				"name":  "partial",
				"first": 1,
				"after": "",
			},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetRepository(clienttypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "partial",
					First: 1,
				}).RespondDataAndErrors(
					clienttypes.GetRepositoryResponse{
						Repository: clienttypes.GetRepositoryRepository{
							Id:       "R1",
							FullName: "octo-org/partial",
						},
					},
					octoql.Error{
						Type:       "FORBIDDEN",
						Message:    "field hidden",
						Path:       octoql.Path{"repository", "propertyValue"},
						Extensions: map[string]any{"code": "hidden"},
					},
				)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetRepository(localtypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "partial",
					First: 1,
				}).RespondDataAndErrors(
					localtypes.GetRepositoryResponse{
						Repository: localtypes.GetRepositoryRepository{
							Id:       "R1",
							FullName: "octo-org/partial",
						},
					},
					octoql.Error{
						Type:       "FORBIDDEN",
						Message:    "field hidden",
						Path:       octoql.Path{"repository", "propertyValue"},
						Extensions: map[string]any{"code": "hidden"},
					},
				)
			},
		},
		{
			name:      "secondary rate limit http 200",
			operation: "GetNode",
			variables: map[string]any{"id": "secondary-200"},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetNode(clienttypes.GetNodeVariables{Id: "secondary-200"}).
					RespondError(
						octoql.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
						clienttypes.WithSecondaryRateLimit(30*time.Second),
					)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "secondary-200"}).
					RespondError(
						octoql.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
						localtypes.WithSecondaryRateLimit(30*time.Second),
					)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientHandler := clienttypes.NewTestHandler(t)
			localHandler := localtypes.NewTestHandler(t)
			test.configureClient(clientHandler)
			test.configureLocal(localHandler)

			clientResponse := postGraphQL(t, clientHandler, test.operation, test.variables)
			localResponse := postGraphQL(t, localHandler, test.operation, test.variables)

			assert.Equal(t, clientResponse.Code, localResponse.Code)
			assert.Equal(t, clientResponse.Header(), localResponse.Header())
			assert.JSONEq(t, clientResponse.Body.String(), localResponse.Body.String())
		})
	}
}

func TestLocalHandlerClientDecoding(t *testing.T) {
	handler := localtypes.NewTestHandler(t)
	requests := make(chan recordedGraphQLRequest, 16)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		request.Body = io.NopCloser(bytes.NewReader(body))
		var recorded recordedGraphQLRequest
		err = json.Unmarshal(body, &recorded)
		require.NoError(t, err)
		requests <- recorded
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	client := octoql.NewClient(server.URL, server.Client())
	updatedAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)

	variables := localtypes.GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
		First: 2,
		After: "cursor-1",
	}
	handler.ExpectGetRepository(variables).Respond(localRepositoryResponse(updatedAt))
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
	assert.Equal(t, updatedAt, response.Data.Repository.UpdatedAt)
	assert.JSONEq(t, `["red","blue"]`, string(response.Data.Repository.PropertyValue))
	require.Len(t, response.Data.Repository.Issues.Nodes, 1)
	assert.Equal(t, "I1", response.Data.Repository.Issues.Nodes[0].Id)
	assert.Equal(t, "bug", response.Data.Repository.Issues.Nodes[0].Title)
	assert.Equal(t, "cursor-2", response.Data.Repository.Issues.PageInfo.EndCursor)
	requireGeneratedRequest(
		t,
		requests,
		"GetRepository",
		githubapi.GetRepository_Operation,
		`{"owner":"octo-org","name":"octo-repo","first":2,"after":"cursor-1"}`,
	)

	nodeVariables := localtypes.GetNodeVariables{Id: "user"}
	handler.ExpectGetNode(nodeVariables).Respond(localtypes.GetNodeResponse{
		Node: &localtypes.GetNodeNodeOctoqlOther{
			Typename: "User",
			Id:       "U1",
		},
	})
	nodeResponse, err := githubapi.GetNode(t.Context(), client, nodeVariables.Id)
	require.NoError(t, err)
	other, ok := nodeResponse.Data.Node.(*githubapi.GetNodeNodeOctoqlOther)
	require.True(t, ok)
	assert.Equal(t, "User", other.Typename)
	assert.Equal(t, "U1", other.Id)
	requireGeneratedRequest(t, requests, "GetNode", githubapi.GetNode_Operation, `{"id":"user"}`)

	searchVariables := localtypes.SearchVariables{Query: "octo"}
	handler.ExpectSearch(searchVariables).Respond(localtypes.SearchResponse{
		Search: []localtypes.SearchSearchSearchResultItem{
			&localtypes.SearchSearchRepository{
				Id:            "R1",
				NameWithOwner: "octo-org/octo-repo",
			},
			&localtypes.SearchSearchIssue{Id: "I1", Title: "bug"},
			&localtypes.SearchSearchSearchResultItemOctoqlOther{Typename: "User"},
		},
	})
	searchResponse, err := githubapi.Search(t.Context(), client, searchVariables.Query)
	require.NoError(t, err)
	require.Len(t, searchResponse.Data.Search, 3)
	searchRepository, ok := searchResponse.Data.Search[0].(*githubapi.SearchSearchRepository)
	require.True(t, ok)
	assert.Equal(t, "octo-org/octo-repo", searchRepository.NameWithOwner)
	searchOther, ok := searchResponse.Data.Search[2].(*githubapi.SearchSearchSearchResultItemOctoqlOther)
	require.True(t, ok)
	assert.Equal(t, "User", searchOther.Typename)
	requireGeneratedRequest(t, requests, "Search", githubapi.Search_Operation, `{"query":"octo"}`)

	property := json.RawMessage(`["one","two"]`)
	handler.ExpectEchoProperty(localtypes.EchoPropertyVariables{Value: property}).
		Respond(localtypes.EchoPropertyResponse{EchoProperty: property})
	propertyResponse, err := githubapi.EchoProperty(t.Context(), client, property)
	require.NoError(t, err)
	assert.JSONEq(t, string(property), string(propertyResponse.Data.EchoProperty))
	requireGeneratedRequest(
		t,
		requests,
		"EchoProperty",
		githubapi.EchoProperty_Operation,
		`{"value":["one","two"]}`,
	)

	handler.ExpectEchoAt(localtypes.EchoAtVariables{Value: updatedAt}).
		Respond(localtypes.EchoAtResponse{EchoAt: updatedAt})
	temporalResponse, err := githubapi.EchoAt(t.Context(), client, updatedAt)
	require.NoError(t, err)
	assert.Equal(t, updatedAt, temporalResponse.Data.EchoAt)
	requireGeneratedRequest(
		t,
		requests,
		"EchoAt",
		githubapi.EchoAt_Operation,
		`{"value":"2026-07-19T12:00:00Z"}`,
	)

	const largeInteger = int64(9_007_199_254_740_993)
	handler.ExpectEchoAny(localtypes.EchoAnyVariables{Value: largeInteger}).
		Respond(localtypes.EchoAnyResponse{EchoAny: map[string]any{
			"count": 42,
			"items": []any{"one", true},
		}})
	arbitraryResponse, err := githubapi.EchoAny(t.Context(), client, largeInteger)
	require.NoError(t, err)
	arbitrary, ok := arbitraryResponse.Data.EchoAny.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(42), arbitrary["count"])
	assert.Equal(t, []any{"one", true}, arbitrary["items"])
	requireGeneratedRequest(
		t,
		requests,
		"EchoAny",
		githubapi.EchoAny_Operation,
		`{"value":9007199254740993}`,
	)

	errorVariables := localtypes.GetNodeVariables{Id: "missing"}
	handler.ExpectGetNode(errorVariables).RespondError(octoql.Error{
		Type:       "NOT_FOUND",
		Message:    "missing",
		Extensions: map[string]any{"code": "missing"},
	})
	errorResponse, err := githubapi.GetNode(t.Context(), client, errorVariables.Id)
	require.NotNil(t, errorResponse)
	graphqlErrors, ok := errors.AsType[octoql.Errors](err)
	require.True(t, ok)
	assert.Equal(t, "missing", graphqlErrors[0].Extensions["code"])
	requireGeneratedRequest(t, requests, "GetNode", githubapi.GetNode_Operation, `{"id":"missing"}`)
}

func TestLocalAndClientHandlerTypesAreDistinct(t *testing.T) {
	clientType := reflect.TypeFor[clienttypes.GetRepositoryResponse]()
	localType := reflect.TypeFor[localtypes.GetRepositoryResponse]()

	assert.NotEqual(t, clientType, localType)
	assert.NotEqual(t, clientType.PkgPath(), localType.PkgPath())
}

func clientRepositoryResponse(updatedAt time.Time) clienttypes.GetRepositoryResponse {
	return clienttypes.GetRepositoryResponse{
		Repository: clienttypes.GetRepositoryRepository{
			Id:            "R1",
			FullName:      "octo-org/octo-repo",
			UpdatedAt:     updatedAt,
			PropertyValue: json.RawMessage(`["red","blue"]`),
			Issues: clienttypes.GetRepositoryRepositoryIssuesIssueConnection{
				Nodes: []clienttypes.GetRepositoryRepositoryIssuesIssueConnectionNodesIssue{{
					Id:    "I1",
					Title: "bug",
				}},
				PageInfo: clienttypes.GetRepositoryRepositoryIssuesIssueConnectionPageInfo{
					HasNextPage: true,
					EndCursor:   "cursor-2",
				},
			},
		},
	}
}

func localRepositoryResponse(updatedAt time.Time) localtypes.GetRepositoryResponse {
	return localtypes.GetRepositoryResponse{
		Repository: localtypes.GetRepositoryRepository{
			Id:            "R1",
			FullName:      "octo-org/octo-repo",
			UpdatedAt:     updatedAt,
			PropertyValue: json.RawMessage(`["red","blue"]`),
			Issues: localtypes.GetRepositoryRepositoryIssuesIssueConnection{
				Nodes: []localtypes.GetRepositoryRepositoryIssuesIssueConnectionNodesIssue{{
					Id:    "I1",
					Title: "bug",
				}},
				PageInfo: localtypes.GetRepositoryRepositoryIssuesIssueConnectionPageInfo{
					HasNextPage: true,
					EndCursor:   "cursor-2",
				},
			},
		},
	}
}

func postGraphQL(
	t *testing.T,
	handler http.Handler,
	operation string,
	variables any,
) *httptest.ResponseRecorder {
	requestBody, err := json.Marshal(map[string]any{
		"operationName": operation,
		"query":         canonicalOperationDocument(t, operation),
		"variables":     variables,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(
		http.MethodPost,
		"https://api.github.example/graphql",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.NotEmpty(t, response.Body.String())
	return response
}

type recordedGraphQLRequest struct {
	OperationName string          `json:"operationName"`
	Query         string          `json:"query"`
	Variables     json.RawMessage `json:"variables"`
}

func requireGeneratedRequest(
	t *testing.T,
	requests <-chan recordedGraphQLRequest,
	operationName string,
	expectedQuery string,
	variables string,
) {
	t.Helper()
	recorded := <-requests
	assert.Equal(t, operationName, recorded.OperationName)
	assert.Equal(t, expectedQuery, recorded.Query)
	if variables == "" {
		assert.Empty(t, recorded.Variables)
		return
	}
	assert.JSONEq(t, variables, string(recorded.Variables))
}

func canonicalOperationDocument(t *testing.T, operation string) string {
	t.Helper()
	switch operation {
	case "CreateRepository":
		return githubapi.CreateRepository_Operation
	case "EchoAny":
		return githubapi.EchoAny_Operation
	case "EchoAt":
		return githubapi.EchoAt_Operation
	case "EchoProperty":
		return githubapi.EchoProperty_Operation
	case "GetNode":
		return githubapi.GetNode_Operation
	case "GetRepository":
		return githubapi.GetRepository_Operation
	case "Search":
		return githubapi.Search_Operation
	case "Viewer":
		return githubapi.Viewer_Operation
	default:
		t.Fatalf("unknown operation %q", operation)
		return ""
	}
}
