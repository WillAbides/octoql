package octoql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Client executes GraphQL operations against an HTTP endpoint.
//
// Configure authentication and other request behavior on the supplied
// [http.Client] or its [http.RoundTripper]. A Client may be used concurrently
// after construction.
type Client struct {
	httpClient *http.Client
	rateLimit  *RateLimit
	endpoint   string

	responseObservation  atomic.Uint64
	rateLimitMu          sync.RWMutex
	rateLimitObservation uint64
}

// NewClient returns a client for endpoint. A nil httpClient uses
// [http.DefaultClient].
func NewClient(endpoint string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		endpoint:   endpoint,
		httpClient: httpClient,
	}
}

// RateLimit returns the latest valid primary rate-limit snapshot observed by
// client. The boolean is false until a response includes a valid
// X-RateLimit-Remaining header.
//
// The snapshot is advisory: other clients and processes can consume the same
// GitHub rate-limit budget after it is observed.
func (client *Client) RateLimit() (RateLimit, bool) {
	if client == nil {
		return RateLimit{}, false
	}

	client.rateLimitMu.RLock()
	defer client.rateLimitMu.RUnlock()
	if client.rateLimit == nil {
		return RateLimit{}, false
	}
	return *client.rateLimit, true
}

// Payload is the GraphQL request body used by generated clients.
type Payload struct {
	Query         string `json:"query"`
	OperationName string `json:"operationName"`
	Variables     any    `json:"variables,omitempty"`
}

// Execute runs operation and decodes its response data into response.
//
// Execute is a generated-code contract. Response must be a non-nil pointer.
// The returned boolean reports whether the GraphQL data field decoded
// successfully and response is usable, including when GraphQL errors are also
// returned.
// Every failure after the server returns an HTTP response includes
// [ResponseError]. GraphQL errors and rate limits remain discoverable in that
// error chain as [Errors] and [RateLimitError].
func (client *Client) Execute(ctx context.Context, payload Payload, response any) (bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode graphql request: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return false, fmt.Errorf("create graphql request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	httpClient := client.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	//nolint:bodyclose // readAndClose closes the body and preserves close errors.
	httpResponse, sendErr := httpClient.Do(request)
	if httpResponse == nil {
		if sendErr == nil {
			return false, errors.New("send graphql request: HTTP client returned no response")
		}
		return false, fmt.Errorf("send graphql request: %w", sendErr)
	}

	observation := client.responseObservation.Add(1)
	rateLimit := rateLimitFromHeader(httpResponse.Header, rateLimitNow())
	client.observeRateLimit(observation, &rateLimit)

	statusCode := httpResponse.StatusCode
	requestID := requestIDFromHeader(httpResponse.Header)
	if sendErr != nil {
		cause := fmt.Errorf("send graphql request: %w", sendErr)
		responseError := newResponseError(statusCode, requestID, nil, false, cause)
		return false, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	if httpResponse.Body == nil {
		cause := errors.New("read graphql response: response body is nil")
		responseError := newResponseError(statusCode, requestID, nil, false, cause)
		return false, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	body, readErr, closeErr := readAndClose(httpResponse.Body)
	if readErr != nil {
		cause := errors.Join(readErr, closeErr)
		responseError := newResponseError(statusCode, requestID, body, true, cause)
		return false, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	data, graphqlErrors, decodeErr := decodeResponse(body)

	hasData := data != nil && !bytes.Equal(bytes.TrimSpace(data), []byte("null"))
	hasErrors := len(graphqlErrors) > 0
	hasUsableData := false
	if hasData {
		dataErr := decodeData(data, response)
		hasUsableData = dataErr == nil
		decodeErr = errors.Join(decodeErr, dataErr)
	}

	cause := decodeErr
	if len(graphqlErrors) > 0 {
		var left error = graphqlErrors
		cause = errors.Join(left, cause)
	}
	cause = errors.Join(cause, closeErr)

	isSuccessful := isSuccessfulStatus(statusCode)
	hasPayload := hasData || hasErrors
	missingPayload := decodeErr == nil && !hasPayload
	if isSuccessful && missingPayload {
		payloadErr := errors.New("decode graphql response: response contains neither data nor errors")
		cause = errors.Join(cause, payloadErr)
	}

	if isSuccessful && cause == nil {
		return hasUsableData, nil
	}

	hasDecodeFailure := decodeErr != nil || missingPayload
	retainBody := !isSuccessful || hasDecodeFailure
	responseError := newResponseError(statusCode, requestID, body, retainBody, cause)
	return hasUsableData, classifyRateLimit(statusCode, &rateLimit, responseError)
}

func (client *Client) observeRateLimit(observation uint64, parsed *parsedRateLimit) {
	if client == nil || parsed == nil || !parsed.remainingValid {
		return
	}

	rateLimit := parsed.primarySnapshot()
	client.rateLimitMu.Lock()
	defer client.rateLimitMu.Unlock()

	hasNewerObservation := client.rateLimit != nil &&
		client.rateLimitObservation >= observation
	if hasNewerObservation {
		return
	}
	client.rateLimit = &rateLimit
	client.rateLimitObservation = observation
}

func requestIDFromHeader(header http.Header) string {
	for name, values := range header {
		if strings.EqualFold(name, "X-GitHub-Request-ID") && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func readAndClose(body io.ReadCloser) ([]byte, error, error) {
	payload, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil {
		readErr = fmt.Errorf("read graphql response: %w", readErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close graphql response: %w", closeErr)
	}
	return payload, readErr, closeErr
}

func decodeResponse(body []byte) (json.RawMessage, Errors, error) {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	err := json.Unmarshal(body, &envelope)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"decode graphql response envelope: %w",
			err,
		)
	}

	var graphqlErrors Errors
	if envelope.Errors == nil {
		return envelope.Data, graphqlErrors, nil
	}

	err = json.Unmarshal(envelope.Errors, &graphqlErrors)
	if err != nil {
		err = fmt.Errorf("decode graphql response errors: %w", err)
	}
	return envelope.Data, graphqlErrors, err
}

func decodeData(data json.RawMessage, response any) error {
	err := json.Unmarshal(data, response)
	if err != nil {
		return fmt.Errorf("decode graphql response data: %w", err)
	}
	return nil
}

func isSuccessfulStatus(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
