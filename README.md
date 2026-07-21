[![Go Reference](https://pkg.go.dev/badge/github.com/willabides/octoql.svg)](https://pkg.go.dev/github.com/willabides/octoql)
[![Test Status](https://github.com/willabides/octoql/actions/workflows/ci.yaml/badge.svg)](https://github.com/willabides/octoql/actions)
[![Contributor Covenant](https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg)](docs/CODE_OF_CONDUCT.md)

# octoql

octoql generates type-safe Go clients and typed test handlers for GitHub-shaped
GraphQL APIs. It validates queries and mutations against a pinned schema, then
generates Go types and helpers backed by a small root runtime package.

octoql is a standalone project derived from
[Khan/genqlient](https://github.com/Khan/genqlient). See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for exact source pins and
attribution.

## Requirements and installation

octoql requires Go 1.26 or newer. Add the runtime and pin `octoqlgen` as a Go
tool dependency in the module that owns the generated client:

```sh
go get github.com/willabides/octoql
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

Add the JSON Schema directive to the generated `octoqlgen.yaml` for editor
completion and validation. The initialized schema configuration has this form:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/WillAbides/octoql/main/octoqlgen.schema.yaml

schema:
  path: .octoql/schema.graphql
  sha256: c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18
  source:
    repository: github/docs
    path: src/graphql/data/fpt/schema.docs.graphql
    revision: 45d83f459620340069df7c375a8867be62616d61
operations:
  - graphql/**/*.graphql
generated: internal/githubapi/generated.go
```

Repository checkouts can use
`# yaml-language-server: $schema=./octoqlgen.schema.yaml` instead. All paths and
globs are relative to `octoqlgen.yaml`. See
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
client := octoql.NewClient("https://api.github.com/graphql", nil)
err := client.SetBearerToken(os.Getenv("GITHUB_TOKEN"))
if err != nil {
	return err
}

response, err := githubapi.GetRepository(
	ctx,
	client,
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

Pass a different endpoint to `octoql.NewClient` for GHES, a proxy, or an
`httptest.Server`. Pass nil as the HTTP client to use `http.DefaultClient`.

For basic authentication or another authentication scheme, configure the
`http.Client` or `http.RoundTripper` passed to `NewClient`.

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
`*octoql.ResponseError`. GraphQL errors, rate limits, and partial data are
independent error facets, so use `errors.AsType` for each detail your
application needs. The client never retries automatically. See the
[root runtime API](https://pkg.go.dev/github.com/willabides/octoql) for error
and rate-limit details.

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

client := octoql.NewClient(server.URL, server.Client())
response, err := githubapi.GetRepository(
	t.Context(),
	client,
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

- [Root runtime API](https://pkg.go.dev/github.com/willabides/octoql)
- [Annotated `octoqlgen.yaml` reference](docs/octoqlgen.yaml)
- [`@octoqlgen` directive reference](docs/octoqlgen_directive.graphql)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)
