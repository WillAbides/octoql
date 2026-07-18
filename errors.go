// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package octoql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

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
//
//nolint:govet // Preserve the documented public API field order.
type Error struct {
	Type       ErrorType      `json:"type,omitempty"`
	Message    string         `json:"message"`
	Path       Path           `json:"path,omitempty"`
	Locations  []Location     `json:"locations,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Errors is the list of errors returned in a GraphQL response.
type Errors []*Error

// HTTPError describes a non-2xx GraphQL HTTP response.
//
//nolint:govet // Preserve the public error fields in semantic presentation order.
type HTTPError struct {
	HTTP HTTPMetadata
	// Body is the raw HTTP response payload.
	Body []byte
	// Errors contains any GraphQL errors decoded from Body.
	Errors Errors
	// Cause is the response body read or JSON decode error, when present.
	Cause error
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

// Error returns a stable summary of the failed HTTP response.
func (httpError *HTTPError) Error() string {
	if httpError == nil {
		return "graphql HTTP request failed"
	}

	message := fmt.Sprintf("graphql HTTP request failed with status %d", httpError.HTTP.StatusCode)
	if len(httpError.Errors) > 0 {
		return message + ": " + httpError.Errors.Error()
	}
	if httpError.Cause != nil {
		return message + ": " + httpError.Cause.Error()
	}
	return message
}

// Unwrap exposes decoded GraphQL errors and the read or decode cause to
// [errors.Is], [errors.As], and [errors.AsType].
func (httpError *HTTPError) Unwrap() []error {
	if httpError == nil {
		return nil
	}

	unwrapped := make([]error, 0, 2)
	if len(httpError.Errors) > 0 {
		unwrapped = append(unwrapped, httpError.Errors)
	}
	if httpError.Cause != nil {
		unwrapped = append(unwrapped, httpError.Cause)
	}
	return unwrapped
}

func newHTTPError(
	metadata HTTPMetadata,
	body []byte,
	graphqlErrors Errors,
	cause error,
) *HTTPError {
	return &HTTPError{
		HTTP: HTTPMetadata{
			StatusCode: metadata.StatusCode,
			Header:     cloneHeader(metadata.Header),
			RequestID:  metadata.RequestID,
		},
		Body:   bytes.Clone(body),
		Errors: slices.Clone(graphqlErrors),
		Cause:  cause,
	}
}
