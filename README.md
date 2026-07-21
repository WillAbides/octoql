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

## Contents

- [Requirements and installation](#requirements-and-installation)
- [Generate a client](#generate-a-client)
- [Schema sources and updates](#schema-sources-and-updates)
- [Call the generated client](#call-the-generated-client)
- [Runtime responses and errors](#runtime-responses-and-errors)
- [Generated types and GitHub defaults](#generated-types-and-github-defaults)
- [Typed test handlers](#typed-test-handlers)
- [Reference](#reference)

## Requirements and installation

octoql requires Go 1.26 or newer. Add the runtime and pin `octoqlgen` as a Go
tool dependency in the module that owns the generated client:

```sh
go get github.com/willabides/octoql
go get -tool github.com/willabides/octoql/cmd/octoqlgen
```

Run the pinned tool with `go tool octoqlgen`. To install a standalone binary
from a release archive with [bindown](https://github.com/WillAbides/bindown):

```sh
bindown template-source add octoql https://github.com/WillAbides/octoql/releases/latest/download/bindown.yaml
bindown dependency add octoqlgen --source octoql
```

To install the standalone binary from source instead, use an explicit version or
commit:

```sh
go install github.com/willabides/octoql/cmd/octoqlgen@<version-or-commit>
```

The Go tool dependency remains the recommended installation because the runtime
and generator then resolve from the same module version.

## Generate a client

Initialize a project:

```sh
go tool octoqlgen init
```

This creates `octoqlgen.yaml` and `.octoql/.gitignore`. It does not fetch a
schema. The generated config uses the gitignored `.octoql/schema.graphql` path,
`graphql/**/*.graphql` for operations, and
`internal/githubapi/generated.go` for output.

Add the JSON Schema directive to `octoqlgen.yaml` for editor completion and
validation, then configure the GitHub Docs schema:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/WillAbides/octoql/main/octoqlgen.schema.yaml

schema:
  path: .octoql/schema.graphql
  sha256: c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18
  source:
    github_docs:
      version: fpt
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

Materialize or verify the configured schema, then generate:

```sh
go tool octoqlgen schema materialize
go tool octoqlgen generate
```

Generation performs the same schema verification or materialization before it
writes code. Query and mutation operation names become generated helper names,
so use an uppercase name when the helper must be exported. octoql does not
support GraphQL subscriptions, and `octoqlgen` rejects subscription operations.

`operations` may also select Go files. A string literal beginning with
`# @octoqlgen` is parsed as an operation:

```go
const getViewerQuery = `# @octoqlgen
query GetViewer {
  viewer {
    login
  }
}
`
```

Use the `@octoqlgen` comment quasi-directive for per-operation options such as
`pointer`, `omitempty`, `flatten`, `bind`, and `typename`. See the
[directive reference](docs/octoqlgen_directive.graphql). It is written in a
comment because server-defined GraphQL directives cannot configure the client
generator.

## Schema sources and updates

`schema.path` is always the schema used for generation. Keep it in the
gitignored `.octoql` directory when the source is remote. A local schema needs
no source:

```yaml
schema:
  path: schema/github.graphql
```

For a local schema, `sha256` is optional. When present, materialization verifies
it. Remote sources require a SHA-256 digest and, for GitHub sources, a full
commit SHA. Configure exactly one source variant.

### GitHub Docs

Use `fpt`, `ghec`, or a GHES version such as `ghes-3.17`:

```yaml
schema:
  path: .octoql/schema.graphql
  sha256: "<64-character-schema-sha256>"
  source:
    github_docs:
      version: fpt
      revision: "<full-github-docs-commit-sha>"
```

### GitHub repository or GHES

Pin a schema file in a GitHub repository. Add `host` for GitHub Enterprise
Server:

```yaml
schema:
  path: .octoql/schema.graphql
  sha256: "<64-character-schema-sha256>"
  source:
    github_repository:
      repository: octo-org/graphql-schema
      revision: "<full-commit-sha>"
      path: schema/github.graphql
      host: github.example.com
```

Omit `host` for `github.com`.

### Immutable URL

```yaml
schema:
  path: .octoql/schema.graphql
  sha256: "<64-character-schema-sha256>"
  source:
    url: https://schemas.example.com/github/<revision>/schema.graphql
```

Prefer a URL whose content is immutable. Materialization refuses bytes that do
not match the configured digest and validates the GraphQL SDL before writing
`schema.path`.

### Authentication, verification, and updates

For GitHub Docs and repository sources, authentication is discovered in this
order:

1. `GH_TOKEN`
2. `GITHUB_TOKEN`
3. `gh auth token --hostname <host>`

GitHub GraphQL requires authentication, including for public repositories.
Materialization and updates fail with guidance when none of these token sources
is available.

`schema materialize` verifies an existing file or fetches a missing remote file.
It never changes `octoqlgen.yaml`:

```sh
go tool octoqlgen schema materialize
```

`schema update` fetches and validates the current remote source, then updates
the materialized schema and its configuration pin: `sha256` for every remote
source and the GitHub revision for GitHub-backed sources. Run schema updates
serially.

```sh
go tool octoqlgen schema update
git diff -- octoqlgen.yaml
go tool octoqlgen generate
```

The `.octoql` schema normally remains ignored while the reviewed pin in
`octoqlgen.yaml` is committed. Use `--config PATH` with materialize, update, or
generate when the config has another name or location.

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

`SetBearerToken` accepts tokens containing letters, digits, `-`, `.`, `_`, `~`,
`+`, or `/`, with optional trailing `=` padding. For basic authentication or
another authentication scheme, configure the `http.Client` or
`http.RoundTripper` passed to `NewClient`.

## Runtime responses and errors

Generated helpers return a pointer to the concrete operation response and an
error. On success, GraphQL data is available directly:

```go
response, err := githubapi.GetRepository(ctx, client, githubapi.GetRepositoryVariables{
	Owner: owner,
	Name:  name,
	First: 10,
})
if err == nil {
	fmt.Println(response.Repository.NameWithOwner)
}
```

The response is nil when the error is non-nil. Sometimes GitHub will return
partial data with an error. Use `errors.AsType` to check for partial data:

```go
_, err := githubapi.GetRepository(ctx, client, githubapi.GetRepositoryVariables{
	Owner: owner,
	Name:  name,
	First: 10,
})
if err != nil {
	partialErr, ok := errors.AsType[*githubapi.GetRepositoryPartialDataError](err)
	if ok {
		fmt.Printf("partial repository: %+v\n", partialErr.PartialData().Repository)
	}
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

Partial data and errors, per-error extensions, headers, status, and rate limits
are configurable:

```go
handler.ExpectGetRepository(variables).
	WithOptions(
		githubapitest.WithStatus(http.StatusForbidden),
		githubapitest.WithHeader("X-GitHub-Request-ID", "request-123"),
		githubapitest.WithPrimaryRateLimit(octoql.RateLimit{
			Limit:     5000,
			Remaining: 0,
			Used:      5000,
			Resource:  "graphql",
		}),
	).
	RespondDataAndErrors(
		partialData,
		octoql.Error{
			Type:       "FORBIDDEN",
			Message:    "one field is unavailable",
			Extensions: map[string]any{"code": "missing"},
		},
	)
```

`WithHeaders` replaces multiple headers. `WithSecondaryRateLimit` writes
`Retry-After`. `RespondError` returns errors without data, and `Handle` provides
complete control of the `http.ResponseWriter`.

## Reference

- [Root runtime API](https://pkg.go.dev/github.com/willabides/octoql)
- [Annotated `octoqlgen.yaml` reference](docs/octoqlgen.yaml)
- [`@octoqlgen` directive reference](docs/octoqlgen_directive.graphql)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)
