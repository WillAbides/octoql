[![Test Status](https://github.com/willabides/octoql/actions/workflows/ci.yaml/badge.svg)](https://github.com/willabides/octoql/actions)
[![Contributor Covenant](https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg)](docs/CODE_OF_CONDUCT.md)

# octoql

octoql generates type-safe Go clients and typed test handlers for GitHub-shaped
GraphQL APIs. It validates queries and mutations against a pinned schema, then
generates a self-contained Go client with typed methods for each operation.

octoql is a standalone project derived from
[Khan/genqlient](https://github.com/Khan/genqlient). See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for exact source pins and
attribution, including the bounded gqltesthandler input used by typed
test-handler generation.

## Contents

- [Requirements and installation](#requirements-and-installation)
- [Generate a client](#generate-a-client)
- [Schema sources and updates](#schema-sources-and-updates)
- [Call the generated client](#call-the-generated-client)
- [Runtime responses and errors](#runtime-responses-and-errors)
- [Generated types and GitHub defaults](#generated-types-and-github-defaults)
- [Typed test handlers](#typed-test-handlers)
- [Migration from genqlient or earlier octoql config](#migration-from-genqlient-or-earlier-octoql-config)
- [Contributing](#contributing)
- [Reference and project policies](#reference-and-project-policies)

## Requirements and installation

octoql requires Go 1.26 or newer. Pin `octoqlgen` as a Go tool dependency in
the module that owns the generated client:

```sh
go get -tool github.com/willabides/octoql/cmd/octoqlgen
```

Run the pinned tool with `go tool octoqlgen`. The Go tool dependency is the
recommended installation because the runtime and generator then resolve from the
same module version.

For a standalone binary, install a release archive with
[bindown](https://github.com/WillAbides/bindown):

```sh
bindown template-source add octoql https://github.com/WillAbides/octoql/releases/latest/download/bindown.yaml
bindown dependency add octoqlgen --source octoql
```

Or build a standalone binary from source with an explicit version or commit:

```sh
go install github.com/willabides/octoql/cmd/octoqlgen@<version-or-commit>
```

Generated clients use only the standard library unless configured scalar
bindings add imports;
application code does not import `github.com/willabides/octoql`.

## Generate a client

Initialize a project:

```sh
go tool octoqlgen init
```

GitHub authentication must be available through `GH_TOKEN`, `GITHUB_TOKEN`, or
the `gh` CLI.

This resolves and fetches the latest GitHub Docs Free, Pro, & Team (`fpt`)
schema, then creates a configuration containing its commit revision and SHA-256
digest. It also creates `.octoql/.gitignore`; the generated config uses the
gitignored `.octoql/schema.graphql` path, `graphql/**/*.graphql` for operations,
and `internal/githubapi/generated.go` for output.

Choose another GitHub Docs schema version with `--schema-version`:

```sh
go tool octoqlgen init --schema-version ghec
go tool octoqlgen init --schema-version ghes-3.21
```

All paths and globs in `octoqlgen.yaml` are relative to that file. See
[`docs/octoqlgen.yaml`](docs/octoqlgen.yaml) for local schemas, other remote
sources, and every configuration option.

Create `graphql/repository.graphql`:

```graphql
query GetRepository($owner: String!, $name: String!, $first: Int!) {
  repository(owner: $owner, name: $name) {
    nameWithOwner
    issues(first: $first) {
      nodes {
        number
        title
      }
    }
  }
}
```

Fetch or verify the configured schema, then generate:

```sh
go tool octoqlgen schema fetch
go tool octoqlgen generate
```

Generation performs the same schema verification or fetch before it writes
code. Query and mutation operation names become generated helper names,
so use an uppercase name when the helper must be exported. octoql does not
support GraphQL subscriptions, and `octoqlgen` rejects subscription operations.

Operations may also be embedded in Go string literals. See the
[directive reference](docs/octoqlgen_directive.graphql) for embedded operations
and per-operation options.

## Schema sources and updates

`schema.path` is always the schema used for generation. Keep it in the
gitignored `.octoql` directory when the source is remote. A local schema needs
only its path:

```yaml
schema:
  path: schema/github.graphql
```

GitHub.com sources require a SHA-256 digest and full commit SHA. Authentication
uses `GH_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`. See the
[configuration reference](docs/octoqlgen.yaml) for all schema settings.

`octoqlgen init` configures and fetches the latest `fpt` schema by default.
Pass `--schema-version` to initialize with another GitHub Docs version.

`schema fetch` verifies an existing file or fetches a missing remote file:

```sh
go tool octoqlgen schema fetch
```

`schema update` fetches the latest version of the configured repository path
from its default branch, validates and writes it, then updates the configuration
revision and `sha256`. Run schema updates serially.

```sh
go tool octoqlgen schema update
git diff -- octoqlgen.yaml
go tool octoqlgen generate
```

The `.octoql` schema normally remains ignored while the reviewed pin in
`octoqlgen.yaml` is committed. Use `--config PATH` with fetch, update, or generate
when the config has another name or location.

## Call the generated client

Configure GitHub bearer authentication directly on the client:

```go
client := githubapi.NewClient("https://api.github.com/graphql", nil)
err := client.SetBearerToken(os.Getenv("GITHUB_TOKEN"))
if err != nil {
	return err
}

response, err := client.GetRepository(
	ctx,
	githubapi.GetRepositoryVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
		First: 10,
	},
)
if err != nil {
	return err
}
fmt.Println(response.Repository.NameWithOwner)
```

Pass a different endpoint to `githubapi.NewClient` for GHES, a proxy, or an
`httptest.Server`. Pass nil as the HTTP client to use `http.DefaultClient`.

For basic authentication or another authentication scheme, configure the
`http.Client` or `http.RoundTripper` passed to `NewClient`.

## Runtime responses and errors

Generated helpers return a pointer to the concrete operation response and an
error. GraphQL data is available directly:

```go
response, err := client.GetRepository(ctx, githubapi.GetRepositoryVariables{
	Owner: owner,
	Name:  name,
	First: 10,
})
if err == nil {
	fmt.Println(response.Repository.NameWithOwner)
}
```

> [!IMPORTANT]
> A generated helper returns a nil response whenever its error is non-nil, even
> when GitHub returned decodable, non-null partial GraphQL data. Do not expect
> the regular `response` result to contain partial data. Instead, use
> `errors.AsType` to extract the operation-specific partial-data error:

```go
partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
if ok {
	fmt.Printf("partial repository: %+v\n", partialErr.PartialData().Repository)
}
```

Each `*githubapi.Error` retains `Type`, `Message`, `Path`, `Locations`, and its own
`Extensions`. `Error.Type` is an open string type so new GitHub values remain
available without a runtime update.

Generated helpers follow the usual Go convention: the response is nil whenever
the error is non-nil. When GitHub returns decodable, non-null partial `data`
alongside GraphQL errors, octoql stores that data in the error for explicit
extraction. A `data: null` response has no partial-data facet:

```go
response, err := client.GetRepository(ctx, githubapi.GetRepositoryVariables{
	Owner: owner,
	Name:  name,
	First: 10,
})
if err != nil {
	// response is always nil here.
	partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
	if ok {
		fmt.Printf("partial repository: %+v\n", partialErr.PartialData().Repository)
	}
}

graphqlErrors, ok := errors.AsType[githubapi.Errors](err)
if ok {
	for _, graphqlError := range graphqlErrors {
		fmt.Printf("%s: %s at %s\n",
			graphqlError.Type,
			graphqlError.Message,
			graphqlError.Path,
		)
	}
}

responseError, ok := errors.AsType[*githubapi.ResponseError](err)
if ok {
	fmt.Printf("status=%d request_id=%s\n",
		responseError.StatusCode,
		responseError.RequestID,
	)
}
```

Every failure after receiving an HTTP response includes
`*githubapi.ResponseError`, including HTTP-200 GraphQL, read, close, protocol, and
decode failures. It carries the status and `X-GitHub-Request-ID`. Client buffers
and decodes at most `githubapi.DefaultResponseSizeLimit`
(10 MiB) from each HTTP response. Configure a different positive limit before
executing operations:

```go
err := client.SetResponseSizeLimit(20 * 1024 * 1024)
if err != nil {
	return err
}
```

An oversized response fails before JSON decoding with
`*githubapi.ResponseSizeLimitError`; it still includes
`*githubapi.ResponseError` and any applicable `*githubapi.RateLimitError` in
the same error chain. `RawBody`
contains at most 64 KiB for non-2xx, over-limit, or undecodable responses;
`RawBodyTruncated` reports truncation. Raw response bodies may contain sensitive
data and should not be logged indiscriminately.

Error types are independent facets of one chain, not mutually exclusive
categories. A rate-limited response can match `*githubapi.RateLimitError`,
`*githubapi.ResponseError`, and `githubapi.Errors`. Use separate `errors.As` or
`errors.AsType` checks when more than one facet matters.

| Outcome | Generated response | Error facets |
| --- | --- | --- |
| Success | Non-nil concrete data | `nil` |
| GraphQL errors with decodable non-null data | `nil`; inspect the generated operation partial-data error | operation partial-data error, `ResponseError`, `Errors` |
| Any error without decodable data | `nil` | `ResponseError` and available causes |
| Primary or secondary rate limit with decodable data | `nil`; inspect the generated operation partial-data error | operation partial-data error, `RateLimitError`, `ResponseError`, and possibly `Errors` |
| Client configuration, encoding, or transport failure before a response | `nil` | Wrapped underlying error; no `ResponseError` |

Primary and secondary GitHub limits wrap `*githubapi.ResponseError` in
`*githubapi.RateLimitError`. Primary limits require
`X-RateLimit-Remaining: 0` plus HTTP 403/429 or a GraphQL `RATE_LIMITED` error.
Secondary limits use `Retry-After` on HTTP 200/403/429 responses:

```go
rateLimitError, ok := errors.AsType[*githubapi.RateLimitError](err)
if ok {
	fmt.Printf("kind=%s remaining=%d retry_at=%s\n",
		rateLimitError.Kind,
		rateLimitError.RateLimit.Remaining,
		rateLimitError.RateLimit.RetryAt,
	)
}
```

The client also keeps the latest valid primary rate-limit headers observed from
any response:

```go
rateLimit, known := client.RateLimit()
if known {
	fmt.Printf("remaining=%d reset=%s\n", rateLimit.Remaining, rateLimit.Reset)
}
```

The snapshot is concurrency-safe and advisory, not a reservation: other
clients or processes can consume the same GitHub budget after it is observed.
Missing or malformed rate-limit headers do not erase the last valid snapshot.
Successful response status, arbitrary headers, and request ID are intentionally
not attached to generated data. Use the supplied `http.RoundTripper` when an
application needs arbitrary successful-response headers.

octoql never retries or sleeps automatically. Apply retry policy in the calling
application after considering operation safety, `Retry-After`, and the parsed
reset time.

Generated errors expose package-neutral accessors for shared handling across
multiple generated packages. Applications can define structural interfaces
without importing a generated package:

```go
type rateLimitFailure interface {
	error
	RateLimitKind() string
	RetryAt() time.Time
}

var rateLimitErr rateLimitFailure
if errors.As(err, &rateLimitErr) {
	scheduleRetry(rateLimitErr.RetryAt())
}
```

`RetryAt` returns the primary rate-limit reset time or the secondary rate-limit
retry time, depending on `RateLimitKind`.

Response errors similarly expose `HTTPStatusCode` and `GitHubRequestID`;
GraphQL error lists expose `GraphQLErrorCount` and `GraphQLError`.

## Generated types and GitHub defaults

GraphQL's built-in scalars map to ordinary Go values:

| GraphQL        | Go        |
|----------------|-----------|
| `Int`          | `int`     |
| `Float`        | `float64` |
| `String`, `ID` | `string`  |
| `Boolean`      | `bool`    |

Nullable named values generate as pointers by default. Generated abstract
interface values are the exception: their nil interface value represents
GraphQL null, so they are never wrapped in pointers. Nullable list values remain
slices so GraphQL `null` and `[]` map to nil and empty slices, respectively;
nullable non-interface list elements generate as pointers. Use
`@octoqlgen(pointer: false)` on a specific operation argument or selected field
when its zero value should represent GraphQL null. The `omitempty` directive
remains independent and explicit.

octoqlgen also supplies GitHub defaults:

| GitHub scalar | Go |
| --- | --- |
| `DateTime`, `PreciseDateTime`, `GitTimestamp` | `time.Time` |
| `CustomPropertyValue` | `encoding/json.RawMessage` |
| `Base64String`, `BigInt`, `Date` | `string` |
| `GitObjectID`, `GitRefname`, `GitSSHRemote` | `string` |
| `HTML`, `URI`, `X509Certificate` | `string` |

Explicit `bindings` in `octoqlgen.yaml` override defaults:

```yaml
bindings:
  DateTime:
    type: example.com/project/githubapi.Timestamp
    marshaler: example.com/project/githubapi.MarshalTimestamp
    unmarshaler: example.com/project/githubapi.UnmarshalTimestamp
```

Unknown custom scalars require a binding. Bound types must implement the
necessary JSON behavior, or provide marshal and unmarshal functions.

For GraphQL interfaces and unions, octoqlgen defaults to generating concrete
types only for implementations selected by fragments. Every abstract selection
also receives an `OctoqlOther` catch-all with shared fields and `__typename`, so
new server implementations remain decodable. To restore inherited generation
of every schema implementation:

```yaml
omit_unreferenced_implementations: false
```

The opt-out removes the catch-all and may make a new server typename an
unmarshal error. Generated types include private JSON first-pass guards where
needed to prevent method promotion from changing `encoding/json` behavior.

## Typed test handlers

Generate a typed `http.Handler` from the configured operations:

```yaml
generated: internal/githubapi/generated.go
test_handler:
  generated: internal/githubapitest/generated.go
  types: client
```

`types: client` is the default and makes handler response values assignable to
generated client types.

Use `types: local` to generate separate handler types:

```yaml
test_handler:
  generated: internal/githubapitest/generated.go
  types: local
```

Local handler values are not assignable to client types. Test-handler
configuration requires query and mutation names to begin with an uppercase
letter.

After `go tool octoqlgen generate`, each handler operation has matching
`Expect<Operation>`, `Default<Operation>`, and `Reset<Operation>` methods:

```go
handler := githubapitest.NewTestHandler(t)
server := httptest.NewServer(handler)
t.Cleanup(server.Close)

variables := githubapitest.GetRepositoryVariables{
	Owner: "octo-org",
	Name:  "octo-repo",
	First: 1,
}
handler.ExpectGetRepository(variables, githubapitest.Times(2)).
	Respond(githubapitest.GetRepositoryResponse{
		Repository: githubapitest.GetRepositoryRepository{
			NameWithOwner: "octo-org/octo-repo",
		},
	})

client := githubapi.NewClient(server.URL, server.Client())
response, err := client.GetRepository(
	t.Context(),
	variables,
)
require.NoError(t, err)
require.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
```

An expectation defaults to one call. Pass `Times(n)` to require exactly `n`,
`MinTimes(n)` to set a minimum, or `MinTimes(0)` to create an unlimited stub.
`Default<Operation>` is an unlimited fallback. Cleanup verifies unmet
expectations, and expectation state is safe for concurrent requests.

Expectations can also configure partial data, errors, headers, status, and rate
limits.

```go
handler.ExpectGetRepository(variables).
	WithOptions(
		githubapitest.WithStatus(http.StatusForbidden),
		githubapitest.WithHeader("X-GitHub-Request-ID", "request-123"),
		githubapitest.WithPrimaryRateLimit(githubapitest.RateLimit{
			Limit:     5000,
			Remaining: 0,
			Used:      5000,
			Resource:  "graphql",
		}),
	).
	RespondDataAndErrors(
		partialData,
		githubapitest.Error{
			Type:       "FORBIDDEN",
			Message:    "one field is unavailable",
			Extensions: map[string]any{"code": "missing"},
		},
	)
```

`WithHeaders` replaces multiple headers. `WithSecondaryRateLimit` writes
`Retry-After`. `RespondError` returns errors without data, and `Handle` gives a
test complete `http.ResponseWriter` control. Test-handler helpers do not retry
or sleep.

## Migration from genqlient or earlier octoql config

- `octoqlgen.yaml` is the only user-facing generator config. Rename and flatten
  inherited `genqlient.yaml` settings into it. Legacy config is not discovered,
  parsed, merged, or translated.
- Replace a scalar or list `schema` setting with `schema.path` and, for remote
  materialization, one pinned `schema.source` plus `schema.sha256`.
- All configured paths are relative to `octoqlgen.yaml`.
- Generated application code has no dependency on
  `github.com/willabides/octoql`. Invoke the pinned
  `github.com/willabides/octoql/cmd/octoqlgen` tool.
- Generated helpers now return concrete operation data. Replace
  `response.Data.Field` with `response.Field`. Replace handwritten `Do` calls
  with generated operations.
- Replace `HTTPError` checks with `ResponseError`. The latter covers every
  failure after an HTTP response, including HTTP-200 GraphQL and decode errors.
- Read successful primary rate-limit state from `Client.RateLimit()`.
- Remove `use_extensions`. It was a no-op. Top-level response extensions are
  ignored; per-error `Error.Extensions` remains available.
- octoql does not support subscriptions. Convert supported operations to queries
  or mutations before migration.
- Explicit scalar bindings override octoql's GitHub defaults.
- Abstract selections now use `OctoqlOther` by default. Set
  `omit_unreferenced_implementations: false` temporarily when migrating an
  exhaustive type switch.
- Upgrade the generator tool and regenerate checked-in code to pick up runtime
  fixes.

## Contributing

Snapshot maintenance is a contributor workflow, not part of installing or using
octoql. Update go-snaps output only when the behavior or generated artifacts
covered by an affected test intentionally change, review every update, and run
the same focused test normally afterward. See [CONTRIBUTING.md](CONTRIBUTING.md)
for commands and repository conventions.

## Reference and project policies

- [Annotated `octoqlgen.yaml` reference](docs/octoqlgen.yaml)
- [`@octoqlgen` directive reference](docs/octoqlgen_directive.graphql)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)
