// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package octoql_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

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
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	wantPath := octoql.Path{"repository", "issues", 2, "title"}
	if !pathsEqual(graphqlError.Path, wantPath) {
		t.Errorf("path = %#v, want %#v", graphqlError.Path, wantPath)
	}
	if len(graphqlError.Locations) != 1 {
		t.Fatalf("len(locations) = %d, want 1", len(graphqlError.Locations))
	}
	if graphqlError.Locations[0] != (octoql.Location{Line: 4, Column: 9}) {
		t.Errorf("location = %#v, want line 4 column 9", graphqlError.Locations[0])
	}

	output, err := json.Marshal(graphqlError)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !jsonEqual(output, input) {
		t.Errorf("round-trip JSON = %s, want %s", output, input)
	}
}

func TestPathRejectsInvalidSegments(t *testing.T) {
	tests := []struct {
		path any
		name string
	}{
		{
			name: "boolean",
			path: octoql.Path{"repository", true},
		},
		{
			name: "fractional number",
			path: json.RawMessage(`["repository",1.5]`),
		},
		{
			name: "object",
			path: json.RawMessage(`["repository",{"field":"name"}]`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch path := test.path.(type) {
			case octoql.Path:
				_, err := json.Marshal(path)
				if err == nil {
					t.Fatal("json.Marshal() error = nil, want invalid path segment error")
				}
			case json.RawMessage:
				var pathValue octoql.Path
				err := json.Unmarshal(path, &pathValue)
				if err == nil {
					t.Fatal("json.Unmarshal() error = nil, want invalid path segment error")
				}
			default:
				t.Fatalf("unexpected test path type %T", test.path)
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
	if gotMessage != wantMessage {
		t.Errorf("Errors.Error() = %q, want %q", gotMessage, wantMessage)
	}

	wrapped := fmt.Errorf("execute query: %w", graphqlErrors)
	var inspectedErrors octoql.Errors
	if !errors.As(wrapped, &inspectedErrors) {
		t.Fatal("errors.As(..., *octoql.Errors) = false, want true")
	}
	typedErrors, ok := errors.AsType[octoql.Errors](wrapped)
	if !ok {
		t.Fatal("errors.AsType[octoql.Errors]() = false, want true")
	}
	if len(typedErrors) != 3 {
		t.Errorf("len(errors.AsType result) = %d, want 3", len(typedErrors))
	}
	typedError, ok := errors.AsType[*octoql.Error](wrapped)
	if !ok {
		t.Fatal("errors.AsType[*octoql.Error]() = false, want true")
	}
	if typedError.Message != "owner is unavailable" {
		t.Errorf("inspected GraphQL error message = %q, want %q", typedError.Message, "owner is unavailable")
	}
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
	if !ok {
		t.Fatal("errors.AsType[*octoql.HTTPError]() = false, want true")
	}
	if inspectedHTTPError != httpError {
		t.Error("errors.AsType[*octoql.HTTPError]() returned a different pointer")
	}
	_, graphqlErrorsFound := errors.AsType[octoql.Errors](wrapped)
	if !graphqlErrorsFound {
		t.Fatal("errors.AsType[octoql.Errors]() = false, want true")
	}
	_, decodeErrorFound := errors.AsType[*json.SyntaxError](wrapped)
	if !decodeErrorFound {
		t.Fatal("errors.AsType[*json.SyntaxError]() = false, want true")
	}
}
