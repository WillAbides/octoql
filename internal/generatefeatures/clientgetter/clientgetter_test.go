package clientgetter

import (
	"context"
	"errors"
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
	client    *octoql.Client
	clientErr error
}

func (ctx testContext) OctoqlClient() (*octoql.Client, error) {
	return ctx.client, ctx.clientErr
}

func TestClientGetterWithCustomContext(t *testing.T) {
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

	ctx := testContext{
		Context: t.Context(),
		client:  octoql.NewClient(server.URL, server.Client()),
	}
	response, err := getRepository(ctx, getRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})

	require.NoError(t, err)
	assert.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
}

func TestClientGetterFailure(t *testing.T) {
	clientErr := errors.New("client unavailable")
	ctx := testContext{
		Context:   t.Context(),
		clientErr: clientErr,
	}

	response, err := getRepository(ctx, getRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})

	assert.Nil(t, response)
	assert.ErrorIs(t, err, clientErr)
	_, hasPartial := errors.AsType[*getRepositoryPartialDataError](err)
	assert.False(t, hasPartial)
}

func TestClientGetterNilClient(t *testing.T) {
	ctx := testContext{Context: t.Context()}

	response, err := getRepository(ctx, getRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})

	assert.Nil(t, response)
	assert.EqualError(t, err, "octoql: client is nil")
	_, hasPartial := errors.AsType[*getRepositoryPartialDataError](err)
	assert.False(t, hasPartial)
}
