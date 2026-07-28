# octoql

octoql generates type-safe Go clients and typed test handlers for GitHub's
GraphQL API from your graphql queries and mutations.

octoql started as a fork of [Khan/genqlient](https://github.com/Khan/genqlient).
See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for attribution.

## Requirements and installation

octoql requires Go 1.26 or newer. Run `octoqlgen` from a `go:generate`
directive with an explicit release version; this does not add a dependency to
the module that owns the generated client.

From the root of an existing Go module, initialize a project with:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init
```

Replace `<version>` here and throughout this guide with a release tag from the
[releases page](https://github.com/WillAbides/octoql/releases). Pinning an
explicit tag keeps generation reproducible. octoql is pre-1.0, so review the
release notes before moving between minor versions.

Generated clients are self-contained and use only the standard library by
default. External types named by `bindings`, `package_bindings`, a custom
`context_type`, or an operation's `@octoqlgen(bind: ...)` directive can add
imports for the packages they name. Application code does not import
`github.com/willabides/octoql`.

## Generate a client

GitHub authentication must be available through `GH_TOKEN`, `GITHUB_TOKEN`, or
the `gh` CLI.

`octoqlgen init` resolves and fetches the latest GitHub Docs Free, Pro, & Team
(`fpt`) schema, then creates a configuration containing its commit revision and
SHA-256 digest. It also creates `.octoql/.gitignore`; the generated config uses
the gitignored `.octoql/schema.graphql` path, `graphql/**/*.graphql` for
operations, and `internal/githubapi/generated.go` for output.

Choose another GitHub Docs schema version with `--schema-version`:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init --schema-version ghec
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init --schema-version ghes-3.21
```

All paths and globs in `octoqlgen.yaml` are relative to that file. The generated
configuration looks like this:

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

The `$schema` comment gives editors completion and hover text for every option.
See the [configuration reference](docs/configuration.md) for local schemas, other
remote sources, and every configuration option.

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

Create `internal/githubapi/githubapi.go` to generate the configured client:

```go
package githubapi

//go:generate go run github.com/willabides/octoql/cmd/octoqlgen@<version> generate --config ../../octoqlgen.yaml
```

Run generation with:

```sh
go generate ./...
```

Generation verifies or fetches the configured schema before it writes code.
Query and mutation operation names become generated helper names, so an
operation needs an uppercase name whenever its helper must be exported.
Configuring a [test handler](#typed-test-handlers) makes uppercase names a hard
requirement for every operation. octoql does not support GraphQL subscriptions,
and `octoqlgen` rejects subscription operations.

Operations may also be embedded in Go string literals. See the
[directive reference](docs/directive.md) for embedded operations
and per-operation options.

## Schema sources and updates

`schema.path` is always the schema used for generation. Keep it in the
gitignored `.octoql` directory when the source is remote. A local schema needs
only its path:

```yaml
schema:
  path: schema/github.graphql
```

GitHub.com sources require a SHA-256 digest and a Git ref. Use a full commit SHA
so generation is reproducible; `init` and `schema update` always write one.
Authentication uses `GH_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`. See the
[configuration reference](docs/configuration.md) for all schema settings.

`octoqlgen init` configures and fetches the latest `fpt` schema by default.
Pass `--schema-version` to initialize with another GitHub Docs version.

`schema fetch` verifies an existing file or fetches a missing remote file:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> schema fetch
```

`schema update` fetches the latest version of the configured repository path
from its default branch, validates and writes it, then updates the configuration
revision and `sha256`. Run schema updates serially.

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> schema update
git diff -- octoqlgen.yaml
go generate ./...
```

The `.octoql` schema normally remains ignored while the reviewed pin in
`octoqlgen.yaml` is committed. Use `--config PATH` with fetch, update, or generate
when the config has another name or location. See the
[CLI reference](docs/cli.md#schema-update) for every command and flag.

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
if response.Repository == nil {
	return fmt.Errorf("repository not found")
}
fmt.Println(response.Repository.NameWithOwner)
```

`Repository` is a pointer because `Query.repository` is nullable in GitHub's
schema. GitHub returns null for repositories that do not exist or that the
token cannot see, so check nullable values before dereferencing them.

Pass a different endpoint to `githubapi.NewClient` for GHES, a proxy, or an
`httptest.Server`. Pass nil as the HTTP client to use `http.DefaultClient`.

Use `Client.SetBearerToken` for OAuth 2.0 bearer authentication. For basic
authentication or another scheme, configure the `http.Client` or
`http.RoundTripper` passed to `NewClient`. Generated clients refuse redirects
by default. See [security considerations](docs/SECURITY.md#redirects-and-credentials)
before enabling redirects or applying credentials in a custom `RoundTripper`.

Configure the final GraphQL endpoint directly whenever possible. An appliance
behind a redirecting load balancer or vanity hostname can opt in to redirects:

```go
err := client.SetAllowRedirects(true)
if err != nil {
	return err
}
```

The opt-in follows at most 10 redirects and removes the bearer `Authorization`
header when a redirect leaves the original scheme, host, and port. Do not enable
redirects to turn an `http://` endpoint into `https://`: configure the
`https://` endpoint directly so the bearer token is never sent in cleartext on
the first hop.

## Runtime responses and errors

Generated helpers return a pointer to the concrete operation response and an
error. The response is nil when the error is non-nil. Sometimes GitHub returns
partial data with an error. Use `errors.AsType` to check for partial data:

```go
partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
if ok {
	fmt.Printf("partial repository: %+v\n", partialErr.PartialData().Repository)
}
```

Every failure after receiving an HTTP response includes
`*githubapi.ResponseError`. GraphQL errors, rate limits, and partial data are
independent error facets, so use `errors.AsType` for each detail your application
needs:

| Facet                          | Carries                                                                        | Present when                                                  |
| ------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------- |
| `*ResponseError`               | `StatusCode`, `RequestID`, and a size-capped `RawBody`                         | Any failure after an HTTP response was received               |
| `Errors`                       | A `[]*Error` with `Message`, `Path`, `Locations`, and `Extensions`             | The response contained GraphQL errors                         |
| `*RateLimitError`              | `Kind` (`RateLimitPrimary` or `RateLimitSecondary`) and a `RateLimit` snapshot | The failing response carried rate-limit signals (see below)   |
| `*<Operation>PartialDataError` | Typed `PartialData()` for that operation                                       | `data` was non-null and decoded successfully alongside errors |

Transport-level failures, such as a connection error before any response
arrives, do not carry `*ResponseError`.

A failure becomes a `*RateLimitError` only when the response carries matching
signals. `RateLimitSecondary` requires a valid `Retry-After` header with status
200, 403, or 429. `RateLimitPrimary` requires `X-RateLimit-Remaining: 0`, and
additionally one of: status 403, status 429, or a GraphQL error of type
`RATE_LIMITED`. Other rejections surface as ordinary errors.

Read the latest observed primary rate-limit state with `client.RateLimit()`,
which is a concurrency-safe advisory snapshot. The client never retries
automatically.

## Fragments, enums, and abstract types

A named fragment generates its own Go type, embedded in each operation that
spreads it:

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

When a field's selection is a single fragment spread, `@octoqlgen(flatten: true)`
uses the fragment type directly instead of generating a wrapper struct. See the
[directive reference](docs/directive.md#flatten).

GraphQL enums generate a named string type with one constant per value. Use
[`casing`](docs/configuration.md#casing) when the default GraphQL-to-Go name
conversion is wrong for a schema, such as enums whose values differ only in
casing.

Interface and union selections generate one struct per implementation named by
an applicable fragment, plus an `OctoqlOther` catch-all holding the shared
fields and `__typename`. The catch-all represents implementations not selected
by a fragment as well as implementation names the generated schema did not
declare:

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

Abstract values are interfaces, so they are never wrapped in pointers: a nil
interface already represents GraphQL null. Set
[`omit_unreferenced_implementations`](docs/configuration.md#omit_unreferenced_implementations)
to false to generate a struct for every implementation the schema allows; the
`OctoqlOther` catch-all is then not generated, and an unrecognized
`__typename` produces an error.

Generated clients deliberately leave enum values and, with the default
configuration, abstract implementation names open at runtime. GitHub can add a
valid value after a client is generated, and accepting it keeps that client
forward compatible. The consequence is that `encoding/json` accepts an enum
string outside the generated constants, while an unrecognized `__typename`
decodes as `OctoqlOther` without an error or log. Go does not check type switches
or enum switches for exhaustiveness.

For access control and other decisions that must fail closed, enumerate the
known cases and deny the default. A concrete-type assertion alone can silently
skip the decision when an unrecognized implementation becomes the catch-all:

```go
switch actor := response.Actor.(type) {
case *githubapi.GetActorActorUser:
	return !actor.IsSuspended
case *githubapi.GetActorActorBot:
	return false
default:
	return false
}
```

Comparing an enum to a generated constant is safe as a positive check. Do not
treat inequality or an `else` branch as proof that the value is another known
constant:

```go
switch permission {
case githubapi.RepositoryPermissionAdmin:
	return true
case githubapi.RepositoryPermissionMaintain,
	githubapi.RepositoryPermissionRead,
	githubapi.RepositoryPermissionTriage,
	githubapi.RepositoryPermissionWrite:
	return false
default:
	return false
}
```

## Pagination

GitHub connections are cursor paginated. Select `pageInfo`, then pass the
previous `endCursor` back as the `after` argument:

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

`after` is nullable, so it generates as `*string`, as does the `endCursor` it is
fed from. Leave it nil for the first page:

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

The client never retries or sleeps on its own, so pace loops yourself and check
`client.RateLimit()` between pages when walking large result sets. `nodes` is
`[Issue]` in GitHub's schema, so its elements are nullable and generate as
pointers.

## Generated types and GitHub defaults

GraphQL's built-in scalars map to ordinary Go values:

| GraphQL        | Go        |
| -------------- | --------- |
| `Int`          | `int`     |
| `Float`        | `float64` |
| `String`, `ID` | `string`  |
| `Boolean`      | `bool`    |

Nullable named values generate as pointers by default. Use
`@octoqlgen(pointer: false)` on an argument or selected field when its zero
value should represent GraphQL null. octoqlgen includes bindings for common
GitHub scalars; add a binding for unknown custom scalars. See the
[configuration reference](docs/configuration.md) and
[directive reference](docs/directive.md) for scalar bindings,
abstract types, and field options.

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

After `go generate ./...`, each handler operation has matching
`Expect<Operation>`, `Default<Operation>`, and `Reset<Operation>` methods. In a
`_test.go` file, import the generated client package as `githubapi` and the
generated handler package as `githubapitest`:

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

`Repository` is a pointer because `Query.repository` is nullable in GitHub's
schema, so check it before dereferencing.

An expectation defaults to one call. Pass `Times(n)` to require exactly `n`,
`MinTimes(n)` to set a minimum, or `MinTimes(0)` to create an unlimited stub.
`Default<Operation>` is an unlimited fallback used when no expectation matches.
`Reset<Operation>` clears expectations for one operation, and `handler.Reset()`
clears them all. Cleanup verifies unmet expectations, and expectation state is
safe for concurrent requests.

Each expectation offers several ways to answer a request:

| Method                                | Purpose                                                                                                    |
| ------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `Respond(data, options...)`           | Return typed response data                                                                                 |
| `RespondError(err, options...)`       | Return a single GraphQL error with no data                                                                 |
| `RespondDataAndErrors(data, errs...)` | Return partial data alongside GraphQL errors                                                               |
| `Handle(fn)`                          | Serve the request with a custom function                                                                   |
| `WithOptions(options...)`             | Apply response options to this expectation's `Respond`, `RespondError`, and `RespondDataAndErrors` replies |

`WithOptions` does not affect `Handle`. The function passed to `Handle` is
responsible for writing its own status and headers.

`Handle` receives the `http.ResponseWriter` and, for operations that declare
variables, the decoded variables.

Response options adjust the HTTP reply. They may be passed to `Respond`,
`RespondError`, or `WithOptions`. `RespondDataAndErrors` takes no options
directly, so use `WithOptions` with it:

| Option                               | Effect                               |
| ------------------------------------ | ------------------------------------ |
| `WithStatus(code)`                   | Set the HTTP status code             |
| `WithHeader(name, values...)`        | Set one response header              |
| `WithHeaders(header)`                | Set several response headers at once |
| `WithPrimaryRateLimit(rateLimit)`    | Emit primary `X-RateLimit-*` headers |
| `WithSecondaryRateLimit(retryAfter)` | Set the `Retry-After` header         |

Together these cover the error paths that are otherwise hard to reproduce, such
as exercising a client's rate-limit handling:

```go
handler.ExpectGetRepository(variables).
	RespondError(
		githubapitest.Error{Message: "API rate limit exceeded"},
		githubapitest.WithSecondaryRateLimit(30*time.Second),
	)
```

## Reference

- [Configuration reference](docs/configuration.md)
- [`@octoqlgen` directive reference](docs/directive.md)
- [CLI reference](docs/cli.md)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)
