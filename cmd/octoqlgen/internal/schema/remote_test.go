package schema

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema/githubapi/githubapitest"
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
	handler := githubapitest.NewTestHandler(t)
	handler.ExpectLatestCommit(githubapitest.LatestCommitVariables{
		Name:  "octo-repository",
		Owner: "octo-owner",
		Path:  "schema.graphql",
	}).Respond(githubapitest.LatestCommitResponse{
		Repository: &githubapitest.LatestCommitRepository{
			DefaultBranchRef: &githubapitest.LatestCommitRepositoryDefaultBranchRef{
				Target: &githubapitest.LatestCommitRepositoryDefaultBranchRefTargetCommit{
					History: githubapitest.LatestCommitRepositoryDefaultBranchRefTargetCommitHistoryCommitHistoryConnection{
						Nodes: []*githubapitest.LatestCommitRepositoryDefaultBranchRefTargetCommitHistoryCommitHistoryConnectionNodesCommit{
							{
								Oid: revision,
							},
						},
					},
				},
			},
		},
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	materializer := testGitHubMaterializer(server.Client())
	materializer.GitHubGraphQLEndpoint = func(string) string {
		return server.URL
	}
	materializer.LookupEnvironment = environmentLookup(map[string]string{
		"GH_TOKEN": "test-token",
	})
	matDeps := materializer.dependencies()

	actual, err := materializer.latestRevision(t.Context(), config.GithubRepository{
		Repository: "octo-owner/octo-repository",
		Path:       "schema.graphql",
	}, &matDeps)

	require.NoError(t, err)
	assert.Equal(t, revision, actual)
}

func TestMaterializerLatestRevisionRequiresAuthentication(t *testing.T) {
	t.Parallel()

	materializer := testGitHubMaterializer(httpClientFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected GitHub request")
		return nil, nil
	}))
	matDeps := materializer.dependencies()

	_, err := materializer.latestRevision(t.Context(), config.GithubRepository{
		Repository: "octo-owner/octo-repository",
		Path:       "schema.graphql",
	}, &matDeps)

	require.EqualError(
		t,
		err,
		"github graphql authentication is required; set GH_TOKEN, GITHUB_TOKEN, or authenticate with gh",
	)
}
