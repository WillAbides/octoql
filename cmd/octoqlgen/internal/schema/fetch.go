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
	"sort"
	"strconv"
	"strings"

	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
)

type sanitizedURLDiagnostic struct {
	cause   error
	message string
}

func (e *sanitizedURLDiagnostic) Error() string {
	return e.message
}

func (e *sanitizedURLDiagnostic) Is(target error) bool {
	return errors.Is(e.cause, target)
}

func sanitizeURLDiagnostic(err error, requestURL string) error {
	if err == nil {
		return nil
	}

	urls := []string{requestURL}
	collectURLErrorURLs(err, &urls)

	message := err.Error()
	urls = uniqueURLsByDescendingLength(urls)
	for _, value := range urls {
		message = redactURLValue(message, value)
	}

	return &sanitizedURLDiagnostic{
		cause:   err,
		message: message,
	}
}

func collectURLErrorURLs(err error, urls *[]string) {
	if err == nil {
		return
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		*urls = append(*urls, urlErr.URL)
	}

	unwrapMany, ok := err.(interface{ Unwrap() []error })
	if ok {
		for _, nested := range unwrapMany.Unwrap() {
			collectURLErrorURLs(nested, urls)
		}
		return
	}

	unwrapOne, ok := err.(interface{ Unwrap() error })
	if ok {
		collectURLErrorURLs(unwrapOne.Unwrap(), urls)
	}
}

func uniqueURLsByDescendingLength(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	urls := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}

		_, exists := unique[value]
		if exists {
			continue
		}
		unique[value] = struct{}{}
		urls = append(urls, value)
	}

	sort.Slice(urls, func(left, right int) bool {
		return len(urls[left]) > len(urls[right])
	})
	return urls
}

func redactURLValue(message, value string) string {
	if value == "" {
		return message
	}

	redacted := redactURL(value)
	message = strings.ReplaceAll(message, value, redacted)
	return strings.ReplaceAll(message, strconv.Quote(value), strconv.Quote(redacted))
}

func redactURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "[redacted URL]"
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

type httpDoer interface {
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
	if source.Url != nil {
		return fetchURL(
			ctx,
			dependencies.httpClient,
			*source.Url,
			"",
			false,
			dependencies.maxResponseBytes,
		)
	}

	repository := source.GithubRepository
	if source.GithubDocs != nil {
		repository = githubDocsRepository(*source.GithubDocs)
	}
	if repository == nil {
		return nil, errors.New("remote schema source is missing")
	}
	host := "github.com"
	if repository.Host != nil {
		host = *repository.Host
	}

	token, err := discoverToken(
		ctx,
		host,
		dependencies.lookupEnvironment,
		dependencies.commandRunner,
	)
	if err != nil {
		return nil, err
	}

	requestURL, err := githubContentsURL(
		dependencies.githubAPIBaseURL(host),
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
	client httpDoer,
	requestURL string,
	token string,
	isGitHub bool,
	maxResponseBytes int64,
) (data []byte, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating schema request: %w", sanitizeURLDiagnostic(err, requestURL))
	}
	if isGitHub {
		request.Header.Set("Accept", "application/vnd.github.raw+json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetching schema: %w", sanitizeURLDiagnostic(err, requestURL))
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

func githubDocsRepository(source config.GithubDocs) *config.GithubRepository {
	filename := "schema.docs.graphql"
	if strings.HasPrefix(source.Version, "ghes-") {
		filename = "schema.docs-enterprise.graphql"
	}
	return &config.GithubRepository{
		Repository: "github/docs",
		Revision:   source.Revision,
		Path:       "src/graphql/data/" + source.Version + "/" + filename,
	}
}

func githubContentsURL(baseURL string, source config.GithubRepository) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing github api base url: %w", err)
	}

	owner, name, ok := strings.Cut(source.Repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", errors.New("github repository must be an owner/name pair")
	}
	pathParts := strings.Split(source.Path, "/")
	base.Path = strings.TrimSuffix(base.Path, "/") +
		"/repos/" + owner +
		"/" + name +
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
