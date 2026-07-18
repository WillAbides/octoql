// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package octoql_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
)

func TestPathJSONRoundTrip(t *testing.T) {
	input := []byte(`{
		"type":"SOME_TYPE",
		"message":"lookup failed",
		"path":["repository","issues",2,"title"],
		"locations":[{"line":4,"column":9}]
	}`)
	var graphqlError octoql.Error
	err := json.Unmarshal(input, &graphqlError)
	require.NoError(t, err)

	wantPath := octoql.Path{"repository", "issues", 2, "title"}
	assert.Equal(t, wantPath, graphqlError.Path)
	require.Len(t, graphqlError.Locations, 1)
	assert.Equal(t, octoql.Location{Line: 4, Column: 9}, graphqlError.Locations[0])

	output, err := json.Marshal(graphqlError)
	require.NoError(t, err)
	assert.JSONEq(t, string(input), string(output))
}

func TestPathRejectsInvalidSegments(t *testing.T) {
	tests := []struct {
		path any
		name string
	}{
		{
			name: "marshal boolean",
			path: octoql.Path{"repository", true},
		},
		{
			name: "null",
			path: json.RawMessage(`["repository",null]`),
		},
		{
			name: "boolean",
			path: json.RawMessage(`["repository",true]`),
		},
		{
			name: "fractional number",
			path: json.RawMessage(`["repository",1.5]`),
		},
		{
			name: "exponent",
			path: json.RawMessage(`["repository",1e2]`),
		},
		{
			name: "object",
			path: json.RawMessage(`["repository",{"field":"name"}]`),
		},
		{
			name: "array",
			path: json.RawMessage(`["repository",[1]]`),
		},
		{
			name: "positive overflow",
			path: json.RawMessage(`["repository",999999999999999999999999999999]`),
		},
		{
			name: "negative overflow",
			path: json.RawMessage(`["repository",-999999999999999999999999999999]`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch path := test.path.(type) {
			case octoql.Path:
				_, err := json.Marshal(path)
				require.Error(t, err)
			case json.RawMessage:
				var pathValue octoql.Path
				err := json.Unmarshal(path, &pathValue)
				require.Error(t, err)
			default:
				require.Failf(t, "unexpected test path type", "type = %T", test.path)
			}
		})
	}
}

func TestErrorsFormattingAndInspection(t *testing.T) {
	graphqlErrors := octoql.Errors{
		&octoql.Error{
			Type:    octoql.ErrorType("FORBIDDEN"),
			Message: "owner is unavailable",
			Path:    octoql.Path{"repository", "owner"},
		},
		nil,
		&octoql.Error{Message: "another failure"},
	}

	gotMessage := graphqlErrors.Error()
	wantMessage := "graphql errors: owner is unavailable (path repository.owner); <nil>; another failure"
	assert.Equal(t, wantMessage, gotMessage)

	wrapped := fmt.Errorf("execute query: %w", graphqlErrors)
	var inspectedErrors octoql.Errors
	require.ErrorAs(t, wrapped, &inspectedErrors)
	typedErrors, ok := errors.AsType[octoql.Errors](wrapped)
	require.True(t, ok)
	assert.Len(t, typedErrors, 3)
	typedError, ok := errors.AsType[*octoql.Error](wrapped)
	require.True(t, ok)
	assert.Equal(t, "owner is unavailable", typedError.Message)
}

func TestHTTPErrorInspection(t *testing.T) {
	decodeError := &json.SyntaxError{Offset: 4}
	graphqlErrors := octoql.Errors{&octoql.Error{Message: "request rejected"}}
	httpError := &octoql.HTTPError{
		HTTP:   octoql.HTTPMetadata{StatusCode: http.StatusBadRequest},
		Body:   []byte("bad"),
		Errors: graphqlErrors,
		Cause:  decodeError,
	}
	wrapped := fmt.Errorf("query failed: %w", httpError)

	inspectedHTTPError, ok := errors.AsType[*octoql.HTTPError](wrapped)
	require.True(t, ok)
	assert.Same(t, httpError, inspectedHTTPError)
	_, graphqlErrorsFound := errors.AsType[octoql.Errors](wrapped)
	assert.True(t, graphqlErrorsFound)
	_, decodeErrorFound := errors.AsType[*json.SyntaxError](wrapped)
	assert.True(t, decodeErrorFound)
}
