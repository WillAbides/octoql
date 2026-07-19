package octoql

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitKind identifies the GitHub rate limit that rejected a request.
type RateLimitKind string

const (
	// RateLimitPrimary identifies exhaustion of GitHub's primary rate limit.
	RateLimitPrimary RateLimitKind = "primary"
	// RateLimitSecondary identifies GitHub's secondary rate limit.
	RateLimitSecondary RateLimitKind = "secondary"
)

// RateLimit describes the rate-limit headers returned by GitHub.
//
// Missing or malformed response headers leave their corresponding fields at
// their zero values.
//
//nolint:govet // Preserve the documented public API field order.
type RateLimit struct {
	Limit      int
	Remaining  int
	Used       int
	Reset      time.Time
	Resource   string
	RetryAfter time.Duration
	RetryAt    time.Time
}

type parsedRateLimit struct {
	RateLimit

	remainingValid  bool
	retryAfterValid bool
}

// RateLimitError describes a response rejected because of a GitHub rate limit.
//
//nolint:govet // Preserve the documented public API field order.
type RateLimitError struct {
	Kind      RateLimitKind
	RateLimit RateLimit
	Err       error
}

// Error returns a summary of the rate-limit failure.
func (rateLimitError *RateLimitError) Error() string {
	if rateLimitError == nil {
		return "github rate limit exceeded"
	}
	if rateLimitError.Err == nil {
		return fmt.Sprintf("github %s rate limit exceeded", rateLimitError.Kind)
	}
	return fmt.Sprintf("github %s rate limit exceeded: %v", rateLimitError.Kind, rateLimitError.Err)
}

// Unwrap exposes the response failure and its GraphQL or processing causes.
func (rateLimitError *RateLimitError) Unwrap() error {
	if rateLimitError == nil {
		return nil
	}
	return rateLimitError.Err
}

var rateLimitNow = time.Now

func maxUnixSeconds() uint64 {
	firstUnixSecond := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
	return uint64(math.MaxInt64 + firstUnixSecond)
}

func rateLimitFromHeader(header http.Header, now time.Time) parsedRateLimit {
	rateLimit := parsedRateLimit{}

	limit, valid := nonnegativeHeaderInt(header, "X-RateLimit-Limit")
	if valid {
		rateLimit.Limit = limit
	}
	remaining, valid := nonnegativeHeaderInt(header, "X-RateLimit-Remaining")
	if valid {
		rateLimit.Remaining = remaining
		rateLimit.remainingValid = true
	}
	used, valid := nonnegativeHeaderInt(header, "X-RateLimit-Used")
	if valid {
		rateLimit.Used = used
	}
	reset, valid := nonnegativeHeaderUnix(header, "X-RateLimit-Reset")
	if valid {
		rateLimit.Reset = reset
	}
	rateLimit.Resource = strings.TrimSpace(headerValue(header, "X-RateLimit-Resource"))

	retryAfter, retryAt, valid := retryAfterFromHeader(header, now)
	if valid {
		rateLimit.RetryAfter = retryAfter
		rateLimit.RetryAt = retryAt
		rateLimit.retryAfterValid = true
	}

	return rateLimit
}

func (rateLimit *parsedRateLimit) primarySnapshot() RateLimit {
	snapshot := rateLimit.RateLimit
	snapshot.RetryAfter = 0
	snapshot.RetryAt = time.Time{}
	return snapshot
}

func nonnegativeHeaderInt(header http.Header, name string) (int, bool) {
	value, valid := parseNonnegativeDecimal(headerValue(header, name), strconv.IntSize)
	if !valid {
		return 0, false
	}
	return int(value), true
}

func nonnegativeHeaderUnix(header http.Header, name string) (time.Time, bool) {
	value, valid := parseNonnegativeDecimal(headerValue(header, name), 63)
	if !valid {
		return time.Time{}, false
	}
	if value > maxUnixSeconds() {
		return time.Time{}, false
	}
	return time.Unix(int64(value), 0).UTC(), true
}

func retryAfterFromHeader(header http.Header, now time.Time) (time.Duration, time.Time, bool) {
	value := strings.TrimSpace(headerValue(header, "Retry-After"))
	if value == "" {
		return 0, time.Time{}, false
	}

	seconds, valid := parseNonnegativeDecimal(value, 64)
	if valid {
		maxSeconds := uint64(math.MaxInt64 / int64(time.Second))
		if seconds > maxSeconds {
			return 0, time.Time{}, false
		}
		retryAfter := time.Duration(seconds) * time.Second
		return retryAfter, now.Add(retryAfter), true
	}

	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, time.Time{}, false
	}
	retryAt = retryAt.UTC()
	retryAfter := retryAt.Sub(now)
	if retryAfter < 0 {
		retryAfter = 0
	}
	return retryAfter, retryAt, true
}

func parseNonnegativeDecimal(value string, bitSize int) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, bitSize)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func headerValue(header http.Header, name string) string {
	for headerName, values := range header {
		if strings.EqualFold(headerName, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func classifyRateLimit(statusCode int, rateLimit *parsedRateLimit, err error) error {
	if err == nil {
		return nil
	}

	isSecondaryResponse := isSecondaryRateLimitStatus(statusCode)
	if rateLimit.retryAfterValid && isSecondaryResponse {
		return &RateLimitError{
			Kind:      RateLimitSecondary,
			RateLimit: rateLimit.RateLimit,
			Err:       err,
		}
	}

	isPrimaryStatus := isPrimaryRateLimitStatus(statusCode)
	isPrimaryGraphQLError := hasGraphQLRateLimitError(err)
	hasNoRemaining := rateLimit.remainingValid && rateLimit.Remaining == 0
	hasPrimarySignal := isPrimaryStatus || isPrimaryGraphQLError
	isPrimaryLimit := hasNoRemaining && hasPrimarySignal
	if isPrimaryLimit {
		return &RateLimitError{
			Kind:      RateLimitPrimary,
			RateLimit: rateLimit.RateLimit,
			Err:       err,
		}
	}
	return err
}

func isSecondaryRateLimitStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusOK, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func isPrimaryRateLimitStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func hasGraphQLRateLimitError(err error) bool {
	graphqlErrors, ok := errors.AsType[Errors](err)
	if !ok {
		return false
	}
	for _, graphqlError := range graphqlErrors {
		if graphqlError != nil && graphqlError.Type == ErrorType("RATE_LIMITED") {
			return true
		}
	}
	return false
}
