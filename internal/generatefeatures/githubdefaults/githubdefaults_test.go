package githubdefaults

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
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
				"publishedOn": "2026-07-21",
				"updatedAt": "2026-07-21T12:34:56.987654321Z",
				"pushedAt": "2026-07-21T12:00:00Z",
				"oid": "0123456789abcdef0123456789abcdef01234567",
				"refname": "refs/heads/main",
				"sshRemote": "git@github.com:octo-org/octo-repo.git",
				"bodyHTML": "<p>ok</p>",
				"encoded": "b2t0bw==",
				"certificate": "-----BEGIN CERTIFICATE-----",
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
			if requestPayload.OperationName == "variableCollisions" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"data":{"echoCollision":"ok"}}`)),
				}, nil
			}
			assert.Equal(t, "getReview", requestPayload.OperationName)
			assert.NotContains(t, requestPayload.Query, "@octoqlgen")
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
	assert.Equal(t, "2026-07-21", response.Review.PublishedOn)
	assert.Equal(t, time.Date(2026, time.July, 21, 12, 34, 56, 987654321, time.UTC), response.Review.UpdatedAt)
	assert.Equal(t, time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC), response.Review.PushedAt)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", response.Review.Oid)
	assert.Equal(t, "refs/heads/main", response.Review.Refname)
	assert.Equal(t, "git@github.com:octo-org/octo-repo.git", response.Review.SshRemote)
	assert.Equal(t, "<p>ok</p>", response.Review.BodyHTML)
	assert.Equal(t, "b2t0bw==", response.Review.Encoded)
	assert.Equal(t, "-----BEGIN CERTIFICATE-----", response.Review.Certificate)
	require.NotNil(t, response.Review.PropertyValue)
	assert.JSONEq(t, `["red","blue"]`, string(*response.Review.PropertyValue))
	require.NotNil(t, response.Review.Author)
	assert.Equal(t, "Bot", response.Review.Author.Typename)
	assert.Equal(t, "elm-bot", response.Review.Author.Login)
	assert.Equal(t, "https://github.example/apps/elm-bot", response.Review.Author.Url)
	assert.Nil(t, response.Review.Editor)

	collisionResp, err := client.variableCollisions(t.Context(), variableCollisionsVariables{
		Ctx:      "ctx",
		Client:   "client",
		Err:      "err",
		Vars:     "vars",
		Response: "response",
		HasData:  "hasData",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", collisionResp.EchoCollision)
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
			name: "concrete OctoqlOther implementation",
			body: `{"node":{"__typename":"OctoqlOther","id":"O1","url":"https://example.test/o","value":"real"},"nestedNodes":[]}`,
			check: func(t *testing.T, response runtimeAbstractsResponse) {
				concrete, ok := response.Node.(*runtimeAbstractsNodeOctoqlOther)
				require.True(t, ok)
				assert.Equal(t, "real", concrete.Value)
			},
		},
		{
			name: "omitted implementation shared fields",
			body: `{"node":{"__typename":"Issue","id":"I1","url":"https://example.test/i"},"nestedNodes":[]}`,
			check: func(t *testing.T, response runtimeAbstractsResponse) {
				require.NotNil(t, response.Node)
				other, ok := response.Node.(*runtimeAbstractsNode2OctoqlOther)
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
				other, ok := response.Node.(*runtimeAbstractsNode2OctoqlOther)
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

	t.Run("null clears reused abstract fields and nested slices round-trip", func(t *testing.T) {
		const nestedWire = `{"node":null,"nestedNodes":[null,[],[null,[],[null,{"__typename":"Bot","id":"B1","url":"https://example.test/b"}]]],"actorNode":null}`
		var response runtimeAbstractsResponse
		err := json.Unmarshal([]byte(nestedWire), &response)
		require.NoError(t, err)
		remarshaled, err := json.Marshal(&response)
		require.NoError(t, err)
		require.JSONEq(t, nestedWire, string(remarshaled))

		const populated = `{"node":{"__typename":"Repository","id":"R1","url":"https://example.test/r","nameWithOwner":"octo/repo"},"nestedNodes":[[[{"__typename":"Bot","id":"B1","url":"https://example.test/b"}]]],"actorNode":null}`
		require.NoError(t, json.Unmarshal([]byte(populated), &response))
		require.NoError(t, json.Unmarshal([]byte(`{"node":null,"nestedNodes":[]}`), &response))
		assert.Nil(t, response.Node)
		assert.Empty(t, response.NestedNodes)
		assert.NotNil(t, response.NestedNodes)
	})

	t.Run("typed nil abstract values marshal as null and naming shapes stay distinct", func(t *testing.T) {
		for _, node := range []runtimeAbstractsNode{
			(*runtimeAbstractsNodeRepository)(nil),
			(*runtimeAbstractsNodeOctoqlOther)(nil),
			(*runtimeAbstractsNode2OctoqlOther)(nil),
		} {
			current := node
			var buf bytes.Buffer
			enc := jsontext.NewEncoder(&buf)
			require.NoError(t, _octoqlMarshalruntimeAbstractsNode(enc, &current))
			assert.Equal(t, "null", strings.TrimSpace(buf.String()))
		}

		shapes := namingShapesResponse{
			NamingUser:           namingShapesNamingUser{Id: "U1"},
			QueryNamingUser:      namingShapesQueryNamingUser{Id: "U2"},
			QueryTypedNamingUser: namingShapesQueryTypedNamingUserQueryNamingUser{Id: "U3"},
		}
		assert.Equal(t, "U1", shapes.NamingUser.Id)
		assert.Equal(t, "U2", shapes.QueryNamingUser.Id)
		assert.Equal(t, "U3", shapes.QueryTypedNamingUser.Id)
	})
}
