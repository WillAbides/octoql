// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package schema

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type httpClient struct {
	client http.Client
}

func (c *httpClient) Do(request *http.Request) (*http.Response, error) {
	return c.client.Do(request)
}

func (m *Materializer) fetch(
	ctx context.Context,
	source config.Source,
	dependencies *dependencies,
) ([]byte, error) {
	if source.URL != nil {
		return fetchURL(
			ctx,
			dependencies.httpClient,
			*source.URL,
			"",
			false,
			dependencies.maxResponseBytes,
		)
	}

	repository := source.GitHubRepository
	if source.GitHubDocs != nil {
		repository = githubDocsRepository(*source.GitHubDocs)
	}
	if repository == nil {
		return nil, errors.New("remote schema source is missing")
	}

	token, err := discoverToken(
		ctx,
		repository.Host,
		dependencies.lookupEnvironment,
		dependencies.commandRunner,
	)
	if err != nil {
		return nil, err
	}

	requestURL, err := githubContentsURL(
		dependencies.githubAPIBaseURL(repository.Host),
		*repository,
	)
	if err != nil {
		return nil, err
	}
	return fetchURL(
		ctx,
		dependencies.httpClient,
		requestURL,
		token,
		true,
		dependencies.maxResponseBytes,
	)
}

func fetchURL(
	ctx context.Context,
	client HTTPClient,
	requestURL string,
	token string,
	isGitHub bool,
	maxResponseBytes int64,
) (data []byte, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating schema request: %w", err)
	}
	if isGitHub {
		request.Header.Set("Accept", "application/vnd.github.raw+json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetching schema: %w", err)
	}
	defer func() {
		err = errors.Join(err, response.Body.Close())
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetching schema: http status %d", response.StatusCode)
	}
	if response.ContentLength > maxResponseBytes {
		return nil, fmt.Errorf(
			"fetching schema: response exceeds %d-byte limit",
			maxResponseBytes,
		)
	}

	data, err = io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading schema response: %w", err)
	}
	if int64(len(data)) > maxResponseBytes {
		return nil, fmt.Errorf(
			"fetching schema: response exceeds %d-byte limit",
			maxResponseBytes,
		)
	}
	return data, nil
}

func githubDocsRepository(source config.GitHubDocs) *config.GitHubRepository {
	filename := "schema.docs.graphql"
	if strings.HasPrefix(source.Version, "ghes-") {
		filename = "schema.docs-enterprise.graphql"
	}
	return &config.GitHubRepository{
		Repository: "github/docs",
		Revision:   source.Revision,
		Path:       "src/graphql/data/" + source.Version + "/" + filename,
		Host:       "github.com",
	}
}

func githubContentsURL(baseURL string, source config.GitHubRepository) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing github api base url: %w", err)
	}

	repositoryParts := strings.Split(source.Repository, "/")
	pathParts := strings.Split(source.Path, "/")
	base.Path = strings.TrimSuffix(base.Path, "/") +
		"/repos/" + repositoryParts[0] +
		"/" + repositoryParts[1] +
		"/contents/" + strings.Join(pathParts, "/")
	query := base.Query()
	query.Set("ref", source.Revision)
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func defaultGitHubAPIBaseURL(host string) string {
	if host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}
