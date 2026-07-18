// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package clientgetter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
)

type testContext struct {
	context.Context
	client *octoql.Client
}

func (ctx testContext) OctoqlClient() *octoql.Client {
	return ctx.client
}

func TestClientGetterWithCustomContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(writer, `{"data":{"value":"custom context"}}`)
		if err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	ctx := testContext{
		Context: t.Context(),
		client:  octoql.NewClient(server.URL, server.Client()),
	}
	response, err := ClientGetter(ctx)

	require.NoError(t, err)
	assert.Equal(t, "custom context", response.Data.Value)
}
