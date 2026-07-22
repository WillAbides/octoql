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
	githubapi "github.com/willabides/octoql/internal/handlertest/client"
	clienttypes "github.com/willabides/octoql/internal/handlertest/githubapitest"
	localtypes "github.com/willabides/octoql/internal/handlertest/localfixture/githubapitest"
)

type wireParityCase struct {
	configureClient func(*clienttypes.TestHandler)
	configureLocal  func(*localtypes.TestHandler)
	variables       any
	headers         []responseHeaderExpectation
	name            string
	operation       string
}

type responseHeaderExpectation struct {
	name  string
	value string
}

func TestHandlerTypeStrategiesWireParity(t *testing.T) {
	// These internal fixtures deliberately export generated client and handler
	// types so this external-package test can compile both type strategies.
	updatedAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	tests := []wireParityCase{
		{
			name:      "repository query aliases lists pagination scalars and response headers",
			operation: "GetRepository",
			variables: map[string]any{
				"owner": "octo-org",
				"name":  "octo-repo",
				"first": 2,
				"after": "cursor-1",
			},
			headers: []responseHeaderExpectation{
				{
					name:  "X-GitHub-Request-ID",
					value: "request-1",
				},
				{
					name:  "X-Handler-Mode",
					value: "client",
				},
			},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetRepository(clienttypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "octo-repo",
					First: 2,
					After: ptr("cursor-1"),
				}).Respond(
					clientRepositoryResponse(updatedAt),
					clienttypes.WithStatus(http.StatusAccepted),
					clienttypes.WithHeader("x-github-request-id", "request-1"),
					clienttypes.WithHeaders(http.Header{
						"x-handler-mode": {"client"},
					}),
					clienttypes.WithPrimaryRateLimit(clienttypes.RateLimit{
						Limit:     5000,
						Remaining: 4999,
						Used:      1,
						Reset:     updatedAt,
						Resource:  "graphql",
					}),
				)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetRepository(localtypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "octo-repo",
					First: 2,
					After: ptr("cursor-1"),
				}).Respond(
					localRepositoryResponse(updatedAt),
					localtypes.WithStatus(http.StatusAccepted),
					localtypes.WithHeader("x-github-request-id", "request-1"),
					localtypes.WithHeaders(http.Header{
						"x-handler-mode": {"client"},
					}),
					localtypes.WithPrimaryRateLimit(localtypes.RateLimit{
						Limit:     5000,
						Remaining: 4999,
						Used:      1,
						Reset:     updatedAt,
						Resource:  "graphql",
					}),
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
						OwnerId:          ptr("O1"),
						Visibility:       ptr(clienttypes.RepositoryVisibilityPrivate),
						ClientMutationId: ptr("mutation-1"),
					},
				}).Respond(clienttypes.CreateRepositoryResponse{
					CreateRepository: clienttypes.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
						Repository: ptr(clienttypes.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
							Id:            "R2",
							NameWithOwner: "octo-org/created",
							UpdatedAt:     updatedAt,
						}),
						ClientMutationId: ptr("mutation-1"),
					},
				})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectCreateRepository(localtypes.CreateRepositoryVariables{
					Input: localtypes.CreateRepositoryInput{
						Name:             "created",
						OwnerId:          ptr("O1"),
						Visibility:       ptr(localtypes.RepositoryVisibilityPrivate),
						ClientMutationId: ptr("mutation-1"),
					},
				}).Respond(localtypes.CreateRepositoryResponse{
					CreateRepository: localtypes.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
						Repository: ptr(localtypes.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
							Id:            "R2",
							NameWithOwner: "octo-org/created",
							UpdatedAt:     updatedAt,
						}),
						ClientMutationId: ptr("mutation-1"),
					},
				})
			},
		},
		{
			name:      "node catch all actual typename",
			operation: "GetNode",
			variables: map[string]any{"id": "user"},
			configureClient: func(handler *clienttypes.TestHandler) {
				node := &clienttypes.GetNodeNodeOctoqlOther{
					Typename: "User",
					Id:       "U1",
				}
				handler.ExpectGetNode(clienttypes.GetNodeVariables{Id: "user"}).
					Respond(clienttypes.GetNodeResponse{
						Node: node,
					})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				node := &localtypes.GetNodeNodeOctoqlOther{
					Typename: "User",
					Id:       "U1",
				}
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "user"}).
					Respond(localtypes.GetNodeResponse{
						Node: node,
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
					After: ptr(""),
				}).RespondDataAndErrors(
					clienttypes.GetRepositoryResponse{
						Repository: ptr(clienttypes.GetRepositoryRepository{
							Id:       "R1",
							FullName: "octo-org/partial",
						}),
					},
					clienttypes.Error{
						Type:       "FORBIDDEN",
						Message:    "field hidden",
						Path:       clienttypes.Path{"repository", "propertyValue"},
						Extensions: map[string]any{"code": "hidden"},
					},
				)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetRepository(localtypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "partial",
					First: 1,
					After: ptr(""),
				}).RespondDataAndErrors(
					localtypes.GetRepositoryResponse{
						Repository: ptr(localtypes.GetRepositoryRepository{
							Id:       "R1",
							FullName: "octo-org/partial",
						}),
					},
					localtypes.Error{
						Type:       "FORBIDDEN",
						Message:    "field hidden",
						Path:       localtypes.Path{"repository", "propertyValue"},
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
						clienttypes.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
						clienttypes.WithSecondaryRateLimit(30*time.Second),
					)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "secondary-200"}).
					RespondError(
						localtypes.Error{Type: "ABUSE_DETECTED", Message: "slow down"},
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
			for _, header := range test.headers {
				assert.Equal(t, header.value, clientResponse.Header().Get(header.name))
				assert.Equal(t, header.value, localResponse.Header().Get(header.name))
			}
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
	client := githubapi.NewClient(server.URL, server.Client())
	updatedAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)

	variables := localtypes.GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
		First: 2,
		After: ptr("cursor-1"),
	}
	handler.ExpectGetRepository(variables).Respond(localRepositoryResponse(updatedAt))
	response, err := client.GetRepository(
		t.Context(),
		githubapi.GetRepositoryVariables{
			Owner: variables.Owner,
			Name:  variables.Name,
			First: variables.First,
			After: variables.After,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, response.Repository)
	assert.Equal(t, "octo-org/octo-repo", response.Repository.FullName)
	assert.Equal(t, updatedAt, response.Repository.UpdatedAt)
	assert.JSONEq(t, `["red","blue"]`, string(response.Repository.PropertyValue))
	require.Len(t, response.Repository.Issues.Nodes, 1)
	assert.Equal(t, "I1", response.Repository.Issues.Nodes[0].Id)
	assert.Equal(t, "bug", response.Repository.Issues.Nodes[0].Title)
	assert.Equal(t, "cursor-2", requirePtrValue(t, response.Repository.Issues.PageInfo.EndCursor))
	requireGeneratedRequest(
		t,
		requests,
		"GetRepository",
		githubapi.GetRepository_Operation,
		`{"owner":"octo-org","name":"octo-repo","first":2,"after":"cursor-1"}`,
	)

	nodeVariables := localtypes.GetNodeVariables{Id: "user"}
	localNode := &localtypes.GetNodeNodeOctoqlOther{
		Typename: "User",
		Id:       "U1",
	}
	handler.ExpectGetNode(nodeVariables).Respond(localtypes.GetNodeResponse{
		Node: localNode,
	})
	nodeResponse, err := client.GetNode(t.Context(), githubapi.GetNodeVariables{
		Id: nodeVariables.Id,
	})
	require.NoError(t, err)
	require.NotNil(t, nodeResponse.Node)
	other, ok := nodeResponse.Node.(*githubapi.GetNodeNodeOctoqlOther)
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
	searchResponse, err := client.Search(t.Context(), githubapi.SearchVariables{
		Query: searchVariables.Query,
	})

	require.NoError(t, err)
	require.Len(t, searchResponse.Search, 3)
	searchRepository, ok := searchResponse.Search[0].(*githubapi.SearchSearchRepository)
	require.True(t, ok)
	assert.Equal(t, "octo-org/octo-repo", searchRepository.NameWithOwner)
	searchOther, ok := searchResponse.Search[2].(*githubapi.SearchSearchSearchResultItemOctoqlOther)
	require.True(t, ok)
	assert.Equal(t, "User", searchOther.Typename)
	requireGeneratedRequest(t, requests, "Search", githubapi.Search_Operation, `{"query":"octo"}`)

	property := json.RawMessage(`["one","two"]`)
	handler.ExpectEchoProperty(localtypes.EchoPropertyVariables{Value: property}).
		Respond(localtypes.EchoPropertyResponse{EchoProperty: property})
	propertyResponse, err := client.EchoProperty(t.Context(), githubapi.EchoPropertyVariables{
		Value: property,
	})
	require.NoError(t, err)
	assert.JSONEq(t, string(property), string(propertyResponse.EchoProperty))
	requireGeneratedRequest(
		t,
		requests,
		"EchoProperty",
		githubapi.EchoProperty_Operation,
		`{"value":["one","two"]}`,
	)

	handler.ExpectEchoAt(localtypes.EchoAtVariables{Value: updatedAt}).
		Respond(localtypes.EchoAtResponse{EchoAt: updatedAt})
	temporalResponse, err := client.EchoAt(t.Context(), githubapi.EchoAtVariables{
		Value: updatedAt,
	})
	require.NoError(t, err)
	assert.Equal(t, updatedAt, temporalResponse.EchoAt)
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
	arbitraryResponse, err := client.EchoAny(t.Context(), githubapi.EchoAnyVariables{
		Value: largeInteger,
	})
	require.NoError(t, err)
	arbitrary, ok := arbitraryResponse.EchoAny.(map[string]any)
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
	handler.ExpectGetNode(errorVariables).RespondError(localtypes.Error{
		Type:       "NOT_FOUND",
		Message:    "missing",
		Extensions: map[string]any{"code": "missing"},
	})
	errorResponse, err := client.GetNode(t.Context(), githubapi.GetNodeVariables{
		Id: errorVariables.Id,
	})
	assert.Nil(t, errorResponse)
	graphqlErrors, ok := errors.AsType[githubapi.Errors](err)
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
		Repository: ptr(clienttypes.GetRepositoryRepository{
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
					EndCursor:   ptr("cursor-2"),
				},
			},
		}),
	}
}

func localRepositoryResponse(updatedAt time.Time) localtypes.GetRepositoryResponse {
	return localtypes.GetRepositoryResponse{
		Repository: ptr(localtypes.GetRepositoryRepository{
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
					EndCursor:   ptr("cursor-2"),
				},
			},
		}),
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
