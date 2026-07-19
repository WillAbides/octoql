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
)

// Client executes GraphQL operations against an HTTP endpoint.
//
// Configure authentication and other request behavior on the supplied
// [http.Client] or its [http.RoundTripper].
type Client struct {
	httpClient *http.Client
	endpoint   string
}

// Operation describes a named GraphQL operation and its query document.
type Operation struct {
	Name  string
	Query string
}

// Response contains the decoded GraphQL response and its HTTP metadata.
// GraphQL responses may contain both Data and Errors.
//
//nolint:govet // Preserve the documented public API field order.
type Response[T any] struct {
	Data       T              `json:"data"`
	Errors     Errors         `json:"errors,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
	HTTP       HTTPMetadata   `json:"-"`
}

// HTTPMetadata describes the HTTP response associated with a GraphQL response.
// Header is a defensive copy and may be safely mutated by the caller.
//
//nolint:govet // Preserve the documented public API field order.
type HTTPMetadata struct {
	StatusCode int
	Header     http.Header
	RequestID  string
	RateLimit  RateLimit
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

// Do executes operation and decodes its response data into T.
//
// Once the server returns an HTTP response, Do always returns a non-nil
// response containing cloned HTTP metadata. A successful HTTP response with
// GraphQL errors returns both the response and [Errors]. A non-2xx response
// returns both the response and an [HTTPError].
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

	var responseCause error
	if sendErr != nil {
		responseCause = fmt.Errorf("send graphql request: %w", sendErr)
	}

	metadata, rateLimit := metadataFromResponse(httpResponse)
	response := &Response[T]{HTTP: metadata}
	if httpResponse.Body == nil {
		err = errors.New("read graphql response: response body is nil")
		err = joinErrors(responseCause, err)
		return finishResponse(response, &metadata, &rateLimit, httpResponse.StatusCode, nil, err)
	}

	body, readErr, closeErr := readAndClose(httpResponse.Body)
	responseCause = joinErrors(responseCause, closeErr)
	if readErr != nil {
		err = joinErrors(responseCause, readErr)
		return finishResponse(response, &metadata, &rateLimit, httpResponse.StatusCode, body, err)
	}

	err = json.Unmarshal(body, response)
	if err != nil {
		err = fmt.Errorf("decode graphql response: %w", err)
		err = joinErrors(responseCause, err)
		return finishResponse(response, &metadata, &rateLimit, httpResponse.StatusCode, body, err)
	}

	if !isSuccessfulStatus(httpResponse.StatusCode) {
		err = newHTTPError(&metadata, body, response.Errors, responseCause)
		return response, classifyRateLimit(httpResponse.StatusCode, &rateLimit, err)
	}
	if responseCause != nil {
		if len(response.Errors) > 0 {
			err = errors.Join(response.Errors, responseCause)
			return response, classifyRateLimit(httpResponse.StatusCode, &rateLimit, err)
		}
		return response, classifyRateLimit(httpResponse.StatusCode, &rateLimit, responseCause)
	}
	if len(response.Errors) > 0 {
		return response, classifyRateLimit(httpResponse.StatusCode, &rateLimit, response.Errors)
	}
	return response, nil
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

func metadataFromResponse(response *http.Response) (HTTPMetadata, parsedRateLimit) {
	header := cloneHeader(response.Header)
	now := rateLimitNow()
	rateLimit := rateLimitFromHeader(header, now)
	metadata := HTTPMetadata{
		StatusCode: response.StatusCode,
		Header:     header,
		RequestID:  requestIDFromHeader(header),
		RateLimit:  rateLimit.RateLimit,
	}
	return metadata, rateLimit
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

func finishResponse[T any](
	response *Response[T],
	metadata *HTTPMetadata,
	rateLimit *parsedRateLimit,
	statusCode int,
	body []byte,
	cause error,
) (*Response[T], error) {
	if !isSuccessfulStatus(statusCode) {
		err := newHTTPError(metadata, body, response.Errors, cause)
		return response, classifyRateLimit(statusCode, rateLimit, err)
	}
	if len(response.Errors) > 0 {
		err := joinErrors(response.Errors, cause)
		return response, classifyRateLimit(statusCode, rateLimit, err)
	}
	return response, classifyRateLimit(statusCode, rateLimit, cause)
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

func cloneHeader(header http.Header) http.Header {
	cloned := header.Clone()
	if cloned == nil {
		return make(http.Header)
	}
	return cloned
}
