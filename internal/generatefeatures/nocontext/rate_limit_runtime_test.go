package nocontext

import (
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rateLimitRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rateLimitRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRateLimitFromHeader(t *testing.T) {
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
			},
			want: &RateLimit{
				Limit:     5000,
				Remaining: 0,
				Used:      5000,
				Reset:     time.Unix(1784404800, 0).UTC(),
				Resource:  "graphql",
			},
		},
		"incomplete numeric headers": {
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
			},
		},
		"missing headers": {
			header: http.Header{},
		},
		"malformed headers": {
			header: http.Header{
				"X-RateLimit-Limit":     {"-1"},
				"X-RateLimit-Remaining": {"-1"},
				"X-RateLimit-Used":      {"+1"},
				"X-RateLimit-Reset":     {"-1"},
			},
		},
		"overflow headers": {
			header: http.Header{
				"X-RateLimit-Limit":     {"999999999999999999999999999999"},
				"X-RateLimit-Remaining": {"999999999999999999999999999999"},
				"X-RateLimit-Used":      {"999999999999999999999999999999"},
				"X-RateLimit-Reset":     {"999999999999999999999999999999"},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := _octoqlRateLimitFromHeader(test.header)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestRetryAfterFromHeader(t *testing.T) {
	now := time.Date(2026, time.July, 18, 19, 0, 0, 0, time.UTC)
	retryAt := now.Add(2 * time.Minute)

	tests := map[string]struct {
		wantRetryAt    time.Time
		header         http.Header
		wantRetryAfter time.Duration
		wantValid      bool
	}{
		"delta seconds": {
			header: http.Header{
				"retry-after": {"30"},
			},
			wantRetryAfter: 30 * time.Second,
			wantRetryAt:    now.Add(30 * time.Second),
			wantValid:      true,
		},
		"HTTP date": {
			header: http.Header{
				"Retry-After": {retryAt.Format(http.TimeFormat)},
			},
			wantRetryAfter: 2 * time.Minute,
			wantRetryAt:    retryAt,
			wantValid:      true,
		},
		"past HTTP date": {
			header: http.Header{
				"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)},
			},
			wantRetryAt: now.Add(-time.Minute),
			wantValid:   true,
		},
		"missing header": {
			header: http.Header{},
		},
		"malformed header": {
			header: http.Header{
				"Retry-After": {"-1"},
			},
		},
		"overflow header": {
			header: http.Header{
				"Retry-After": {"999999999999999999999999999999"},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			gotRetryAfter, gotRetryAt, gotValid := _octoqlRetryAfterFromHeader(test.header, now)
			assert.Equal(t, test.wantValid, gotValid)
			assert.Equal(t, test.wantRetryAfter, gotRetryAfter)
			assert.Equal(t, test.wantRetryAt, gotRetryAt)
		})
	}
}

type rateLimitFacet interface {
	error
	RateLimitKind() string
	RetryAt() time.Time
}

func TestNonnegativeHeaderUnixRepresentability(t *testing.T) {
	maximum := _octoqlMaxUnixSeconds()
	tests := map[string]struct {
		value string
		want  bool
	}{
		"largest representable Unix second": {
			value: strconv.FormatUint(maximum, 10),
			want:  true,
		},
		"first unrepresentable Unix second": {
			value: strconv.FormatUint(maximum+1, 10),
		},
		"maximum int64 Unix second": {
			value: strconv.FormatInt(math.MaxInt64, 10),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			header := http.Header{"X-RateLimit-Reset": {test.value}}
			got, valid := _octoqlNonnegativeHeaderUnix(header, "X-RateLimit-Reset")
			assert.Equal(t, test.want, valid)
			wantTime := time.Time{}
			if test.want {
				wantTime = time.Unix(int64(maximum), 0).UTC()
			}
			assert.Equal(t, wantTime, got)
		})
	}
}

func TestDoClassifiesRateLimits(t *testing.T) {
	now := time.Date(2026, time.July, 18, 19, 0, 0, 0, time.UTC)
	reset := time.Unix(1784404800, 0).UTC()
	previousNow := _octoqlRateLimitNow
	_octoqlRateLimitNow = func() time.Time {
		return now
	}
	t.Cleanup(func() {
		_octoqlRateLimitNow = previousNow
	})

	tests := map[string]struct {
		wantReset         time.Time
		wantRateLimit     *RateLimit
		body              string
		header            http.Header
		statusCode        int
		wantData          bool
		wantRate          bool
		wantSecond        bool
		wantResponseError bool
	}{
		"primary limit on partial GraphQL response": {
			wantRateLimit: &RateLimit{
				Limit:     5000,
				Remaining: 0,
				Used:      5000,
				Resource:  "graphql",
			},
			body: `{
				"data":{"value":"partial"},
				"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]
			}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
				"X-RateLimit-Resource":  {"graphql"},
			},
			statusCode:        http.StatusOK,
			wantData:          true,
			wantRate:          true,
			wantResponseError: true,
		},
		"primary limit with remaining points": {
			wantReset: reset,
			wantRateLimit: &RateLimit{
				Limit:     5000,
				Remaining: 3,
				Used:      4997,
				Reset:     reset,
				Resource:  "graphql",
			},
			body: `{"errors":[{"type":"RATE_LIMITED","message":"query cost exceeds remaining points"}]}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"3"},
				"X-RateLimit-Used":      {"4997"},
				"X-RateLimit-Reset":     {"1784404800"},
				"X-RateLimit-Resource":  {"graphql"},
			},
			statusCode:        http.StatusOK,
			wantRate:          true,
			wantResponseError: true,
		},
		"primary limit without primary quota headers": {
			body:              `{"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]}`,
			header:            http.Header{},
			statusCode:        http.StatusOK,
			wantRate:          true,
			wantResponseError: true,
		},
		"primary limit on too many requests": {
			body:              `{"errors":[{"message":"too many requests"}]}`,
			header:            http.Header{},
			statusCode:        http.StatusTooManyRequests,
			wantRate:          true,
			wantResponseError: true,
		},
		"primary limit on forbidden response with zero remaining": {
			wantReset: reset,
			wantRateLimit: &RateLimit{
				Limit:     5000,
				Remaining: 0,
				Used:      5000,
				Reset:     reset,
				Resource:  "graphql",
			},
			body: `{"errors":[{"message":"API rate limit exceeded"}]}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
				"X-RateLimit-Reset":     {"1784404800"},
				"X-RateLimit-Resource":  {"graphql"},
			},
			statusCode:        http.StatusForbidden,
			wantRate:          true,
			wantResponseError: true,
		},
		"forbidden response with incomplete rate limit headers is not primary": {
			body: `{"errors":[{"message":"API rate limit exceeded"}]}`,
			header: http.Header{
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Reset":     {"1784404800"},
			},
			statusCode:        http.StatusForbidden,
			wantResponseError: true,
		},
		"forbidden response with remaining points is not primary": {
			body: `{"errors":[{"message":"forbidden"}]}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"3"},
				"X-RateLimit-Used":      {"4997"},
				"X-RateLimit-Reset":     {"1784404800"},
			},
			statusCode:        http.StatusForbidden,
			wantResponseError: true,
		},
		"rate limited GraphQL error on service unavailable is not rate limit": {
			body:              `{"errors":[{"type":"RATE_LIMITED","message":"temporarily unavailable"}]}`,
			header:            http.Header{},
			statusCode:        http.StatusServiceUnavailable,
			wantResponseError: true,
		},
		"secondary limit on GraphQL response": {
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After": {"30"},
			},
			statusCode:        http.StatusOK,
			wantRate:          true,
			wantSecond:        true,
			wantResponseError: true,
		},
		"secondary limit on forbidden response": {
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After": {"30"},
			},
			statusCode:        http.StatusForbidden,
			wantRate:          true,
			wantSecond:        true,
			wantResponseError: true,
		},
		"secondary limit takes precedence": {
			wantRateLimit: &RateLimit{
				Limit:     5000,
				Remaining: 0,
				Used:      5000,
			},
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After":           {"30"},
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
			},
			statusCode:        http.StatusForbidden,
			wantRate:          true,
			wantSecond:        true,
			wantResponseError: true,
		},
		"retry after service unavailable response": {
			body: `{"errors":[{"message":"temporarily unavailable"}]}`,
			header: http.Header{
				"Retry-After": {"30"},
			},
			statusCode:        http.StatusServiceUnavailable,
			wantResponseError: true,
		},
		"retry after redirect response": {
			body: `{"errors":[{"message":"redirected"}]}`,
			header: http.Header{
				"Retry-After": {"30"},
			},
			statusCode:        http.StatusFound,
			wantResponseError: true,
		},
		"arbitrary forbidden response": {
			body:              `{"errors":[{"message":"forbidden"}]}`,
			header:            http.Header{},
			statusCode:        http.StatusForbidden,
			wantResponseError: true,
		},
		"successful response with zero remaining": {
			body: `{"data":{"value":"complete"}}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
			},
			statusCode: http.StatusOK,
			wantData:   true,
		},
		"unrelated GraphQL error with zero remaining": {
			body: `{"errors":[{"type":"FORBIDDEN","message":"field unavailable"}]}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
			},
			statusCode:        http.StatusOK,
			wantResponseError: true,
		},
		"malformed retry after is not secondary": {
			body: `{"errors":[{"message":"forbidden"}]}`,
			header: http.Header{
				"Retry-After": {"later"},
			},
			statusCode:        http.StatusForbidden,
			wantResponseError: true,
		},
		"missing remaining is not primary": {
			body: `{"errors":[{"message":"forbidden"}]}`,
			header: http.Header{
				"X-RateLimit-Limit": {"5000"},
			},
			statusCode:        http.StatusOK,
			wantResponseError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client := rateLimitClient(test.statusCode, test.header, test.body)
			response, err := doRateLimitOperation[struct {
				Value string `json:"value"`
			}](t, client)

			assert.Equal(t, test.wantData, response != nil)
			if !test.wantRate {
				_, ok := errors.AsType[*RateLimitError](err)
				assert.False(t, ok)
			}
			if test.wantRate {
				rateLimitError, ok := errors.AsType[*RateLimitError](err)
				require.True(t, ok)
				wantKind := RateLimitPrimary
				if test.wantSecond {
					wantKind = RateLimitSecondary
				}
				assert.Equal(t, wantKind, rateLimitError.Kind)
				assert.Equal(t, test.wantRateLimit, rateLimitError.RateLimit)
				var facet rateLimitFacet
				require.ErrorAs(t, err, &facet)
				assert.Equal(t, string(wantKind), facet.RateLimitKind())
				if test.wantSecond {
					assert.Equal(t, 30*time.Second, rateLimitError.RetryAfter)
					assert.Equal(t, now.Add(30*time.Second), rateLimitError.RetryAt())
					assert.Equal(t, now.Add(30*time.Second), facet.RetryAt())
				}
				if !test.wantSecond {
					assert.Zero(t, rateLimitError.RetryAfter)
					assert.Equal(t, test.wantReset, rateLimitError.RetryAt())
					assert.Equal(t, test.wantReset, facet.RetryAt())
				}
			}

			_, hasResponseError := errors.AsType[*ResponseError](err)
			assert.Equal(t, test.wantResponseError, hasResponseError)
			if name == "primary limit on partial GraphQL response" {
				require.NotNil(t, response)
				assert.Equal(t, "partial", response.Value)
				_, errorsFound := errors.AsType[Errors](err)
				assert.True(t, errorsFound)
			}
			if name == "successful response with zero remaining" {
				rateLimit, known := client.RateLimit()
				require.True(t, known)
				assert.Zero(t, rateLimit.Remaining)
			}
		})
	}
}

func TestRateLimitErrorsDoNotAliasHeaders(t *testing.T) {
	transportHeader := http.Header{
		"X-GitHub-Request-ID":   {"request-rate-limit"},
		"Retry-After":           {"30"},
		"X-RateLimit-Limit":     {"5000"},
		"X-RateLimit-Remaining": {"0"},
		"X-RateLimit-Used":      {"5000"},
	}
	client := rateLimitClient(
		http.StatusForbidden,
		transportHeader,
		`{"errors":[{"message":"rate limited"}]}`,
	)

	response, err := doRateLimitOperation[struct{}](t, client)
	assert.Nil(t, response)
	rateLimitError, ok := errors.AsType[*RateLimitError](err)
	require.True(t, ok)
	require.NotNil(t, rateLimitError.RateLimit)
	responseError, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok)

	transportHeader.Set("Retry-After", "99")
	rateLimitError.RateLimit.Remaining = 99
	assert.Equal(t, 30*time.Second, rateLimitError.RetryAfter)
	assert.Equal(t, "request-rate-limit", responseError.RequestID)
	snapshot, known := client.RateLimit()
	require.True(t, known)
	assert.Zero(t, snapshot.Remaining)
}

func TestClientRateLimitNewestObservationWins(t *testing.T) {
	client := &Client{}
	client._octoqlObserveRateLimit(2, &RateLimit{
		Limit:     100,
		Remaining: 20,
		Used:      80,
	})
	client._octoqlObserveRateLimit(1, &RateLimit{
		Limit:     100,
		Remaining: 90,
		Used:      10,
	})

	rateLimit, known := client.RateLimit()
	require.True(t, known)
	assert.Equal(t, 20, rateLimit.Remaining)
	assert.Equal(t, 80, rateLimit.Used)
}

func TestClientRateLimitConcurrentAccess(t *testing.T) {
	client := &Client{}
	var waitGroup sync.WaitGroup
	for observation := uint64(1); observation <= 100; observation++ {
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			client._octoqlObserveRateLimit(observation, &RateLimit{
				Limit:     100,
				Remaining: int(observation),
				Used:      100 - int(observation),
			})
		}()
		go func() {
			defer waitGroup.Done()
			client.RateLimit()
		}()
	}
	waitGroup.Wait()

	rateLimit, known := client.RateLimit()
	require.True(t, known)
	assert.Equal(t, 100, rateLimit.Remaining)
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

func doRateLimitOperation[T any](t *testing.T, client *Client) (*T, error) {
	response := new(T)
	hasData, err := client._octoqlExecute(
		t.Context(),
		_octoqlPayload{
			OperationName: "Viewer",
			Query:         "query Viewer { viewer { login } }",
		},
		response,
	)
	if !hasData {
		return nil, err
	}
	return response, err
}
