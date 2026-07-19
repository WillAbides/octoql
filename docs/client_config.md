# Configuring and using the genqlient client

This document describes common patterns for using generated clients at runtime. For full client reference documentation, see the [godoc].

[godoc]: https://pkg.go.dev/github.com/willabides/octoql

## Creating a client

Call [`octoql.NewClient`][godoc#NewClient] to get an `*octoql.Client`, which you can then pass to generated query and mutation functions.

For example (see the [getting started docs](INTRODUCTION.md) for the full setup):

```go
ctx := context.Background()
client := octoql.NewClient("https://api.github.com/graphql", http.DefaultClient)
resp, err := getUser(ctx, client, "benjaminjkraft")
fmt.Println(resp.Data.User.Name, err)
```

You can pass the client around however you like to inject dependencies, such as via a global variable, context value, or [fancy typed context][kacontext].

[godoc#NewClient]: https://pkg.go.dev/github.com/willabides/octoql#NewClient
[kacontext]: https://blog.khanacademy.org/statically-typed-context-in-go/

### Authentication and other headers

To use an API requiring authentication, customize the HTTP client passed to [`octoql.NewClient`][godoc#NewClient]. The usual way to do this is to wrap the client's `Transport`:

```go
type authedTransport struct {
  wrapped http.RoundTripper
}

func (t *authedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
  key := ...
  req.Header.Set("Authorization", "bearer "+key)
  return t.wrapped.RoundTrip(req)
}

func MakeQuery(...) {
  client := octoql.NewClient("https://api.github.com/graphql",
    &http.Client{Transport: &authedTransport{wrapped: http.DefaultTransport}})

  resp, err := MyQuery(ctx, client, ...)
}
```

The same method works for passing other HTTP headers, like [`traceparent`](https://www.w3.org/TR/trace-context/). To set a request-dependent header, the `RoundTrip` method has access to the full request, including the context from `req.Context()`. For more on wrapping HTTP clients, see [this post](https://dev.to/stevenacoffman/tripperwares-http-client-middleware-chaining-roundtrippers-3o00).

## Testing

### Testing code that uses genqlient

Testing code that uses genqlient typically involves passing in a special HTTP client that does what you want, similar to authentication.  For example, you might write a client whose `RoundTrip` returns a fixed response, constructed with [`httptest`].  Or, you can use `httptest` to start up a temporary server, and point genqlient at that.  Many third-party packages provide support for this sort of thing; genqlient should work with any HTTP-level mocking that can expose a regular `http.Client`.

For an example, octoql's own integration tests use both approaches:
- we [set up a simple GraphQL server](../internal/integration/server/server.go) using [`gqlgen`][gqlgen] and [`httptest`][httptest], and run requests against that
- we also [wrap the HTTP client](../internal/integration/runtime_test.go) to exercise HTTP metadata and typed error behavior.

[gqlgen]: https://gqlgen.com/
[httptest]: https://pkg.go.dev/net/http/httptest

### Testing servers

If you want, you can use genqlient to test your GraphQL APIs; as with mocking you can point genqlient at anything that exposes an ordinary HTTP endpoint or a custom `http.Client`. However, at Khan Academy we've found that genqlient usually isn't the best client for testing: for example, manually constructing values of genqlient's response types gets cumbersome when interfaces or fragments are involved. Instead, we prefer to use a lightweight (and weakly-typed) client for that, and may separately open-source ours in the future.

## Response objects

Each generated query or mutation helper returns an `*octoql.Response[T]`, where `T` is the generated operation response type. For example, given a simple query:

octoql does not support GraphQL subscriptions. octoqlgen rejects subscription
operations during generation.

```graphql
query getUser($login: String!) {
  user(login: $login) {
    name
  }
}
```

genqlient will generate something like the following:

```go
func getUser(...) (*octoql.Response[getUserResponse], error) { ... }

type getUserResponse struct {
	User getUserUser
}

type getUserUser struct {
	Name string
}
```

For more on accessing response objects for interfaces and fragments, see the [operations documentation](operations.md#interfaces).

### Handling errors

Generated helpers return the root runtime error unchanged. GraphQL errors are
inspectable as `octoql.Errors` and individual `*octoql.Error` values. Non-2xx
responses are inspectable as `*octoql.HTTPError`, and primary or secondary
rate-limit responses as `*octoql.RateLimitError`.

Once an HTTP response is received, the returned response remains non-nil and
contains any partial `Data`, `Errors`, `Extensions`, cloned HTTP headers,
request ID, and rate-limit metadata. Transport or request failures before an
HTTP response return a nil response.

For example, you might do one of the following:
```go
// return both error and field:
resp, err := getUser(...)
return resp.Data.User.Name, err

// handle different errors differently:
resp, err := getUser(...)
var errList octoql.Errors
if errors.As(err, &errList) {
  for _, err := range errList {
    fmt.Printf("%v at %v\n", err.Message, err.Path)
  }
  fmt.Printf("partial response: %v\n", resp.Data)
} else if err != nil {
  fmt.Printf("http/network error: %v\n", err)
} else {
  fmt.Printf("successful response: %v\n", resp)
}
```

### Marshaling

All genqlient-generated types support both JSON-marshaling and unmarshaling, which can be useful for putting them in a cache, inspecting them by hand, using them in mocks (although this is [not recommended](#testing-servers)), or anything else you can do with JSON.  It's not guaranteed that marshaling a genqlient type will produce the exact GraphQL input -- we try to get as close as we can but there are some limitations around Go zero values -- but unmarshaling again should produce the value genqlient returned.  That is:

```go
resp, err := MyQuery(...)
// not guaranteed to match what the server sent (but close):
b, err := json.Marshal(resp.Data)
// guaranteed to match resp:
var respAgain MyQueryResponse
err := json.Unmarshal(b, &respAgain)
```
