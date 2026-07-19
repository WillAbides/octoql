package handlertest_test

import (
	"bytes"
	"encoding/json"
	"errors"
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
	name            string
	operation       string
	variables       any
	configureClient func(*clienttypes.TestHandler)
	configureLocal  func(*localtypes.TestHandler)
}

func TestHandlerTypeStrategiesWireParity(t *testing.T) {
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
			name:      "node referenced variant",
			operation: "GetNode",
			variables: map[string]any{"id": "repository"},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetNode(clienttypes.GetNodeVariables{Id: "repository"}).
					Respond(clienttypes.GetNodeResponse{
						Node: &clienttypes.GetNodeNodeRepository{
							Id:            "R1",
							NameWithOwner: "octo-org/octo-repo",
						},
					})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "repository"}).
					Respond(localtypes.GetNodeResponse{
						Node: &localtypes.GetNodeNodeRepository{
							Id:            "R1",
							NameWithOwner: "octo-org/octo-repo",
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
			name:      "custom property value string",
			operation: "EchoProperty",
			variables: map[string]any{"value": "one"},
			configureClient: func(handler *clienttypes.TestHandler) {
				value := json.RawMessage(`"one"`)
				handler.ExpectEchoProperty(clienttypes.EchoPropertyVariables{Value: value}).
					Respond(clienttypes.EchoPropertyResponse{EchoProperty: value})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				value := json.RawMessage(`"one"`)
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
		{
			name:      "secondary rate limit http 403",
			operation: "GetNode",
			variables: map[string]any{"id": "secondary-403"},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetNode(clienttypes.GetNodeVariables{Id: "secondary-403"}).
					RespondError(
						octoql.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
						clienttypes.WithSecondaryRateLimit(30*time.Second),
						clienttypes.WithStatus(http.StatusForbidden),
					)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "secondary-403"}).
					RespondError(
						octoql.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
						localtypes.WithSecondaryRateLimit(30*time.Second),
						localtypes.WithStatus(http.StatusForbidden),
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
	server := httptest.NewServer(handler)
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
	assert.Equal(t, "cursor-2", response.Data.Repository.Issues.PageInfo.EndCursor)

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
