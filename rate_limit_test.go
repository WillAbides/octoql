// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package octoql

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rateLimitRoundTripFunc func(*http.Request) (*http.Response, error)

func (function rateLimitRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestRateLimitFromHeader(t *testing.T) {
	now := time.Date(2026, time.July, 18, 19, 0, 0, 0, time.UTC)
	retryAt := now.Add(2 * time.Minute)

	tests := map[string]struct {
		want   *RateLimit
		header http.Header
	}{
		"all headers": {
			header: http.Header{
				"x-ratelimit-limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
				"X-RateLimit-Reset":     {"1784404800"},
				"X-RateLimit-Resource":  {" graphql "},
				"retry-after":           {"30"},
			},
			want: &RateLimit{
				Limit:      5000,
				Remaining:  0,
				Used:       5000,
				Reset:      time.Unix(1784404800, 0).UTC(),
				Resource:   "graphql",
				RetryAfter: 30 * time.Second,
				RetryAt:    now.Add(30 * time.Second),
			},
		},
		"retry after HTTP date": {
			header: http.Header{
				"Retry-After": {retryAt.Format(http.TimeFormat)},
			},
			want: &RateLimit{
				RetryAfter: 2 * time.Minute,
				RetryAt:    retryAt,
			},
		},
		"past retry after HTTP date": {
			header: http.Header{
				"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)},
			},
			want: &RateLimit{
				RetryAt: now.Add(-time.Minute),
			},
		},
		"missing headers": {
			header: http.Header{},
			want:   &RateLimit{},
		},
		"malformed headers": {
			header: http.Header{
				"X-RateLimit-Limit":     {"not-a-number"},
				"X-RateLimit-Remaining": {"-1"},
				"X-RateLimit-Used":      {"+1"},
				"X-RateLimit-Reset":     {"invalid"},
				"Retry-After":           {"tomorrow"},
			},
			want: &RateLimit{},
		},
		"overflow headers": {
			header: http.Header{
				"X-RateLimit-Limit":     {"999999999999999999999999999999"},
				"X-RateLimit-Remaining": {"999999999999999999999999999999"},
				"X-RateLimit-Used":      {"999999999999999999999999999999"},
				"X-RateLimit-Reset":     {"999999999999999999999999999999"},
				"Retry-After":           {"999999999999999999999999999999"},
			},
			want: &RateLimit{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := rateLimitFromHeader(test.header, now)
			assert.Equal(t, *test.want, got.RateLimit)
		})
	}
}

func TestRateLimitRejectsNegativeAndOverflowNumericHeaders(t *testing.T) {
	tests := map[string]http.Header{
		"negative limit":       {"X-RateLimit-Limit": {"-1"}},
		"negative remaining":   {"X-RateLimit-Remaining": {"-1"}},
		"negative used":        {"X-RateLimit-Used": {"-1"}},
		"negative reset":       {"X-RateLimit-Reset": {"-1"}},
		"negative retry after": {"Retry-After": {"-1"}},
		"overflow limit":       {"X-RateLimit-Limit": {"999999999999999999999999999999"}},
		"overflow remaining":   {"X-RateLimit-Remaining": {"999999999999999999999999999999"}},
		"overflow used":        {"X-RateLimit-Used": {"999999999999999999999999999999"}},
		"overflow reset":       {"X-RateLimit-Reset": {"999999999999999999999999999999"}},
		"overflow retry after": {"Retry-After": {"999999999999999999999999999999"}},
	}

	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			got := rateLimitFromHeader(header, time.Time{})
			assert.Equal(t, RateLimit{}, got.RateLimit)
		})
	}
}

func TestDoClassifiesRateLimits(t *testing.T) {
	now := time.Date(2026, time.July, 18, 19, 0, 0, 0, time.UTC)
	previousNow := rateLimitNow
	rateLimitNow = func() time.Time {
		return now
	}
	t.Cleanup(func() {
		rateLimitNow = previousNow
	})

	//nolint:govet // Test fields follow request and expected-response presentation order.
	tests := map[string]struct {
		body       string
		header     http.Header
		statusCode int
		wantRate   bool
		wantSecond bool
		wantHTTP   bool
	}{
		"primary limit on partial GraphQL response": {
			body: `{
				"data":{"value":"partial"},
				"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]
			}`,
			header: http.Header{
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Resource":  {"graphql"},
			},
			statusCode: http.StatusOK,
			wantRate:   true,
		},
		"secondary limit on GraphQL response": {
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After": {"30"},
			},
			statusCode: http.StatusOK,
			wantRate:   true,
			wantSecond: true,
		},
		"secondary limit on forbidden response": {
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After": {"30"},
			},
			statusCode: http.StatusForbidden,
			wantRate:   true,
			wantSecond: true,
			wantHTTP:   true,
		},
		"secondary limit takes precedence": {
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After":           {"30"},
				"X-RateLimit-Remaining": {"0"},
			},
			statusCode: http.StatusForbidden,
			wantRate:   true,
			wantSecond: true,
			wantHTTP:   true,
		},
		"arbitrary forbidden response": {
			body:       `{"errors":[{"message":"forbidden"}]}`,
			header:     http.Header{},
			statusCode: http.StatusForbidden,
			wantHTTP:   true,
		},
		"successful response with zero remaining": {
			body: `{"data":{"value":"complete"}}`,
			header: http.Header{
				"X-RateLimit-Remaining": {"0"},
			},
			statusCode: http.StatusOK,
		},
		"malformed retry after is not secondary": {
			body: `{"errors":[{"message":"forbidden"}]}`,
			header: http.Header{
				"Retry-After": {"later"},
			},
			statusCode: http.StatusForbidden,
			wantHTTP:   true,
		},
		"missing remaining is not primary": {
			body: `{"errors":[{"message":"forbidden"}]}`,
			header: http.Header{
				"X-RateLimit-Limit": {"5000"},
			},
			statusCode: http.StatusOK,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client := rateLimitClient(test.statusCode, test.header, test.body)
			response, err := Do[struct {
				Value string `json:"value"`
			}](t.Context(), client, testOperation(), nil)

			require.NotNil(t, response)
			if test.wantRate {
				rateLimitError, ok := errors.AsType[*RateLimitError](err)
				require.True(t, ok)
				wantKind := RateLimitPrimary
				if test.wantSecond {
					wantKind = RateLimitSecondary
				}
				assert.Equal(t, wantKind, rateLimitError.Kind)
				assert.Equal(t, response.HTTP.RateLimit, rateLimitError.RateLimit)
				assert.Equal(t, time.Time{}, rateLimitError.RateLimit.Reset)
				if test.wantSecond {
					assert.Equal(t, 30*time.Second, rateLimitError.RateLimit.RetryAfter)
					assert.Equal(t, now.Add(30*time.Second), rateLimitError.RateLimit.RetryAt)
				}
			} else {
				_, ok := errors.AsType[*RateLimitError](err)
				assert.False(t, ok)
			}

			_, isHTTPError := errors.AsType[*HTTPError](err)
			assert.Equal(t, test.wantHTTP, isHTTPError)
			if name == "primary limit on partial GraphQL response" {
				assert.Equal(t, "partial", response.Data.Value)
				_, errorsFound := errors.AsType[Errors](err)
				assert.True(t, errorsFound)
			}
		})
	}
}

func TestRateLimitMetadataDoesNotAliasHeaders(t *testing.T) {
	transportHeader := http.Header{
		"Retry-After":           {"30"},
		"X-RateLimit-Remaining": {"0"},
	}
	client := rateLimitClient(
		http.StatusForbidden,
		transportHeader,
		`{"errors":[{"message":"rate limited"}]}`,
	)

	response, err := Do[struct{}](t.Context(), client, testOperation(), nil)
	require.NotNil(t, response)
	rateLimitError, ok := errors.AsType[*RateLimitError](err)
	require.True(t, ok)
	httpError, ok := errors.AsType[*HTTPError](err)
	require.True(t, ok)

	transportHeader.Set("Retry-After", "99")
	assert.Equal(t, "30", response.HTTP.Header.Get("Retry-After"))
	assert.Equal(t, "30", httpError.HTTP.Header.Get("Retry-After"))
	assert.Equal(t, 30*time.Second, response.HTTP.RateLimit.RetryAfter)
	assert.Equal(t, 30*time.Second, httpError.HTTP.RateLimit.RetryAfter)
	assert.Equal(t, 30*time.Second, rateLimitError.RateLimit.RetryAfter)

	response.HTTP.Header.Set("Retry-After", "1")
	assert.Equal(t, "30", httpError.HTTP.Header.Get("Retry-After"))
}

func TestDoDoesNotClassifyTransportErrors(t *testing.T) {
	transportError := errors.New("transport failed")
	client := NewClient(
		"https://api.github.com/graphql",
		&http.Client{Transport: rateLimitRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportError
		})},
	)

	response, err := Do[struct{}](t.Context(), client, testOperation(), nil)
	assert.Nil(t, response)
	assert.ErrorIs(t, err, transportError)
	_, ok := errors.AsType[*RateLimitError](err)
	assert.False(t, ok)
}

func rateLimitClient(statusCode int, header http.Header, body string) *Client {
	return NewClient(
		"https://api.github.com/graphql",
		&http.Client{Transport: rateLimitRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: statusCode,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	)
}

func testOperation() Operation {
	return Operation{
		Name:  "Viewer",
		Query: "query Viewer { viewer { login } }",
	}
}
