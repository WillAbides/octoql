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

// DefaultResponseSizeLimit is the maximum GraphQL HTTP response size buffered
// and decoded by a Client unless configured otherwise. It is 10 MiB.
const DefaultResponseSizeLimit int64 = 10 * 1024 * 1024

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
	responseSizeLimit    atomic.Int64
	rateLimitMu          sync.RWMutex
	rateLimitObservation uint64
}

// NewClient returns a client for endpoint. A nil httpClient uses
// [http.DefaultClient].
func NewClient(endpoint string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	client := &Client{
		endpoint:   endpoint,
		httpClient: httpClient,
	}
	client.responseSizeLimit.Store(DefaultResponseSizeLimit)
	return client
}

// ResponseSizeLimit returns the maximum HTTP response size Client buffers and
// decodes. A newly constructed Client uses [DefaultResponseSizeLimit].
func (c *Client) ResponseSizeLimit() int64 {
	if c == nil {
		return 0
	}

	limit := c.responseSizeLimit.Load()
	if limit > 0 {
		return limit
	}
	return DefaultResponseSizeLimit
}

// SetResponseSizeLimit configures the maximum HTTP response size Client buffers
// and decodes. limit must be greater than zero. It may be called concurrently
// with [Client.Execute].
func (c *Client) SetResponseSizeLimit(limit int64) error {
	if c == nil {
		return errors.New("octoql: client is nil")
	}
	if limit <= 0 {
		return errors.New("octoql: response size limit must be greater than zero")
	}

	c.responseSizeLimit.Store(limit)
	return nil
}

// RateLimit returns the latest valid primary rate-limit snapshot observed by
// client. The boolean is false until a response includes a valid
// X-RateLimit-Remaining header.
//
// The snapshot is advisory: other clients and processes can consume the same
// GitHub rate-limit budget after it is observed.
func (c *Client) RateLimit() (RateLimit, bool) {
	if c == nil {
		return RateLimit{}, false
	}

	c.rateLimitMu.RLock()
	defer c.rateLimitMu.RUnlock()
	if c.rateLimit == nil {
		return RateLimit{}, false
	}
	return *c.rateLimit, true
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
func (c *Client) Execute(ctx context.Context, payload Payload, response any) (bool, error) {
	if c == nil {
		return false, errors.New("octoql: client is nil")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode graphql request: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return false, fmt.Errorf("create graphql request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	httpClient := c.httpClient
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

	observation := c.responseObservation.Add(1)
	rateLimit := rateLimitFromHeader(httpResponse.Header, rateLimitNow())
	c.observeRateLimit(observation, &rateLimit)

	statusCode := httpResponse.StatusCode
	requestID := requestIDFromHeader(httpResponse.Header)
	if sendErr != nil {
		cause := fmt.Errorf("send graphql request: %w", sendErr)
		responseError := newResponseError(responseErrorParams{
			statusCode: statusCode,
			requestID:  requestID,
			err:        cause,
		})
		return false, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	if httpResponse.Body == nil {
		cause := errors.New("read graphql response: response body is nil")
		responseError := newResponseError(responseErrorParams{
			statusCode: statusCode,
			requestID:  requestID,
			err:        cause,
		})
		return false, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	body, readErr, closeErr := readAndClose(
		httpResponse.Body,
		c.ResponseSizeLimit(),
	)
	if readErr != nil {
		cause := errors.Join(readErr, closeErr)
		_, responseTooLarge := errors.AsType[*ResponseSizeLimitError](readErr)
		responseError := newResponseError(responseErrorParams{
			statusCode:    statusCode,
			requestID:     requestID,
			body:          body,
			retainBody:    true,
			bodyTruncated: responseTooLarge,
			err:           cause,
		})
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
	responseError := newResponseError(responseErrorParams{
		statusCode: statusCode,
		requestID:  requestID,
		body:       body,
		retainBody: retainBody,
		err:        cause,
	})
	return hasUsableData, classifyRateLimit(statusCode, &rateLimit, responseError)
}

func (c *Client) observeRateLimit(observation uint64, parsed *parsedRateLimit) {
	if c == nil || parsed == nil || !parsed.remainingValid {
		return
	}

	rateLimit := parsed.primarySnapshot()
	c.rateLimitMu.Lock()
	defer c.rateLimitMu.Unlock()

	hasNewerObservation := c.rateLimit != nil &&
		c.rateLimitObservation >= observation
	if hasNewerObservation {
		return
	}
	c.rateLimit = &rateLimit
	c.rateLimitObservation = observation
}

func requestIDFromHeader(header http.Header) string {
	for name, values := range header {
		if strings.EqualFold(name, "X-GitHub-Request-ID") && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func readAndClose(body io.ReadCloser, limit int64) ([]byte, error, error) {
	payload, readErr := io.ReadAll(io.LimitReader(body, limit))
	if readErr == nil {
		var excess [1]byte
		count, excessErr := io.ReadFull(body, excess[:])
		switch {
		case count > 0:
			readErr = &ResponseSizeLimitError{Limit: limit}
		case excessErr != nil && !errors.Is(excessErr, io.EOF):
			readErr = excessErr
		}
	}
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
