# Testing generated clients with typed handlers

Add `test_handler.generated` to `octoqlgen.yaml` when tests need a typed GraphQL
server:

```yaml
generated: internal/githubapi/generated.go
test_handler:
  generated: internal/githubapitest/generated.go
```

`octoqlgen generate --config octoqlgen.yaml` loads the config once, verifies or
materializes the schema once, and parses and validates the operation documents
once. The client and handler render from that shared operation plan before any
output is written. Omitting `test_handler` generates only the client.

The handler destination is resolved relative to `octoqlgen.yaml`. Its package is
inferred independently from its directory, including a new empty directory. The
generated package imports the generated client package and aliases the converted
operation, input, enum, fragment, abstract variant, and `OctoqlOther` types.
Operations included in a handler must begin with an uppercase letter so their
client types can be imported by the separate handler package.

## Typed expectations

Each query or mutation gets `Expect<Operation>`, `Default<Operation>`, and
`Reset<Operation>` methods. Variables are decoded from the request and matched
by value. Matching expectations are consumed in registration order.

```go
handler := githubapitest.NewTestHandler(t)
handler.ExpectGetRepository(vars).Respond(data)
handler.ExpectGetRepository(missingVars).RespondError(octoql.Error{
	Type:       "NOT_FOUND",
	Message:    "repository not found",
	Path:       octoql.Path{"repository"},
	Extensions: map[string]any{"code": "missing"},
})
handler.ExpectGetRepository(partialVars).
	WithOptions(githubapitest.WithExtensions(map[string]any{"trace": "abc"})).
	RespondDataAndErrors(partialData, octoql.Error{
		Type:    "FORBIDDEN",
		Message: "one field is unavailable",
	})
```

An expectation defaults to one call. `Times(n)` requires exactly `n` calls,
`MinTimes(n)` requires at least `n`, and `MinTimes(0)` creates an unlimited stub.
`Default<Operation>` is an unlimited fallback used only when no concrete
expectation matches. `NewTestHandler` registers cleanup verification with
`testing.TB.Cleanup`.

Unexpected calls to a known operation fail the test and return a GraphQL error.
Unknown operation names return a GraphQL error without creating a typed
expectation or failing the test. Expectation queues, counts, defaults, resets,
request matching, and cleanup verification are locked for concurrent use.
Treat response values as immutable after registering them.

`Handle(func(vars, http.ResponseWriter))` receives typed variables and gives the
test full HTTP response control.

## Response controls

`Respond` and `RespondError` accept response options. Partial responses use
`WithOptions` followed by `RespondDataAndErrors`.

- `WithStatus` sets the HTTP status.
- `WithHeader` and `WithHeaders` set caller-controlled response headers.
  Supplied headers are cloned when the response is registered.
- `WithExtensions` writes top-level GraphQL `extensions`.
- `WithPrimaryRateLimit` writes GitHub's primary `X-RateLimit-*` headers.
- `WithSecondaryRateLimit` writes `Retry-After`. Combine it with `WithStatus(403)`
  for GitHub's HTTP 403 form; the default status supports the HTTP 200 form.

No helper retries or sleeps.

## `httptest` example

```go
handler := githubapitest.NewTestHandler(t)
server := httptest.NewServer(handler)
t.Cleanup(server.Close)

client := octoql.NewClient(server.URL, server.Client())
vars := githubapitest.GetRepositoryVariables{
	Owner: "octo-org",
	Name:  "octo-repo",
	First: 1,
}
handler.ExpectGetRepository(vars).Respond(githubapitest.GetRepositoryResponse{
	Repository: githubapitest.GetRepositoryRepository{
		FullName: "octo-org/octo-repo",
	},
})

response, err := githubapi.GetRepository(
	t.Context(),
	client,
	vars.Owner,
	vars.Name,
	vars.First,
	vars.After,
)
require.NoError(t, err)
require.Equal(t, "octo-org/octo-repo", response.Data.Repository.FullName)

rateLimited := githubapitest.GetRepositoryVariables{
	Owner: "octo-org",
	Name:  "rate-limited",
	First: 1,
}
handler.ExpectGetRepository(rateLimited).RespondError(
	octoql.Error{Type: "RATE_LIMITED", Message: "quota exhausted"},
	githubapitest.WithPrimaryRateLimit(octoql.RateLimit{
		Limit:     5000,
		Remaining: 0,
		Used:      5000,
		Resource:  "graphql",
	}),
)
```

The checked-in
[`internal/handlertest`](../internal/handlertest/handler_test.go) fixture is a
runnable example covering mutations, aliases, partial data, GitHub errors,
top-level extensions, custom status and headers, primary and secondary rate
limits, dynamic handlers, abstract types, pagination inputs, custom scalars, and
concurrent requests.
