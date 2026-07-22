package githubdefaults

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGeneratedGitHubDefaultsWireDecoding(t *testing.T) {
	const responseBody = `{
		"data": {
			"review": {
				"fullDatabaseId": "9007199254740993",
				"createdAt": "2026-07-21T12:34:56.123456789Z",
				"author": {
					"__typename": "Bot",
					"login": "elm-bot",
					"url": "https://github.example/apps/elm-bot"
				},
				"editor": null
			}
		}
	}`

	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var requestPayload _octoqlPayload
			err := json.NewDecoder(request.Body).Decode(&requestPayload)
			require.NoError(t, err)
			assert.Equal(t, "GetReview", requestPayload.OperationName)
			assert.Equal(t, 2, strings.Count(requestPayload.Query, "__typename"))

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			}, nil
		}),
	}
	client := NewClient("https://api.github.example/graphql", httpClient)

	response, err := client.GetReview(t.Context())

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "9007199254740993", response.Review.FullDatabaseId)
	assert.Equal(t, time.Date(2026, time.July, 21, 12, 34, 56, 123456789, time.UTC), response.Review.CreatedAt)
	assert.Equal(t, 123456789, response.Review.CreatedAt.Nanosecond())
	require.NotNil(t, response.Review.Author)
	assert.Equal(t, "Bot", response.Review.Author.Typename)
	assert.Equal(t, "elm-bot", response.Review.Author.Login)
	assert.Equal(t, "https://github.example/apps/elm-bot", response.Review.Author.Url)
	assert.Nil(t, response.Review.Editor)
}
