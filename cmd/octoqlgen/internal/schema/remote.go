package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/willabides/octoql"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema/githubapi"
)

var fullCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// RemoteResult is exact validated schema content together with its immutable pin.
type RemoteResult struct {
	Revision string
	SHA256   string
	Data     []byte
}

// Resolve fetches a remote schema. GitHub-backed sources are pinned to the
// latest commit that changed the selected path before their content is fetched.
func (m *Materializer) Resolve(ctx context.Context, source config.Source) (RemoteResult, error) {
	sourceCount := sourceVariantCount(source)
	if sourceCount != 1 {
		return RemoteResult{}, errors.New("schema source must set exactly one remote source variant")
	}
	deps := m.dependencies()
	timeoutContext, cancel := context.WithTimeout(ctx, deps.timeout)
	defer cancel()

	if source.Url != nil {
		data, err := m.fetch(timeoutContext, source, &deps)
		if err != nil {
			return RemoteResult{}, err
		}
		err = validateSDL(data)
		if err != nil {
			return RemoteResult{}, err
		}
		return remoteResult(data, ""), nil
	}

	repository := source.GithubRepository
	if source.GithubDocs != nil {
		repository = githubDocsRepository(*source.GithubDocs)
	}
	if repository == nil {
		return RemoteResult{}, errors.New("remote schema source is missing")
	}

	revision, err := m.latestRevision(timeoutContext, *repository, &deps)
	if err != nil {
		return RemoteResult{}, err
	}
	pinned := source
	if pinned.GithubDocs != nil {
		pinned.GithubDocs = &config.GithubDocs{
			Version:  pinned.GithubDocs.Version,
			Revision: revision,
		}
	}
	if pinned.GithubRepository != nil {
		pinned.GithubRepository = &config.GithubRepository{
			Host:       pinned.GithubRepository.Host,
			Path:       pinned.GithubRepository.Path,
			Repository: pinned.GithubRepository.Repository,
			Revision:   revision,
		}
	}
	data, err := m.fetch(timeoutContext, pinned, &deps)
	if err != nil {
		return RemoteResult{}, err
	}
	err = validateSDL(data)
	if err != nil {
		return RemoteResult{}, err
	}
	return remoteResult(data, revision), nil
}

func remoteResult(data []byte, revision string) RemoteResult {
	sum := sha256.Sum256(data)
	return RemoteResult{
		Data:     data,
		Revision: revision,
		SHA256:   hex.EncodeToString(sum[:]),
	}
}

func (m *Materializer) latestRevision(
	ctx context.Context,
	repository config.GithubRepository,
	deps *dependencies,
) (string, error) {
	owner, name, err := githubRepositoryParts(repository.Repository)
	if err != nil {
		return "", err
	}
	host := "github.com"
	if repository.Host != nil {
		host = *repository.Host
	}
	token, err := discoverToken(ctx, host, deps.lookupEnvironment, deps.commandRunner)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", errors.New(
			"github graphql authentication is required; set GH_TOKEN, GITHUB_TOKEN, or authenticate with gh",
		)
	}
	client := octoql.NewClient(deps.githubGraphQLEndpoint(host), &http.Client{
		Transport: httpClientTransport{client: deps.httpClient},
	})
	err = client.SetBearerToken(token)
	if err != nil {
		return "", fmt.Errorf("configuring github graphql authentication: %w", err)
	}

	result, err := githubapi.LatestCommit(ctx, client, githubapi.LatestCommitVariables{
		Owner: owner,
		Name:  name,
		Path:  repository.Path,
	})
	if err != nil {
		return "", fmt.Errorf("resolving schema revision: %w", err)
	}
	if result.Repository == nil ||
		result.Repository.DefaultBranchRef == nil ||
		result.Repository.DefaultBranchRef.Target == nil {
		return "", errors.New("no github commit changed the configured schema path")
	}

	target, ok := result.Repository.DefaultBranchRef.Target.(*githubapi.LatestCommitRepositoryDefaultBranchRefTargetCommit)
	if !ok || len(target.History.Nodes) == 0 || target.History.Nodes[0] == nil {
		return "", errors.New("no github commit changed the configured schema path")
	}
	revision := target.History.Nodes[0].Oid
	if !fullCommitSHA.MatchString(revision) {
		return "", errors.New("github commits response did not contain a full commit sha")
	}
	return revision, nil
}

type httpClientTransport struct {
	client httpDoer
}

func (t httpClientTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.client.Do(request)
}

func githubRepositoryParts(repository string) (string, string, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", errors.New("github repository must be an owner/name pair")
	}
	return owner, name, nil
}
