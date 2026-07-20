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
	// RawBody contains at most the first 64 KiB of a non-successful, over-limit,
	// or undecodable response. It is omitted for ordinary GraphQL errors.
	RawBody []byte
	// RawBodyTruncated reports whether RawBody omits trailing response bytes.
	RawBodyTruncated bool

	err error
}

// ResponseSizeLimitError reports that a GraphQL HTTP response exceeded Client's
// configured response-size limit.
type ResponseSizeLimitError struct {
	// Limit is the configured maximum response size in bytes.
	Limit int64
}

// Error reports that the response exceeded its configured limit.
func (e *ResponseSizeLimitError) Error() string {
	if e == nil {
		return "graphql response exceeds its size limit"
	}
	return fmt.Sprintf("graphql response exceeds %d-byte limit", e.Limit)
}

// MarshalJSON encodes string and integer path segments in GraphQL wire format.
func (p Path) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}

	segments := make([]json.RawMessage, len(p))
	for index, segment := range p {
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
func (p *Path) UnmarshalJSON(data []byte) error {
	if p == nil {
		return errors.New("decode graphql path: nil destination")
	}

	var rawSegments []json.RawMessage
	err := json.Unmarshal(data, &rawSegments)
	if err != nil {
		return fmt.Errorf("decode graphql path: %w", err)
	}
	if rawSegments == nil {
		*p = nil
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

	*p = segments
	return nil
}

// String formats a path using dotted fields and bracketed list indexes.
func (p Path) String() string {
	var result strings.Builder
	for _, segment := range p {
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
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	message := e.Message
	if message == "" {
		message = "graphql error"
	}
	path := e.Path.String()
	if path == "" {
		return message
	}
	return fmt.Sprintf("%s (path %s)", message, path)
}

// Error returns a stable summary of all GraphQL errors.
func (e Errors) Error() string {
	if len(e) == 0 {
		return "graphql request failed"
	}

	messages := make([]string, 0, len(e))
	for _, graphqlError := range e {
		messages = append(messages, graphqlError.Error())
	}
	if len(messages) == 1 {
		return "graphql error: " + messages[0]
	}
	return "graphql errors: " + strings.Join(messages, "; ")
}

// Unwrap exposes individual GraphQL errors to [errors.Is], [errors.As], and
// [errors.AsType].
func (e Errors) Unwrap() []error {
	unwrapped := make([]error, 0, len(e))
	for _, graphqlError := range e {
		if graphqlError != nil {
			unwrapped = append(unwrapped, graphqlError)
		}
	}
	return unwrapped
}

// Error returns a stable summary of the failed response.
func (e *ResponseError) Error() string {
	if e == nil {
		return "graphql response failed"
	}

	message := "graphql response failed"
	if !isSuccessfulStatus(e.StatusCode) {
		message = fmt.Sprintf(
			"graphql response failed with status %d",
			e.StatusCode,
		)
	}
	if e.err != nil {
		return message + ": " + e.err.Error()
	}
	return message
}

// Unwrap exposes decoded GraphQL errors and response processing failures to
// [errors.Is], [errors.As], and [errors.AsType].
func (e *ResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type responseErrorParams struct {
	statusCode    int
	requestID     string
	body          []byte
	retainBody    bool
	bodyTruncated bool
	err           error
}

func newResponseError(params responseErrorParams) *ResponseError {
	var rawBody []byte
	isTruncated := false
	if params.retainBody {
		rawBody = params.body
		isTruncated = params.bodyTruncated
		if len(rawBody) > maxResponseErrorRawBody {
			rawBody = rawBody[:maxResponseErrorRawBody]
			isTruncated = true
		}
		rawBody = bytes.Clone(rawBody)
	}

	return &ResponseError{
		StatusCode:       params.statusCode,
		RequestID:        params.requestID,
		RawBody:          rawBody,
		RawBodyTruncated: isTruncated,
		err:              params.err,
	}
}
