package octoql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	endpoint   string

	responseObservation  atomic.Uint64
	rateLimitMu          sync.RWMutex
	rateLimit            RateLimit
	rateLimitObservation uint64
	hasRateLimit         bool
}

// Operation describes a named GraphQL operation and its query document.
type Operation struct {
	Name  string
	Query string
}

// Response contains decoded GraphQL data and top-level extensions.
// GraphQL errors are returned through error.
type Response[T any] struct {
	Data       T              `json:"data"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

type responseEnvelope struct {
	Data       json.RawMessage `json:"data"`
	Errors     json.RawMessage `json:"errors"`
	Extensions json.RawMessage `json:"extensions"`
}

//nolint:govet // Preserve standard GraphQL request field order in encoded JSON.
type requestPayload struct {
	Query         string `json:"query"`
	OperationName string `json:"operationName"`
	Variables     any    `json:"variables,omitempty"`
}

// NewClient returns a client for endpoint. A nil httpClient uses
// [http.DefaultClient]. Endpoint validation occurs when [Do] executes an
// operation.
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
	return client.rateLimit, client.hasRateLimit
}

// Do executes operation and decodes its response into T.
//
// Once the server returns an HTTP response, Do always returns a non-nil data
// envelope. A response failure returns [ResponseError]. GraphQL errors and rate
// limits remain discoverable in that error chain as [Errors] and
// [RateLimitError]. Failures before an HTTP response return nil.
func Do[T any](
	ctx context.Context,
	client *Client,
	operation Operation,
	variables any,
) (*Response[T], error) {
	err := validateRequest(ctx, client, operation)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(requestPayload{
		Query:         operation.Query,
		OperationName: operation.Name,
		Variables:     variables,
	})
	if err != nil {
		return nil, fmt.Errorf("encode graphql request: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpoint,
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("create graphql request: %w", err)
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
			return nil, errors.New("send graphql request: HTTP client returned no response")
		}
		return nil, fmt.Errorf("send graphql request: %w", sendErr)
	}

	observation := client.responseObservation.Add(1)
	rateLimit := rateLimitFromHeader(httpResponse.Header, rateLimitNow())
	client.observeRateLimit(observation, rateLimit)

	response := &Response[T]{}
	statusCode := httpResponse.StatusCode
	requestID := requestIDFromHeader(httpResponse.Header)
	if sendErr != nil {
		cause := fmt.Errorf("send graphql request: %w", sendErr)
		responseError := newResponseError(statusCode, requestID, nil, false, cause)
		return response, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	if httpResponse.Body == nil {
		cause := errors.New("read graphql response: response body is nil")
		responseError := newResponseError(statusCode, requestID, nil, false, cause)
		return response, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	body, readErr, closeErr := readAndClose(httpResponse.Body)
	if readErr != nil {
		cause := joinErrors(readErr, closeErr)
		responseError := newResponseError(statusCode, requestID, body, true, cause)
		return response, classifyRateLimit(statusCode, &rateLimit, responseError)
	}

	var graphqlErrors Errors
	var hasData bool
	var hasErrors bool
	var decodeErr error
	response, graphqlErrors, hasData, hasErrors, decodeErr = decodeResponse[T](body)

	cause := decodeErr
	if len(graphqlErrors) > 0 {
		cause = joinErrors(graphqlErrors, cause)
	}
	cause = joinErrors(cause, closeErr)

	isSuccessful := isSuccessfulStatus(statusCode)
	hasPayload := hasData || hasErrors
	missingPayload := decodeErr == nil && !hasPayload
	if isSuccessful && missingPayload {
		payloadErr := errors.New("decode graphql response: response contains neither data nor errors")
		cause = joinErrors(cause, payloadErr)
	}

	if isSuccessful && cause == nil {
		return response, nil
	}

	hasDecodeFailure := decodeErr != nil || missingPayload
	retainBody := !isSuccessful || hasDecodeFailure
	responseError := newResponseError(statusCode, requestID, body, retainBody, cause)
	return response, classifyRateLimit(statusCode, &rateLimit, responseError)
}

// ResponseData is a generated-code contract that converts the low-level
// response envelope into a concrete data return.
func ResponseData[T any](response *Response[T], err error) (*T, error) {
	if response == nil {
		return nil, err
	}
	return &response.Data, err
}

func (client *Client) observeRateLimit(observation uint64, parsed parsedRateLimit) {
	if client == nil || !parsed.remainingValid {
		return
	}

	rateLimit := parsed.primarySnapshot()
	client.rateLimitMu.Lock()
	defer client.rateLimitMu.Unlock()

	hasNewerObservation := client.hasRateLimit &&
		client.rateLimitObservation >= observation
	if hasNewerObservation {
		return
	}
	client.rateLimit = rateLimit
	client.rateLimitObservation = observation
	client.hasRateLimit = true
}

func validateRequest(ctx context.Context, client *Client, operation Operation) error {
	if client == nil {
		return errors.New("octoql: client is nil")
	}
	if ctx == nil {
		return errors.New("octoql: context is nil")
	}

	endpoint := strings.TrimSpace(client.endpoint)
	if endpoint == "" {
		return errors.New("octoql: endpoint is empty")
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("octoql: invalid endpoint %q: %w", client.endpoint, err)
	}
	if parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
		return fmt.Errorf("octoql: endpoint %q must use http or https", client.endpoint)
	}
	if parsedEndpoint.Host == "" {
		return fmt.Errorf("octoql: endpoint %q has no host", client.endpoint)
	}

	if strings.TrimSpace(operation.Name) == "" {
		return errors.New("octoql: operation name is empty")
	}
	if !isGraphQLName(operation.Name) {
		return fmt.Errorf("octoql: operation name %q is invalid", operation.Name)
	}
	if strings.TrimSpace(operation.Query) == "" {
		return errors.New("octoql: operation query is empty")
	}
	return nil
}

func isGraphQLName(name string) bool {
	for index, char := range name {
		isLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		if char != '_' && !isLetter && (index == 0 || !isDigit) {
			return false
		}
	}
	return name != ""
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

func decodeResponse[T any](
	body []byte,
) (*Response[T], Errors, bool, bool, error) {
	response := &Response[T]{}
	var envelope responseEnvelope
	err := json.Unmarshal(body, &envelope)
	if err != nil {
		return response, nil, false, false, fmt.Errorf(
			"decode graphql response envelope: %w",
			err,
		)
	}

	hasData := envelope.Data != nil
	hasErrorsField := envelope.Errors != nil

	var graphqlErrors Errors
	var errorsErr error
	if hasErrorsField {
		errorsErr = json.Unmarshal(envelope.Errors, &graphqlErrors)
		if errorsErr != nil {
			errorsErr = fmt.Errorf("decode graphql response errors: %w", errorsErr)
		}
	}
	hasErrors := len(graphqlErrors) > 0

	var dataErr error
	if hasData {
		decoded := new(T)
		dataErr = json.Unmarshal(envelope.Data, decoded)
		if dataErr == nil {
			response.Data = *decoded
		}
		if dataErr != nil {
			dataErr = fmt.Errorf("decode graphql response data: %w", dataErr)
		}
	}

	var extensionsErr error
	if envelope.Extensions != nil {
		extensionsErr = json.Unmarshal(envelope.Extensions, &response.Extensions)
		if extensionsErr != nil {
			extensionsErr = fmt.Errorf(
				"decode graphql response extensions: %w",
				extensionsErr,
			)
		}
	}

	decodeErr := joinErrors(errorsErr, dataErr)
	decodeErr = joinErrors(decodeErr, extensionsErr)
	return response, graphqlErrors, hasData, hasErrors, decodeErr
}

func isSuccessfulStatus(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func joinErrors(left, right error) error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return errors.Join(left, right)
}
