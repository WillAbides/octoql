package githubdefaults

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGeneratedGitHubDefaultsWireDecoding(t *testing.T) {
	const responseBody = `{
		"data": {
			"review": {
				"fullDatabaseId": "9007199254740993",
				"createdAt": "2026-07-21T12:34:56.123456789Z",
				"propertyValue": ["red","blue"],
				"author": {
					"__typename": "Bot",
					"login": "elm-bot",
					"url": "https://github.example/apps/elm-bot"
				},
				"editor": null
			}
		}
	}`

	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var requestPayload _octoqlPayload
			err := json.NewDecoder(request.Body).Decode(&requestPayload)
			require.NoError(t, err)
			assert.Equal(t, "getReview", requestPayload.OperationName)
			assert.Equal(t, 2, strings.Count(requestPayload.Query, "__typename"))

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			}, nil
		}),
	}
	client := NewClient("https://api.github.example/graphql", httpClient)

	response, err := client.getReview(t.Context())

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "9007199254740993", response.Review.FullDatabaseId)
	assert.Equal(t, time.Date(2026, time.July, 21, 12, 34, 56, 123456789, time.UTC), response.Review.CreatedAt)
	assert.Equal(t, 123456789, response.Review.CreatedAt.Nanosecond())
	require.NotNil(t, response.Review.PropertyValue)
	assert.JSONEq(t, `["red","blue"]`, string(*response.Review.PropertyValue))
	require.NotNil(t, response.Review.Author)
	assert.Equal(t, "Bot", response.Review.Author.Typename)
	assert.Equal(t, "elm-bot", response.Review.Author.Login)
	assert.Equal(t, "https://github.example/apps/elm-bot", response.Review.Author.Url)
	assert.Nil(t, response.Review.Editor)
}

func TestOmittedImplementationUnmarshal(t *testing.T) {
	tests := []struct {
		check func(*testing.T, runtimeAbstractsResponse)
		name  string
		body  string
	}{
		{
			name: "referenced implementation",
			body: `{"node":{"__typename":"Repository","id":"R1","url":"https://example.test/r","nameWithOwner":"octo/repo"},"nestedNodes":[],"actorNode":{"__typename":"Organization","login":"octo-org"}}`,
			check: func(t *testing.T, response runtimeAbstractsResponse) {
				require.NotNil(t, response.Node)
				repository, ok := response.Node.(*runtimeAbstractsNodeRepository)
				require.True(t, ok)
				assert.Equal(t, "octo/repo", repository.NameWithOwner)
				require.NotNil(t, response.ActorNode)
				organization, ok := response.ActorNode.(*runtimeAbstractsActorNodeOrganization)
				require.True(t, ok)
				assert.Equal(t, "octo-org", organization.Login)
			},
		},
		{
			name: "omitted implementation shared fields",
			body: `{"node":{"__typename":"Issue","id":"I1","url":"https://example.test/i"},"nestedNodes":[]}`,
			check: func(t *testing.T, response runtimeAbstractsResponse) {
				require.NotNil(t, response.Node)
				other, ok := response.Node.(*runtimeAbstractsNodeOctoqlOther)
				require.True(t, ok)
				assert.Equal(t, "Issue", other.GetTypename())
				assert.Equal(t, "I1", other.GetId())
				assert.Equal(t, "https://example.test/i", other.GetUrl())
			},
		},
		{
			name: "future typename",
			body: `{"node":{"__typename":"FutureNode","id":"F1","url":"https://example.test/f"},"nestedNodes":[]}`,
			check: func(t *testing.T, response runtimeAbstractsResponse) {
				require.NotNil(t, response.Node)
				other, ok := response.Node.(*runtimeAbstractsNodeOctoqlOther)
				require.True(t, ok)
				assert.Equal(t, "FutureNode", other.GetTypename())
			},
		},
		{
			name: "nulls and lists",
			body: `{"node":null,"nestedNodes":[[[null,{"__typename":"Bot","id":"B1","url":"https://example.test/b"}]]]}`,
			check: func(t *testing.T, response runtimeAbstractsResponse) {
				assert.Nil(t, response.Node)
				assert.Nil(t, response.NestedNodes[0][0][0])
				require.NotNil(t, response.NestedNodes[0][0][1])
				other, ok := response.NestedNodes[0][0][1].(*runtimeAbstractsNestedNodesNodeOctoqlOther)
				require.True(t, ok)
				assert.Equal(t, "Bot", other.GetTypename())
				assert.Equal(t, "B1", other.GetId())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response runtimeAbstractsResponse
			err := json.Unmarshal([]byte(test.body), &response)
			require.NoError(t, err)
			test.check(t, response)
		})
	}

	t.Run("missing typename", func(t *testing.T) {
		var response runtimeAbstractsResponse
		err := json.Unmarshal([]byte(`{"node":{"id":"I1","url":"https://example.test/i"}}`), &response)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "response was missing Node.__typename")
	})

	t.Run("null clears reused abstract field", func(t *testing.T) {
		var response runtimeAbstractsResponse
		err := json.Unmarshal(
			[]byte(`{"node":{"__typename":"Repository","id":"R1","url":"https://example.test/r","nameWithOwner":"octo/repo"}}`),
			&response,
		)
		require.NoError(t, err)
		require.NotNil(t, response.Node)

		err = json.Unmarshal([]byte(`{"node":null}`), &response)
		require.NoError(t, err)
		assert.Nil(t, response.Node)
	})

	t.Run("typed nil abstract values marshal as null", func(t *testing.T) {
		for _, node := range []runtimeAbstractsNode{
			(*runtimeAbstractsNodeRepository)(nil),
			(*runtimeAbstractsNodeOctoqlOther)(nil),
		} {
			current := node
			data, err := _octoqlMarshalruntimeAbstractsNode(&current)
			require.NoError(t, err)
			assert.Equal(t, "null", string(data))
		}
	})
}
