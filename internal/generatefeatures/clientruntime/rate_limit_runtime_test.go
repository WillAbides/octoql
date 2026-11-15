package clientruntime

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

type rateLimitFacet interface {
	error
	RateLimitKind() string
	RetryAt() time.Time
}

func TestRateLimitNumericBoundaries(t *testing.T) {
	maxInt := strconv.Itoa(math.MaxInt)
	overMaxInt := strconv.FormatUint(uint64(math.MaxInt)+1, 10)
	got := _octoqlRateLimitFromHeader(http.Header{
		"X-RateLimit-Limit":     {maxInt},
		"X-RateLimit-Remaining": {maxInt},
		"X-RateLimit-Used":      {maxInt},
	})
	assert.Equal(t, &RateLimit{
		Limit:     math.MaxInt,
		Remaining: math.MaxInt,
		Used:      math.MaxInt,
	}, got)
	assert.Nil(t, _octoqlRateLimitFromHeader(http.Header{
		"X-RateLimit-Limit":     {overMaxInt},
		"X-RateLimit-Remaining": {"0"},
		"X-RateLimit-Used":      {"5000"},
	}))

	maximumUnix := _octoqlMaxUnixSeconds()
	gotTime, valid := _octoqlNonnegativeHeaderUnix(
		http.Header{"X-RateLimit-Reset": {strconv.FormatUint(maximumUnix, 10)}},
		"X-RateLimit-Reset",
	)
	assert.True(t, valid)
	assert.Equal(t, time.Unix(int64(maximumUnix), 0).UTC(), gotTime)

	_, valid = _octoqlNonnegativeHeaderUnix(
		http.Header{"X-RateLimit-Reset": {strconv.FormatUint(maximumUnix+1, 10)}},
		"X-RateLimit-Reset",
	)
	assert.False(t, valid)
}

func TestDoClassifiesRateLimits(t *testing.T) {
	now := time.Date(2026, time.July, 18, 19, 0, 0, 0, time.UTC)
	reset := time.Unix(1784404800, 0).UTC()
	httpDate := now.Add(2 * time.Minute)
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
		wantRetryAfter    time.Duration
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
				"data":{"repository":{"nameWithOwner":"octo-org/partial"}},
				"errors":[{"type":"RATE_LIMITED","message":"quota exhausted"}]
			}`,
			header: http.Header{
				"x-ratelimit-limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
				"X-RateLimit-Resource":  {" graphql "},
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
		"primary limit on too many requests without headers": {
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
		"forbidden response with incomplete or remaining quota is not primary": {
			body: `{"errors":[{"message":"forbidden"}]}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"3"},
				"X-RateLimit-Used":      {"4997"},
			},
			statusCode:        http.StatusForbidden,
			wantResponseError: true,
		},
		"secondary limit with HTTP-date Retry-After": {
			body: `{"errors":[{"message":"slow down"}]}`,
			header: http.Header{
				"Retry-After": {httpDate.Format(http.TimeFormat)},
			},
			statusCode:        http.StatusOK,
			wantRetryAfter:    2 * time.Minute,
			wantReset:         httpDate,
			wantRate:          true,
			wantSecond:        true,
			wantResponseError: true,
		},
		"secondary limit takes precedence over primary headers": {
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
			wantRetryAfter:    30 * time.Second,
			wantReset:         now.Add(30 * time.Second),
			wantRate:          true,
			wantSecond:        true,
			wantResponseError: true,
		},
		"service unavailable and malformed headers are not rate limits": {
			body: `{"errors":[{"type":"RATE_LIMITED","message":"temporarily unavailable"}]}`,
			header: http.Header{
				"Retry-After":           {"-1"},
				"X-RateLimit-Limit":     {"-1"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"+1"},
			},
			statusCode:        http.StatusServiceUnavailable,
			wantResponseError: true,
		},
		"successful response with zero remaining records snapshot without error": {
			body: `{"data":{"repository":{"nameWithOwner":"octo-org/octo-repo"}}}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
			},
			statusCode: http.StatusOK,
			wantData:   true,
		},
		"unrelated GraphQL error with zero remaining is not rate limit": {
			body: `{"errors":[{"type":"FORBIDDEN","message":"field unavailable"}]}`,
			header: http.Header{
				"X-RateLimit-Limit":     {"5000"},
				"X-RateLimit-Remaining": {"0"},
				"X-RateLimit-Used":      {"5000"},
			},
			statusCode:        http.StatusOK,
			wantResponseError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client := newResponseClient(test.statusCode, test.header, test.body)
			response, err := invokeRepositoryQuery(t, client)

			if err == nil {
				assert.Equal(t, test.wantData, response != nil)
			}
			if err != nil {
				assert.Nil(t, response)
				partialErr, hasPartial := errors.AsType[*getRepositoryPartialDataError](err)
				assert.Equal(t, test.wantData, hasPartial)
				if hasPartial {
					assert.Equal(t, "octo-org/partial", partialErr.PartialData().Repository.NameWithOwner)
				}
			}
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
				assert.Equal(t, test.wantRetryAfter, rateLimitError.RetryAfter)
				assert.Equal(t, test.wantReset, rateLimitError.RetryAt())
				var facet rateLimitFacet
				require.ErrorAs(t, err, &facet)
				assert.Equal(t, string(wantKind), facet.RateLimitKind())
				assert.Equal(t, test.wantReset, facet.RetryAt())
			}

			_, hasResponseError := errors.AsType[*ResponseError](err)
			assert.Equal(t, test.wantResponseError, hasResponseError)
			if test.wantData && err == nil {
				rateLimit, known := client.RateLimit()
				require.True(t, known)
				assert.Zero(t, rateLimit.Remaining)
			}
		})
	}
}

type delayedReader struct {
	reader  io.Reader
	onRead  func()
	started bool
}

func (r *delayedReader) Read(buffer []byte) (int, error) {
	if !r.started {
		r.started = true
		r.onRead()
	}
	return r.reader.Read(buffer)
}

func TestDoAnchorsRetryAfterToResponseArrival(t *testing.T) {
	arrival := time.Date(2026, time.July, 18, 19, 0, 0, 0, time.UTC)
	currentNow := arrival
	clockReads := 0
	previousNow := _octoqlRateLimitNow
	_octoqlRateLimitNow = func() time.Time {
		clockReads++
		return currentNow
	}
	t.Cleanup(func() {
		_octoqlRateLimitNow = previousNow
	})

	successClient := newResponseClient(
		http.StatusOK,
		http.Header{},
		`{"data":{}}`,
	)
	_, err := successClient.wireSemantics(t.Context())
	require.NoError(t, err)
	assert.Zero(t, clockReads)

	slowFailureClient := newStaticResponseClient(
		http.StatusOK,
		http.Header{
			"Retry-After": {"30"},
		},
		io.NopCloser(&delayedReader{
			reader: strings.NewReader(`{"errors":[{"message":"slow down"}]}`),
			onRead: func() {
				currentNow = currentNow.Add(5 * time.Second)
			},
		}),
	)

	_, err = slowFailureClient.wireSemantics(t.Context())
	rateLimitError, ok := errors.AsType[*RateLimitError](err)
	require.True(t, ok)
	assert.Equal(t, 30*time.Second, rateLimitError.RetryAfter)
	assert.Equal(t, arrival.Add(30*time.Second), rateLimitError.RetryAt())
}

func TestRateLimitErrorsDoNotAliasHeaders(t *testing.T) {
	transportHeader := http.Header{
		"X-GitHub-Request-ID":   {"request-rate-limit"},
		"Retry-After":           {"30"},
		"X-RateLimit-Limit":     {"5000"},
		"X-RateLimit-Remaining": {"0"},
		"X-RateLimit-Used":      {"5000"},
	}
	client := newResponseClient(
		http.StatusForbidden,
		transportHeader,
		`{"errors":[{"message":"rate limited"}]}`,
	)

	response, err := client.wireSemantics(t.Context())
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
