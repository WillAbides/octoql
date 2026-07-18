// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package nocontext

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
)

func TestNoContextUsesBackground(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(
			writer,
			`{"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}}}`,
		)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	client := octoql.NewClient(server.URL, server.Client())
	response, err := GetRepository(client, "octo-org", "octo-repo")

	require.NoError(t, err)
	assert.Equal(t, "octo-org/octo-repo", response.Data.Repository.NameWithOwner)
}
