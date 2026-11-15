package handlertest_test

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	githubapi "github.com/willabides/octoql/internal/handlertest/client"
	clienttypes "github.com/willabides/octoql/internal/handlertest/githubapitest"
	localtypes "github.com/willabides/octoql/internal/handlertest/localfixture/githubapitest"
)

type wireParityCase struct {
	callClient      func(*testing.T, *githubapi.Client)
	configureClient func(*clienttypes.TestHandler)
	configureLocal  func(*localtypes.TestHandler)
	headers         []responseHeaderExpectation
	name            string
}

type responseHeaderExpectation struct {
	name  string
	value string
}

func TestHandlerTypeStrategiesWireParity(t *testing.T) {
	// These internal fixtures deliberately export generated client and handler
	// types so this external-package test can compile both type strategies.
	updatedAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	const largeInteger = int64(9_007_199_254_740_993)

	tests := []wireParityCase{
		{
			name: "repository query aliases lists pagination scalars and response headers",
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
				handler.ExpectGetRepository(repositoryVariables()).Respond(
					repositoryData(),
					clienttypes.WithStatus(http.StatusAccepted),
					clienttypes.WithHeader("x-github-request-id", "request-1"),
					clienttypes.WithHeaders(http.Header{
						"x-handler-mode": {"client"},
					}),
					clienttypes.WithPrimaryRateLimit(githubapi.RateLimit{
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
					After: new("cursor-1"),
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
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.GetRepository(t.Context(), repositoryVariables())
				require.NoError(t, err)
				require.NotNil(t, response.Repository)
				assert.Equal(t, "octo-org/octo-repo", response.Repository.FullName)
				assert.Equal(t, updatedAt, response.Repository.UpdatedAt)
				assert.JSONEq(t, `["red","blue"]`, string(response.Repository.PropertyValue))
				require.Len(t, response.Repository.Issues.Nodes, 1)
				assert.Equal(t, "I1", response.Repository.Issues.Nodes[0].Id)
				assert.Equal(t, "bug", response.Repository.Issues.Nodes[0].Title)
				assert.Equal(t, "cursor-2", requirePtrValue(t, response.Repository.Issues.PageInfo.EndCursor))
			},
		},
		{
			name: "mutation input and enum",
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectCreateRepository(githubapi.CreateRepositoryVariables{
					Input: githubapi.CreateRepositoryInput{
						Name:             "created",
						OwnerId:          new("O1"),
						Visibility:       new(githubapi.RepositoryVisibilityPrivate),
						ClientMutationId: new("mutation-1"),
					},
				}).Respond(githubapi.CreateRepositoryResponse{
					CreateRepository: githubapi.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
						Repository: new(githubapi.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
							Id:            "R2",
							NameWithOwner: "octo-org/created",
							UpdatedAt:     updatedAt,
						}),
						ClientMutationId: new("mutation-1"),
					},
				})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectCreateRepository(localtypes.CreateRepositoryVariables{
					Input: localtypes.CreateRepositoryInput{
						Name:             "created",
						OwnerId:          new("O1"),
						Visibility:       new(localtypes.RepositoryVisibilityPrivate),
						ClientMutationId: new("mutation-1"),
					},
				}).Respond(localtypes.CreateRepositoryResponse{
					CreateRepository: localtypes.CreateRepositoryCreateRepositoryCreateRepositoryPayload{
						Repository: new(localtypes.CreateRepositoryCreateRepositoryCreateRepositoryPayloadRepository{
							Id:            "R2",
							NameWithOwner: "octo-org/created",
							UpdatedAt:     updatedAt,
						}),
						ClientMutationId: new("mutation-1"),
					},
				})
			},
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.CreateRepository(t.Context(), githubapi.CreateRepositoryVariables{
					Input: githubapi.CreateRepositoryInput{
						Name:             "created",
						OwnerId:          new("O1"),
						Visibility:       new(githubapi.RepositoryVisibilityPrivate),
						ClientMutationId: new("mutation-1"),
					},
				})
				require.NoError(t, err)
				require.NotNil(t, response.CreateRepository.Repository)
				assert.Equal(t, "octo-org/created", response.CreateRepository.Repository.NameWithOwner)
			},
		},
		{
			name: "node catch all actual typename",
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetNode(githubapi.GetNodeVariables{Id: "user"}).
					Respond(githubapi.GetNodeResponse{
						Node: &githubapi.GetNodeNodeOctoqlOther{
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
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.GetNode(t.Context(), githubapi.GetNodeVariables{Id: "user"})
				require.NoError(t, err)
				other, ok := response.Node.(*githubapi.GetNodeNodeOctoqlOther)
				require.True(t, ok)
				assert.Equal(t, "User", other.Typename)
				assert.Equal(t, "U1", other.Id)
			},
		},
		{
			name: "search union list and catch all",
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectSearch(githubapi.SearchVariables{Query: "octo"}).
					Respond(githubapi.SearchResponse{
						Search: []githubapi.SearchSearchSearchResultItem{
							&githubapi.SearchSearchRepository{
								Id:            "R1",
								NameWithOwner: "octo-org/octo-repo",
							},
							&githubapi.SearchSearchIssue{Id: "I1", Title: "bug"},
							&githubapi.SearchSearchSearchResultItemOctoqlOther{
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
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.Search(t.Context(), githubapi.SearchVariables{Query: "octo"})
				require.NoError(t, err)
				require.Len(t, response.Search, 3)
				repository, ok := response.Search[0].(*githubapi.SearchSearchRepository)
				require.True(t, ok)
				assert.Equal(t, "octo-org/octo-repo", repository.NameWithOwner)
				other, ok := response.Search[2].(*githubapi.SearchSearchSearchResultItemOctoqlOther)
				require.True(t, ok)
				assert.Equal(t, "User", other.Typename)
			},
		},
		{
			name: "custom property value array",
			configureClient: func(handler *clienttypes.TestHandler) {
				value := json.RawMessage(`["one","two"]`)
				handler.ExpectEchoProperty(githubapi.EchoPropertyVariables{Value: value}).
					Respond(githubapi.EchoPropertyResponse{EchoProperty: value})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				value := json.RawMessage(`["one","two"]`)
				handler.ExpectEchoProperty(localtypes.EchoPropertyVariables{Value: value}).
					Respond(localtypes.EchoPropertyResponse{EchoProperty: value})
			},
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				value := json.RawMessage(`["one","two"]`)
				response, err := client.EchoProperty(t.Context(), githubapi.EchoPropertyVariables{Value: value})
				require.NoError(t, err)
				assert.JSONEq(t, string(value), string(response.EchoProperty))
			},
		},
		{
			name: "date time binding",
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectEchoAt(githubapi.EchoAtVariables{Value: updatedAt}).
					Respond(githubapi.EchoAtResponse{EchoAt: updatedAt})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectEchoAt(localtypes.EchoAtVariables{Value: updatedAt}).
					Respond(localtypes.EchoAtResponse{EchoAt: updatedAt})
			},
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.EchoAt(t.Context(), githubapi.EchoAtVariables{Value: updatedAt})
				require.NoError(t, err)
				assert.Equal(t, updatedAt, response.EchoAt)
			},
		},
		{
			name: "large integer and arbitrary json",
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectEchoAny(githubapi.EchoAnyVariables{Value: largeInteger}).
					Respond(githubapi.EchoAnyResponse{EchoAny: map[string]any{
						"count": 42,
						"items": []any{"one", true},
					}})
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectEchoAny(localtypes.EchoAnyVariables{Value: largeInteger}).
					Respond(localtypes.EchoAnyResponse{EchoAny: map[string]any{
						"count": 42,
						"items": []any{"one", true},
					}})
			},
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.EchoAny(t.Context(), githubapi.EchoAnyVariables{Value: largeInteger})
				require.NoError(t, err)
				arbitrary, ok := response.EchoAny.(map[string]any)
				require.True(t, ok)
				assert.InDelta(t, 42.0, arbitrary["count"], 0)
				assert.Equal(t, []any{"one", true}, arbitrary["items"])
			},
		},
		{
			name: "partial data graphql error and per error extensions",
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetRepository(githubapi.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "partial",
					First: 1,
					After: new(""),
				}).RespondDataAndErrors(
					githubapi.GetRepositoryResponse{
						Repository: new(githubapi.GetRepositoryRepository{
							Id:       "R1",
							FullName: "octo-org/partial",
						}),
					},
					githubapi.Error{
						Type:       "FORBIDDEN",
						Message:    "field hidden",
						Path:       githubapi.Path{"repository", "propertyValue"},
						Extensions: map[string]any{"code": "hidden"},
					},
				)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetRepository(localtypes.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "partial",
					First: 1,
					After: new(""),
				}).RespondDataAndErrors(
					localtypes.GetRepositoryResponse{
						Repository: new(localtypes.GetRepositoryRepository{
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
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.GetRepository(t.Context(), githubapi.GetRepositoryVariables{
					Owner: "octo-org",
					Name:  "partial",
					First: 1,
					After: new(""),
				})
				assert.Nil(t, response)
				partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
				require.True(t, ok)
				assert.Equal(t, "octo-org/partial", partialErr.PartialData().Repository.FullName)
				graphqlErrors, ok := errors.AsType[githubapi.Errors](err)
				require.True(t, ok)
				assert.Equal(t, "hidden", graphqlErrors[0].Extensions["code"])
			},
		},
		{
			name: "secondary rate limit http 200",
			headers: []responseHeaderExpectation{
				{
					name:  "X-RateLimit-Limit",
					value: "",
				},
				{
					name:  "X-RateLimit-Remaining",
					value: "",
				},
				{
					name:  "X-RateLimit-Used",
					value: "",
				},
				{
					name:  "Retry-After",
					value: "30",
				},
			},
			configureClient: func(handler *clienttypes.TestHandler) {
				handler.ExpectGetNode(githubapi.GetNodeVariables{Id: "secondary-200"}).
					RespondError(
						githubapi.Error{
							Type:    "ABUSE_DETECTED",
							Message: "slow down",
						},
						clienttypes.WithSecondaryRateLimit(30*time.Second),
					)
			},
			configureLocal: func(handler *localtypes.TestHandler) {
				handler.ExpectGetNode(localtypes.GetNodeVariables{Id: "secondary-200"}).
					RespondError(
						localtypes.Error{
							Type:    "ABUSE_DETECTED",
							Message: "slow down",
						},
						localtypes.WithSecondaryRateLimit(30*time.Second),
					)
			},
			callClient: func(t *testing.T, client *githubapi.Client) {
				t.Helper()
				response, err := client.GetNode(t.Context(), githubapi.GetNodeVariables{Id: "secondary-200"})
				assert.Nil(t, response)
				rateLimitErr, ok := errors.AsType[*githubapi.RateLimitError](err)
				require.True(t, ok)
				assert.Equal(t, githubapi.RateLimitSecondary, rateLimitErr.Kind)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientHandler := clienttypes.NewTestHandler(t)
			localHandler := localtypes.NewTestHandler(t)
			test.configureClient(clientHandler)
			test.configureLocal(localHandler)

			clientAPI, clientRecorder := newRecordedStrategyClient(t, clientHandler)
			localAPI, localRecorder := newRecordedStrategyClient(t, localHandler)
			test.callClient(t, clientAPI)
			test.callClient(t, localAPI)

			assert.Equal(t, clientRecorder.Code, localRecorder.Code)
			assert.Equal(t, clientRecorder.Header(), localRecorder.Header())
			for _, header := range test.headers {
				if header.value == "" {
					assert.Empty(t, clientRecorder.Header().Values(header.name))
					assert.Empty(t, localRecorder.Header().Values(header.name))
					continue
				}
				assert.Equal(t, header.value, clientRecorder.Header().Get(header.name))
				assert.Equal(t, header.value, localRecorder.Header().Get(header.name))
			}
			assert.JSONEq(t, clientRecorder.Body.String(), localRecorder.Body.String())
		})
	}
}

func newRecordedStrategyClient(
	t *testing.T,
	handler http.Handler,
) (*githubapi.Client, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handler.ServeHTTP(recorder, request)
		maps.Copy(writer.Header(), recorder.Header())
		writer.WriteHeader(recorder.Code)
		_, _ = writer.Write(recorder.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	return githubapi.NewClient(server.URL, server.Client()), recorder
}

func localRepositoryResponse(updatedAt time.Time) localtypes.GetRepositoryResponse {
	return localtypes.GetRepositoryResponse{
		Repository: new(localtypes.GetRepositoryRepository{
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
					EndCursor:   new("cursor-2"),
				},
			},
		}),
	}
}
