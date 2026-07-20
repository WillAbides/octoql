package octoql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const maxResponseErrorRawBody = 64 * 1024

// ErrorType identifies a GitHub GraphQL error category. It is an open string
// type so values introduced by GitHub remain available to callers.
type ErrorType string

// Path is a GraphQL response path. Each segment is either a string field name
// or an integer list index.
type Path []any

// Location identifies a line and column in a GraphQL document.
type Location struct {
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// Error describes an error returned in a GraphQL response.
type Error struct {
	Type       ErrorType      `json:"type,omitempty"`
	Message    string         `json:"message"`
	Path       Path           `json:"path,omitempty"`
	Locations  []Location     `json:"locations,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Errors is the list of errors returned in a GraphQL response.
type Errors []*Error

// ResponseError describes a failed GraphQL HTTP response.
type ResponseError struct {
	// StatusCode is the HTTP response status.
	StatusCode int
	// RequestID is GitHub's X-GitHub-Request-ID value, when present.
	RequestID string
	// RawBody contains at most the first 64 KiB of a non-successful or
	// undecodable response. It is omitted for ordinary GraphQL errors.
	RawBody []byte
	// RawBodyTruncated reports whether RawBody omits trailing response bytes.
	RawBodyTruncated bool

	err error
}

// MarshalJSON encodes string and integer path segments in GraphQL wire format.
func (path Path) MarshalJSON() ([]byte, error) {
	if path == nil {
		return []byte("null"), nil
	}

	segments := make([]json.RawMessage, len(path))
	for index, segment := range path {
		var encoded []byte
		var err error
		switch value := segment.(type) {
		case string:
			encoded, err = json.Marshal(value)
		case int:
			encoded, err = json.Marshal(value)
		default:
			return nil, fmt.Errorf(
				"encode graphql path segment %d: expected string or int, got %T",
				index,
				segment,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("encode graphql path segment %d: %w", index, err)
		}
		segments[index] = encoded
	}

	encoded, err := json.Marshal(segments)
	if err != nil {
		return nil, fmt.Errorf("encode graphql path: %w", err)
	}
	return encoded, nil
}

// UnmarshalJSON decodes GraphQL string and integer path segments.
func (path *Path) UnmarshalJSON(data []byte) error {
	if path == nil {
		return errors.New("decode graphql path: nil destination")
	}

	var rawSegments []json.RawMessage
	err := json.Unmarshal(data, &rawSegments)
	if err != nil {
		return fmt.Errorf("decode graphql path: %w", err)
	}
	if rawSegments == nil {
		*path = nil
		return nil
	}

	segments := make(Path, len(rawSegments))
	for index, rawSegment := range rawSegments {
		trimmed := bytes.TrimSpace(rawSegment)
		if len(trimmed) > 0 && trimmed[0] == '"' {
			var field string
			err = json.Unmarshal(trimmed, &field)
			if err != nil {
				return fmt.Errorf("decode graphql path segment %d: %w", index, err)
			}
			segments[index] = field
			continue
		}

		var listIndex int
		var parsed int64
		parsed, err = strconv.ParseInt(string(trimmed), 10, strconv.IntSize)
		if err != nil {
			return fmt.Errorf(
				"decode graphql path segment %d: expected string or integer: %w",
				index,
				err,
			)
		}
		listIndex = int(parsed)
		segments[index] = listIndex
	}

	*path = segments
	return nil
}

// String formats a path using dotted fields and bracketed list indexes.
func (path Path) String() string {
	var result strings.Builder
	for _, segment := range path {
		switch value := segment.(type) {
		case string:
			if result.Len() > 0 {
				result.WriteByte('.')
			}
			result.WriteString(value)
		case int:
			result.WriteByte('[')
			result.WriteString(strconv.Itoa(value))
			result.WriteByte(']')
		default:
			if result.Len() > 0 {
				result.WriteByte('.')
			}
			result.WriteString("<invalid>")
		}
	}
	return result.String()
}

// Error returns the GraphQL error message and response path.
func (graphqlError *Error) Error() string {
	if graphqlError == nil {
		return "<nil>"
	}

	message := graphqlError.Message
	if message == "" {
		message = "graphql error"
	}
	path := graphqlError.Path.String()
	if path == "" {
		return message
	}
	return fmt.Sprintf("%s (path %s)", message, path)
}

// Error returns a stable summary of all GraphQL errors.
func (graphqlErrors Errors) Error() string {
	if len(graphqlErrors) == 0 {
		return "graphql request failed"
	}

	messages := make([]string, 0, len(graphqlErrors))
	for _, graphqlError := range graphqlErrors {
		messages = append(messages, graphqlError.Error())
	}
	if len(messages) == 1 {
		return "graphql error: " + messages[0]
	}
	return "graphql errors: " + strings.Join(messages, "; ")
}

// Unwrap exposes individual GraphQL errors to [errors.Is], [errors.As], and
// [errors.AsType].
func (graphqlErrors Errors) Unwrap() []error {
	unwrapped := make([]error, 0, len(graphqlErrors))
	for _, graphqlError := range graphqlErrors {
		if graphqlError != nil {
			unwrapped = append(unwrapped, graphqlError)
		}
	}
	return unwrapped
}

// Error returns a stable summary of the failed response.
func (responseError *ResponseError) Error() string {
	if responseError == nil {
		return "graphql response failed"
	}

	message := "graphql response failed"
	if !isSuccessfulStatus(responseError.StatusCode) {
		message = fmt.Sprintf(
			"graphql response failed with status %d",
			responseError.StatusCode,
		)
	}
	if responseError.err != nil {
		return message + ": " + responseError.err.Error()
	}
	return message
}

// Unwrap exposes decoded GraphQL errors and response processing failures to
// [errors.Is], [errors.As], and [errors.AsType].
func (responseError *ResponseError) Unwrap() error {
	if responseError == nil {
		return nil
	}
	return responseError.err
}

func newResponseError(
	statusCode int,
	requestID string,
	body []byte,
	retainBody bool,
	err error,
) *ResponseError {
	var rawBody []byte
	var isTruncated bool
	if retainBody {
		rawBody = body
		if len(rawBody) > maxResponseErrorRawBody {
			rawBody = rawBody[:maxResponseErrorRawBody]
			isTruncated = true
		}
		rawBody = bytes.Clone(rawBody)
	}

	return &ResponseError{
		StatusCode:       statusCode,
		RequestID:        requestID,
		RawBody:          rawBody,
		RawBodyTruncated: isTruncated,
		err:              err,
	}
}

type partialDataError[T any] struct {
	data T
	err  error
}

func (e *partialDataError[T]) Error() string {
	return e.err.Error()
}

func (e *partialDataError[T]) Unwrap() error {
	return e.err
}

func (e *partialDataError[T]) partialData() any {
	return e.data
}

type partialDataCarrier interface {
	error
	partialData() any
}

// IsPartialDataError reports whether err contains partial GraphQL data.
func IsPartialDataError(err error) bool {
	_, ok := errors.AsType[partialDataCarrier](err)
	return ok
}

// GetPartialData extracts partial GraphQL data into dest.
//
// It returns false when err does not contain partial data. It panics when dest
// is nil or has a different type from the stored partial data.
func GetPartialData[T any](err error, dest *T) bool {
	if dest == nil {
		panic("octoql: partial data destination is nil")
	}
	carrier, ok := errors.AsType[partialDataCarrier](err)
	if !ok {
		return false
	}
	data, ok := carrier.partialData().(T)
	if !ok {
		panic(fmt.Sprintf(
			"partial data destination has type %T, partial data has type %T",
			dest,
			carrier.partialData(),
		))
	}
	*dest = data
	return true
}

// NewPartialDataError returns an error carrying partial GraphQL data.
//
// This is a generated-code contract.
func NewPartialDataError[T any](data T, err error) error {
	if err == nil {
		panic("NewPartialDataError called with nil error")
	}
	return &partialDataError[T]{
		data: data,
		err:  err,
	}
}
