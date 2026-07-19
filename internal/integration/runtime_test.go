// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package integration

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
	githubclient "github.com/willabides/octoql/internal/generatefeatures/nocontext"
)

type integrationRoundTripFunc func(*http.Request) (*http.Response, error)

func (function integrationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGeneratedQueryTransportFailure(t *testing.T) {
	transportError := errors.New("transport failed")
	httpClient := &http.Client{
		Transport: integrationRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportError
		}),
	}
	client := octoql.NewClient("https://api.github.example/graphql", httpClient)

	response, err := githubclient.GetRepository(client, "octo-org", "octo-repo")

	require.ErrorIs(t, err, transportError)
	assert.Nil(t, response)
}
