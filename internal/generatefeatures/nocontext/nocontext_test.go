package nocontext

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
	response, err := GetRepository(client, GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})

	require.NoError(t, err)
	assert.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
}

type testExecutor struct{}

func (testExecutor) Execute(
	_ context.Context,
	payload octoql.Payload,
	response any,
) (bool, error) {
	if payload.OperationName != "GetRepository" {
		return false, errors.New("unexpected operation")
	}
	decodedResponse, ok := response.(*GetRepositoryResponse)
	if !ok {
		return false, errors.New("unexpected response type")
	}
	decodedResponse.Repository = &GetRepositoryRepository{
		NameWithOwner: "octo-org/octo-repo",
	}
	return true, nil
}

func TestNoContextUsesCustomExecutor(t *testing.T) {
	response, err := GetRepository(testExecutor{}, GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
	})

	require.NoError(t, err)
	assert.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
}
