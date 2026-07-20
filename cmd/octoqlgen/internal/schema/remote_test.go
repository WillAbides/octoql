package schema

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
)

func TestMaterializerResolveRejectsInvalidSourceVariants(t *testing.T) {
	t.Parallel()

	url := "https://example.test/schema.graphql"
	tests := []struct {
		source config.Source
		name   string
	}{
		{
			name: "local only",
		},
		{
			name: "docs and url",
			source: config.Source{
				GithubDocs: &config.GithubDocs{Version: "fpt"},
				Url:        &url,
			},
		},
		{
			name: "repository and url",
			source: config.Source{
				GithubRepository: &config.GithubRepository{
					Repository: "owner/repository",
					Path:       "schema.graphql",
				},
				Url: &url,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewMaterializer().Resolve(t.Context(), test.source)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "exactly one remote source")
		})
	}
}

func TestMaterializerLatestRevision(t *testing.T) {
	t.Parallel()

	const revision = "a5f6550fe5e9664e5f6c5d9b85c68f4f663e948e"
	expectedAuthorization := "Bearer " + "test-token"
	client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "https://api.example.test/graphql", request.URL.String())
		assert.Equal(t, expectedAuthorization, request.Header.Get("Authorization"))

		var payload struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Name  string `json:"name"`
				Owner string `json:"owner"`
				Path  string `json:"path"`
			} `json:"variables"`
		}
		err := json.NewDecoder(request.Body).Decode(&payload)
		require.NoError(t, err)
		assert.Equal(t, "LatestCommit", payload.OperationName)
		assert.Equal(t, "octo-repository", payload.Variables.Name)
		assert.Equal(t, "octo-owner", payload.Variables.Owner)
		assert.Equal(t, "schema.graphql", payload.Variables.Path)

		return response(http.StatusOK, []byte(
			`{"data":{"repository":{"defaultBranchRef":{"target":{"__typename":"Commit","history":{"nodes":[{"oid":"`+
				revision+`"}]}}}}}}`,
		)), nil
	})
	materializer := testGitHubMaterializer(client)
	materializer.LookupEnvironment = environmentLookup(map[string]string{
		"GH_TOKEN": "test-token",
	})
	dependencies := materializer.dependencies()

	actual, err := materializer.latestRevision(t.Context(), config.GithubRepository{
		Repository: "octo-owner/octo-repository",
		Path:       "schema.graphql",
	}, &dependencies)

	require.NoError(t, err)
	assert.Equal(t, revision, actual)
}
