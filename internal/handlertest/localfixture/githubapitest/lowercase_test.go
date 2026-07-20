package githubapitest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLowercaseOperation(t *testing.T) {
	handler := NewTestHandler(t)
	handler.ExpectgetViewer().Respond(getViewerResponse{
		Viewer: getViewerViewerUser{Login: "octocat"},
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"https://api.github.example/graphql",
		strings.NewReader(`{"operationName":"getViewer","variables":{}}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Data getViewerResponse `json:"data"`
	}
	err := json.Unmarshal(response.Body.Bytes(), &payload)
	require.NoError(t, err)
	assert.Equal(t, "octocat", payload.Data.Viewer.Login)
}
