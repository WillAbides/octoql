package githubapitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTB struct {
	cleanups []func()
	errors   []string
	mu       sync.Mutex
}

func (f *fakeTB) Cleanup(cleanup func()) {
	f.mu.Lock()
	f.cleanups = append(f.cleanups, cleanup)
	f.mu.Unlock()
}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}

func (f *fakeTB) runCleanups() {
	f.mu.Lock()
	cleanups := slices.Clone(f.cleanups)
	f.mu.Unlock()
	for _, cleanup := range slices.Backward(cleanups) {
		cleanup()
	}
}

func (f *fakeTB) errorMessages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.errors...)
}

func TestCleanupAndUnexpectedOperationFailures(t *testing.T) {
	t.Run("unmet minimum count", func(t *testing.T) {
		fake := &fakeTB{}
		handler := newTestHandler(fake)
		required := EchoPropertyVariables{Value: json.RawMessage(`"required"`)}
		handler.ExpectEchoProperty(required, MinTimes(2)).Respond(EchoPropertyResponse{})

		postOperation(t, handler, "EchoProperty", required)
		fake.runCleanups()
		require.Len(t, fake.errorMessages(), 1)
		assert.Contains(t, fake.errorMessages()[0], "1 call(s) remaining")
	})

	t.Run("unexpected variables unknown operation and unmet expectation", func(t *testing.T) {
		fake := &fakeTB{}
		handler := newTestHandler(fake)
		handler.ExpectGetNode(GetNodeVariables{Id: "expected"}).Respond(GetNodeResponse{})

		known := postOperation(t, handler, "GetNode", GetNodeVariables{Id: "different"})
		assert.Contains(t, known.Body.String(), "no expectation found")
		require.Len(t, fake.errorMessages(), 1)

		unknown := postOperation(t, handler, "UnknownOperation", map[string]any{})
		assert.Contains(t, unknown.Body.String(), "unknown operation")
		assert.Len(t, fake.errorMessages(), 1)

		fake.runCleanups()
		require.Len(t, fake.errorMessages(), 2)
		assert.Contains(t, fake.errorMessages()[1], "unmet GetNode expectation")
	})
}

func TestWithPrimaryRateLimitRejectsNegativeValues(t *testing.T) {
	tests := map[string]RateLimit{
		"negative limit": {
			Limit: -1,
		},
		"negative remaining": {
			Remaining: -1,
		},
		"negative used": {
			Used: -1,
		},
	}

	for name, rateLimit := range tests {
		t.Run(name, func(t *testing.T) {
			options := buildResponseOptions(WithPrimaryRateLimit(rateLimit))
			require.EqualError(
				t,
				options.err,
				"primary rate limit values must not be negative",
			)
		})
	}
}

func postOperation(
	t *testing.T,
	handler http.Handler,
	operationName string,
	variables any,
) *httptest.ResponseRecorder {
	requestBody, err := json.Marshal(map[string]any{
		"operationName": operationName,
		"variables":     variables,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(
		http.MethodPost,
		"https://api.github.example/graphql",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if strings.TrimSpace(recorder.Body.String()) == "" {
		t.Fatal("handler returned an empty response")
	}
	return recorder
}
