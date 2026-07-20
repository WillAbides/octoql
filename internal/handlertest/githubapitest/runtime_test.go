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

func TestExpectationCountsDefaultsAndFIFO(t *testing.T) {
	t.Run("fifo and exact counts", func(t *testing.T) {
		fake := &fakeTB{}
		handler := newTestHandler(fake)
		variables := EchoPropertyVariables{Value: json.RawMessage(`"fifo"`)}
		handler.ExpectEchoProperty(variables).Respond(EchoPropertyResponse{
			EchoProperty: json.RawMessage(`"first"`),
		})
		handler.ExpectEchoProperty(variables).Respond(EchoPropertyResponse{
			EchoProperty: json.RawMessage(`"second"`),
		})

		first := postOperation(t, handler, "EchoProperty", variables)
		second := postOperation(t, handler, "EchoProperty", variables)
		assert.Contains(t, first.Body.String(), `"first"`)
		assert.Contains(t, second.Body.String(), `"second"`)
		fake.runCleanups()
		assert.Empty(t, fake.errorMessages())
	})

	t.Run("minimum and stub", func(t *testing.T) {
		fake := &fakeTB{}
		handler := newTestHandler(fake)
		required := EchoPropertyVariables{Value: json.RawMessage(`"required"`)}
		stub := EchoPropertyVariables{Value: json.RawMessage(`"stub"`)}
		handler.ExpectEchoProperty(required, MinTimes(2)).Respond(EchoPropertyResponse{})
		handler.ExpectEchoProperty(stub, MinTimes(0)).Respond(EchoPropertyResponse{})

		postOperation(t, handler, "EchoProperty", required)
		fake.runCleanups()
		require.Len(t, fake.errorMessages(), 1)
		assert.Contains(t, fake.errorMessages()[0], "1 call(s) remaining")
	})

	t.Run("default fallback and concrete precedence", func(t *testing.T) {
		fake := &fakeTB{}
		handler := newTestHandler(fake)
		concrete := EchoPropertyVariables{Value: json.RawMessage(`"concrete"`)}
		fallback := EchoPropertyVariables{Value: json.RawMessage(`"fallback"`)}
		handler.DefaultEchoProperty().Handle(func(
			variables EchoPropertyVariables,
			writer http.ResponseWriter,
		) {
			_, err := writer.Write(append([]byte{}, variables.Value...))
			assert.NoError(t, err)
		})
		handler.ExpectEchoProperty(concrete).Respond(EchoPropertyResponse{
			EchoProperty: json.RawMessage(`"expected"`),
		})

		concreteResponse := postOperation(t, handler, "EchoProperty", concrete)
		fallbackResponse := postOperation(t, handler, "EchoProperty", fallback)
		assert.Contains(t, concreteResponse.Body.String(), `"expected"`)
		assert.JSONEq(t, `"fallback"`, fallbackResponse.Body.String())
		fake.runCleanups()
		assert.Empty(t, fake.errorMessages())
	})
}

func TestUnexpectedKnownUnknownAndUnmetExpectations(t *testing.T) {
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
}

func TestResetDisarmsExpectations(t *testing.T) {
	fake := &fakeTB{}
	handler := newTestHandler(fake)
	variables := GetNodeVariables{Id: "reset"}
	handler.ExpectGetNode(variables, Times(2)).Respond(GetNodeResponse{})
	handler.ResetGetNode(variables)

	fake.runCleanups()
	assert.Empty(t, fake.errorMessages())
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
