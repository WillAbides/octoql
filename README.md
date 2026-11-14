# octoql

octoql generates type-safe Go clients and typed test handlers for GitHub's
GraphQL API from your GraphQL queries and mutations.

octoql started as a fork of [Khan/genqlient](https://github.com/Khan/genqlient).
See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for attribution.

## Requirements and installation

octoql requires Go 1.27 or newer. Run `octoqlgen` from a `go:generate`
directive with an explicit release version.

From the root of an existing Go module, initialize a project:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init
```

Replace `<version>` here and throughout this guide with a release tag from the
[releases page](https://github.com/WillAbides/octoql/releases). Pinning an
explicit tag keeps generation reproducible. octoql is pre-1.0, so review the
release notes before updating.

Generated clients are self-contained and depend only on the Go standard library
by default. Application code never imports `github.com/willabides/octoql`.

## Generate a client

`octoqlgen init` requires GitHub authentication through `GH_TOKEN`,
`GITHUB_TOKEN`, or the `gh` CLI. By default, it downloads the latest GitHub
Docs Free, Pro, & Team (`fpt`) schema into `.octoql/`, creates
`.octoql/.gitignore`, and writes `octoqlgen.yaml`:

```yaml
# yaml-language-server: $schema=https://github.com/WillAbides/octoql/releases/download/<version>/octoqlgen.schema.yaml

schema:
  path: .octoql/schema.graphql
  sha256: <digest>
  source:
    repository: github/docs
    path: src/graphql/data/fpt/schema.docs.graphql
    revision: <commit sha>
operations:
  - graphql/**/*.graphql
generated: internal/githubapi/generated.go
```

To target GitHub Enterprise Cloud or GitHub Enterprise Server instead, pass
`--schema-version`:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init --schema-version ghec
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init --schema-version ghes-3.21
```

See the [configuration reference](docs/configuration.md) for all `octoqlgen.yaml`
options.

Next, write a query in `graphql/repository.graphql`:

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

Add a `go:generate` directive in `internal/githubapi/githubapi.go`:

```go
package githubapi

//go:generate go run github.com/willabides/octoql/cmd/octoqlgen@<version> generate --config ../../octoqlgen.yaml
```

Then run `go generate`:

```sh
go generate ./...
```

Each query or mutation name becomes a method on the generated `Client`. Use an
uppercase operation name whenever the generated method and types need to be
exported, or whenever a [typed test handler](#typed-test-handlers) is
configured. GraphQL subscriptions are not supported. See the
[directive reference](docs/directive.md) for per-operation and per-field
customization.

## Schema sources and updates

When `schema.source` is configured, keep `schema.path` in the gitignored
`.octoql` directory and commit the pinned `revision` and `sha256` in
`octoqlgen.yaml`. For a checked-in local schema, specify only `path`:

```yaml
schema:
  path: schema/github.graphql
```

To verify an existing schema file or download a missing remote schema without
running code generation:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> schema fetch
```

To pull the latest schema from the configured repository's default branch and
update `octoqlgen.yaml`:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> schema update
git diff -- octoqlgen.yaml
go generate ./...
```

See the [CLI reference](docs/cli.md) for all commands and flags.

## Call the generated client

Create a client with `NewClient` and configure bearer authentication with
`SetBearerToken`:

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
if response.Repository == nil {
	return fmt.Errorf("repository not found")
}
fmt.Println(response.Repository.NameWithOwner)
```

`response.Repository` is a pointer because `Query.repository` is nullable in
GitHub's schema. GitHub returns `null` when a repository does not exist or is
hidden from the caller's token, so check nullable fields before dereferencing
them.

Pass a different URL to `NewClient` for GitHub Enterprise Server, a proxy, or an
`httptest.Server`. Passing `nil` for the HTTP client uses `http.DefaultClient`.
For non-bearer authentication schemes, configure the `http.Client` or
`http.RoundTripper` passed to `NewClient`.

Generated clients refuse HTTP redirects by default. Point `NewClient` directly
at the final GraphQL endpoint whenever possible, or call
`client.SetAllowRedirects(true)` when a redirecting appliance requires it. See
[security considerations](docs/SECURITY.md#redirects-and-credentials) before
enabling redirects or attaching credentials in a custom `RoundTripper`.

## Runtime responses and errors

Generated operation methods return `(*<Operation>Response, error)`. Whenever
`err` is non-nil, the returned response pointer is `nil`.

If the server returns valid partial `data` alongside GraphQL errors, the error
chain includes a typed `*<Operation>PartialDataError`:

```go
partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
if ok {
	fmt.Printf("partial repository: %+v\n", partialErr.PartialData().Repository)
}
```

Every failure after receiving an HTTP response wraps `*githubapi.ResponseError`.
GraphQL errors, rate-limit metadata, and partial data are independent facets on
the returned error chain. Inspect whichever facets your caller needs with
`errors.AsType`:

| Facet                          | Carries                                                                        | Present when                                                  |
| ------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------- |
| `*ResponseError`               | `StatusCode`, `RequestID`, and a size-capped `RawBody`                         | Any failure after an HTTP response was received               |
| `Errors`                       | A `[]*Error` with `Message`, `Path`, `Locations`, and `Extensions`             | The response contained GraphQL errors                         |
| `*RateLimitError`              | `Kind` (`RateLimitPrimary` or `RateLimitSecondary`) and a `RateLimit` snapshot | The failing response carried rate-limit signals (see below)   |
| `*<Operation>PartialDataError` | Typed `PartialData()` for that operation                                       | `data` was non-null and decoded successfully alongside errors |

Transport errors that occur before an HTTP response is received do not include
`*ResponseError`.

A failing response includes `*RateLimitError` when it matches GitHub's primary
or secondary rate-limit signals:

- **`RateLimitSecondary`** applies when the response status is `200`, `403`, or
  `429` and includes a valid `Retry-After` header.
- **`RateLimitPrimary`** applies when `RateLimitSecondary` does not match and
  the response either contains a GraphQL error with `type == "RATE_LIMITED"`
  (on status `200`, `403`, or `429`), has HTTP status `429`, or has HTTP status
  `403` with `X-RateLimit-Remaining: 0`.

Call `client.RateLimit()` at any time to read a concurrency-safe snapshot of the
most recently observed primary rate-limit headers. The generated client never
sleeps or retries automatically.

## Fragments, enums, and abstract types

A named GraphQL fragment generates a reusable Go struct that is embedded into
every selection that spreads it:

```graphql
fragment RepositoryFields on Repository {
  nameWithOwner
  stargazerCount
}

query GetRepository($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    ...RepositoryFields
  }
}
```

```go
type RepositoryFields struct {
	NameWithOwner  string
	StargazerCount int
}
```

When a field's selection consists of a single fragment spread, annotate the
field with `@octoqlgen(flatten: true)` to use the fragment struct directly
instead of generating a wrapper struct. See the
[directive reference](docs/directive.md#flatten).

GraphQL enums generate a named `string` type and one constant per schema value.
Interface and union selections generate a Go interface, one concrete struct per
implementation referenced in a fragment spread, and an `OctoqlOther` catch-all
struct holding the shared fields and `__typename`:

```go
switch node := response.Node.(type) {
case *githubapi.GetNodeNodeIssue:
	fmt.Println("issue", node.Id, node.Title)
case *githubapi.GetNodeNodeRepository:
	fmt.Println("repository", node.Id)
case *githubapi.GetNodeNodeOctoqlOther:
	fmt.Println("other type", node.GetTypename())
}
```

By default, enum values and abstract implementations remain open at runtime so
generated clients stay forward-compatible when GitHub adds new enum values or
schema types. Unrecognized enum strings unmarshal without error, and
unrecognized `__typename` values decode into `OctoqlOther` (unless
[`omit_unreferenced_implementations`](docs/configuration.md#omit_unreferenced_implementations)
is set to `false`). When writing authorization or policy checks that must fail
closed, match known values explicitly and deny the `default` case. See
[schema evolution and authorization checks](docs/SECURITY.md#schema-evolution-and-authorization-checks)
for examples.

## Pagination

GitHub connections use cursor pagination. Request `pageInfo { hasNextPage endCursor }`
and pass `endCursor` as the next request's `after` variable:

```graphql
query ListIssues($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    issues(first: 100, after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
        number
        title
      }
    }
  }
}
```

Both `$after` and `endCursor` are nullable and generate as `*string`. Start with
`after = nil` on the first page:

```go
var after *string
for {
	response, err := client.ListIssues(ctx, githubapi.ListIssuesVariables{
		Owner: "octo-org",
		Name:  "octo-repo",
		After: after,
	})
	if err != nil {
		return err
	}

	if response.Repository == nil {
		return fmt.Errorf("repository not found")
	}
	issues := response.Repository.Issues
	for _, issue := range issues.Nodes {
		if issue == nil {
			continue
		}
		fmt.Println(issue.Number, issue.Title)
	}

	if !issues.PageInfo.HasNextPage {
		return nil
	}
	after = issues.PageInfo.EndCursor
}
```

## Generated types and GitHub defaults

Built-in GraphQL scalars map to standard Go types:

| GraphQL        | Go        |
| -------------- | --------- |
| `Int`          | `int`     |
| `Float`        | `float64` |
| `String`, `ID` | `string`  |
| `Boolean`      | `bool`    |

Nullable fields, variables, and list elements generate as pointers by default.
Attach `@octoqlgen(pointer: false)` to a variable or field when its Go zero
value should represent `null`, or `@octoqlgenDefaults(pointer: false)` on an
operation or fragment to change the default for its selections.

`octoqlgen` includes built-in bindings for common GitHub scalars such as
`DateTime`, `URI`, `HTML`, and `GitObjectID`. Custom scalars can be mapped in
`octoqlgen.yaml` under `bindings`. See the
[configuration reference](docs/configuration.md) and
[directive reference](docs/directive.md) for details.

## Typed test handlers

`octoqlgen` can generate a typed `http.Handler` alongside the client:

```yaml
generated: internal/githubapi/generated.go
test_handler:
  generated: internal/githubapitest/generated.go
```

For each operation, the generated handler provides `Expect<Operation>`,
`Default<Operation>`, and `Reset<Operation>` methods:

```go
handler := githubapitest.NewTestHandler(t)
server := httptest.NewServer(handler)
t.Cleanup(server.Close)

variables := githubapitest.GetRepositoryVariables{
	Owner: "octo-org",
	Name:  "octo-repo",
	First: 1,
}
handler.ExpectGetRepository(variables).
	Respond(githubapitest.GetRepositoryResponse{
		Repository: &githubapitest.GetRepositoryRepository{
			NameWithOwner: "octo-org/octo-repo",
		},
	})

client := githubapi.NewClient(server.URL, server.Client())
response, err := client.GetRepository(
	t.Context(),
	variables,
)
require.NoError(t, err)
require.NotNil(t, response.Repository)
require.Equal(t, "octo-org/octo-repo", response.Repository.NameWithOwner)
```

Each `Expect<Operation>` expects one matching call by default, or you can chain
`Times(n)` or `MinTimes(n)`. `Default<Operation>` registers a fallback when no
expectation matches, and `Reset<Operation>` or `handler.Reset()` clears
registered expectations. The handler is safe for concurrent requests and
verifies unmet expectations during `t.Cleanup`.

Expectations can return typed data with `Respond`, GraphQL errors with
`RespondError`, partial data with `RespondDataAndErrors`, or run a custom
handler function with `Handle`. Response options such as `WithStatus`,
`WithHeader`, `WithPrimaryRateLimit`, and `WithSecondaryRateLimit` let tests
exercise error and rate-limit paths directly:

```go
handler.ExpectGetRepository(variables).
	RespondError(
		githubapitest.Error{Message: "API rate limit exceeded"},
		githubapitest.WithSecondaryRateLimit(30*time.Second),
	)
```

See [`test_handler` in the configuration reference](docs/configuration.md#test_handler)
for `types: local` and other handler configuration options.

## Reference

- [Configuration reference](docs/configuration.md)
- [octoqlgen directive reference](docs/directive.md)
- [CLI reference](docs/cli.md)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)
