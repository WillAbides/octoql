# octoql

octoql generates type-safe Go clients and typed test handlers for GitHub's
GraphQL API from your graphql queries and mutations.

octoql started as a fork of [Khan/genqlient](https://github.com/Khan/genqlient).
See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for attribution.

## Requirements and installation

octoql requires Go 1.26 or newer. Run `octoqlgen` from a `go:generate`
directive with an explicit release version; this does not add a dependency to
the module that owns the generated client.

Initialize a project with:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init
```

Generated clients are self-contained and use only the standard library unless
configured scalar bindings add imports. Application code does not import
`github.com/willabides/octoql`.

## Generate a client

GitHub authentication must be available through `GH_TOKEN`, `GITHUB_TOKEN`, or
the `gh` CLI.

This resolves and fetches the latest GitHub Docs Free, Pro, & Team (`fpt`)
schema, then creates a configuration containing its commit revision and SHA-256
digest. It also creates `.octoql/.gitignore`; the generated config uses the
gitignored `.octoql/schema.graphql` path, `graphql/**/*.graphql` for operations,
and `internal/githubapi/generated.go` for output.

Choose another GitHub Docs schema version with `--schema-version`:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init --schema-version ghec
go run github.com/willabides/octoql/cmd/octoqlgen@<version> init --schema-version ghes-3.21
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
Query and mutation operation names become generated helper names, so use an
uppercase name when the helper must be exported. octoql does not support
GraphQL subscriptions, and `octoqlgen` rejects subscription operations.

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

Use `Client.SetBearerToken` for OAuth 2.0 bearer authentication. For basic
authentication or another scheme, configure the `http.Client` or
`http.RoundTripper` passed to `NewClient`. Credentials applied by a custom
`RoundTripper` are reapplied on every hop, bypassing normal redirect credential
protections. Generated clients refuse redirects by default, but a custom
transport that follows redirects itself can bypass that policy.

Configure the final GraphQL endpoint directly whenever possible. An appliance
behind a redirecting load balancer or vanity hostname can opt in to redirects:

```go
err := client.SetAllowRedirects(true)
if err != nil {
	return err
}
```

The opt-in follows at most 10 redirects and removes the bearer `Authorization`
header when a redirect leaves the original scheme, host, and port. It cannot
remove credentials applied by a custom `RoundTripper`. Do not enable redirects
to turn an `http://` endpoint into `https://`: configure the `https://` endpoint
directly so the bearer token is never sent in cleartext on the first hop.

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
needs. Read the latest observed primary rate-limit state with
`client.RateLimit()`. The client never retries automatically.

## Generated types and GitHub defaults

GraphQL's built-in scalars map to ordinary Go values:

| GraphQL        | Go        |
|----------------|-----------|
| `Int`          | `int`     |
| `Float`        | `float64` |
| `String`, `ID` | `string`  |
| `Boolean`      | `bool`    |

Nullable named values generate as pointers by default. Use
`@octoqlgen(pointer: false)` on an argument or selected field when its zero
value should represent GraphQL null. octoqlgen includes bindings for common
GitHub scalars; add a binding for unknown custom scalars. See the
[configuration reference](docs/octoqlgen.yaml) and
[directive reference](docs/octoqlgen_directive.graphql) for scalar bindings,
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

## Reference

- [Annotated `octoqlgen.yaml` reference](docs/octoqlgen.yaml)
- [`@octoqlgen` directive reference](docs/octoqlgen_directive.graphql)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)
