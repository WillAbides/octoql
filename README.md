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
attribution, including the bounded gqltesthandler input used by typed
test-handler generation.

## Contents

- [Requirements and installation](#requirements-and-installation)
- [Generate a client](#generate-a-client)
- [Schema sources and updates](#schema-sources-and-updates)
- [Call the generated client](#call-the-generated-client)
- [Runtime responses and errors](#runtime-responses-and-errors)
- [Handwritten operations](#handwritten-operations)
- [Generated types and GitHub defaults](#generated-types-and-github-defaults)
- [Typed test handlers](#typed-test-handlers)
- [Migration from genqlient or earlier octoql config](#migration-from-genqlient-or-earlier-octoql-config)
- [Contributing](#contributing)
- [Reference and project policies](#reference-and-project-policies)

## Requirements and installation

octoql requires Go 1.26 or newer. Add the runtime and pin `octoqlgen` as a Go
tool dependency in the module that owns the generated client:

```sh
go get github.com/willabides/octoql
go get -tool github.com/willabides/octoql/cmd/octoqlgen
```

Run the pinned tool with `go tool octoqlgen`. To install a standalone binary
from source instead, use an explicit version or commit:

```sh
go install github.com/willabides/octoql/cmd/octoqlgen@<version-or-commit>
```

octoql does not currently publish prebuilt release archives. The Go tool
dependency is the recommended installation because the runtime and generator
then resolve from the same module version.

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
validation, then configure a local or remote schema:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/WillAbides/octoql/main/octoqlgen.schema.yaml

schema:
  path: .octoql/schema.graphql
operations:
  - graphql/**/*.graphql
generated: internal/githubapi/generated.go
```

Repository checkouts can use
`# yaml-language-server: $schema=./octoqlgen.schema.yaml` instead. All paths and
globs are relative to `octoqlgen.yaml`. The committed
[`octoqlgen.schema.yaml`](octoqlgen.schema.yaml) is the canonical structural
schema, and the annotated
[`docs/octoqlgen.yaml`](docs/octoqlgen.yaml) explains every option.

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
`# @genqlient` is parsed as an operation:

```go
const getViewerQuery = `# @genqlient
query GetViewer {
  viewer {
    login
  }
}
`
```

Use the inherited `@genqlient` comment directive for per-operation options such
as `pointer`, `omitempty`, `flatten`, `bind`, and `typename`. See the
[directive reference](docs/genqlient_directive.graphql).

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
4. anonymous access when no token is available

`schema materialize` verifies an existing file or fetches a missing remote file.
It never changes `octoqlgen.yaml`:

```sh
go tool octoqlgen schema materialize
```

`schema update` fetches the current remote source, validates it, and atomically
updates the materialized schema together with `sha256` and the GitHub revision:

```sh
go tool octoqlgen schema update
git diff -- octoqlgen.yaml
go tool octoqlgen generate
```

The `.octoql` schema normally remains ignored while the reviewed pin in
`octoqlgen.yaml` is committed. Use `--config PATH` with materialize, update, or
generate when the config has another name or location.

## Call the generated client

Authentication belongs in the supplied `http.Client` or `http.RoundTripper`.
This transport clones each request before adding a GitHub bearer token:

```go
type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (transport tokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

httpClient := &http.Client{
	Transport: tokenTransport{
		token: os.Getenv("GITHUB_TOKEN"),
		base:  http.DefaultTransport,
	},
}
client := octoql.NewClient("https://api.github.com/graphql", httpClient)

response, err := githubapi.GetRepository(
	ctx,
	client,
	"octo-org",
	"octo-repo",
	10,
)
if err != nil {
	return err
}
fmt.Println(response.Data.Repository.NameWithOwner)
```

Pass a different endpoint to `octoql.NewClient` for GHES, a proxy, or an
`httptest.Server`. A nil HTTP client uses `http.DefaultClient`.

## Runtime responses and errors

Generated helpers return `*octoql.Response[T]`. Its fields are:

- `Data`, including partial GraphQL data
- `Errors`, the decoded `octoql.Errors`
- `Extensions`, top-level GraphQL extensions
- `HTTP`, containing status, cloned headers, `X-GitHub-Request-ID`, and parsed
  rate-limit metadata

Each `*octoql.Error` retains `Type`, `Message`, `Path`, `Locations`, and its own
`Extensions`. `Error.Type` is an open string type so new GitHub values remain
available without a runtime update.

Once an HTTP response exists, octoql returns a non-nil response even when it
also returns an error. Inspect partial data before deciding whether it is usable:

```go
response, err := githubapi.GetRepository(ctx, client, owner, name, 10)

var graphqlErrors octoql.Errors
if errors.As(err, &graphqlErrors) {
	fmt.Printf("partial repository: %+v\n", response.Data.Repository)
	for _, graphqlError := range graphqlErrors {
		fmt.Printf("%s: %s at %s\n",
			graphqlError.Type,
			graphqlError.Message,
			graphqlError.Path,
		)
	}
}

httpError, ok := errors.AsType[*octoql.HTTPError](err)
if ok {
	fmt.Printf("status=%d request_id=%s\n",
		httpError.HTTP.StatusCode,
		httpError.HTTP.RequestID,
	)
}
```

A non-2xx response is represented by `*octoql.HTTPError`, which retains the
HTTP metadata, raw body, decoded GraphQL errors, and any read or decode cause.
Failures before an HTTP response, such as transport errors, return a nil
response.

Primary and secondary GitHub limits wrap the underlying GraphQL or HTTP error
in `*octoql.RateLimitError`. Primary limits are detected from
`X-RateLimit-Remaining: 0`. Secondary limits are detected from `Retry-After` on
HTTP 200 or 403 responses:

```go
rateLimitError, ok := errors.AsType[*octoql.RateLimitError](err)
if ok {
	fmt.Printf("kind=%s remaining=%d retry_at=%s\n",
		rateLimitError.Kind,
		rateLimitError.RateLimit.Remaining,
		rateLimitError.RateLimit.RetryAt,
	)
}
```

octoql never retries or sleeps automatically. Apply retry policy in the calling
application after considering operation safety, `Retry-After`, and the parsed
reset time.

## Handwritten operations

Generated helpers call the same root runtime available to handwritten clients.
Supply the endpoint, operation name, query document, and a JSON-encodable
variables value:

```go
type viewerData struct {
	Viewer struct {
		Login string `json:"login"`
	} `json:"viewer"`
}

response, err := octoql.Do[viewerData](
	ctx,
	octoql.NewClient("https://api.github.com/graphql", httpClient),
	octoql.Operation{
		Name:  "Viewer",
		Query: "query Viewer { viewer { login } }",
	},
	nil,
)
if err != nil {
	return err
}
fmt.Println(response.Data.Viewer.Login)
```

The generated client and handwritten `octoql.Do` expose the same response,
error, extension, request ID, header, and rate-limit behavior.

## Generated types and GitHub defaults

GraphQL's built-in scalars map to ordinary Go values:

| GraphQL | Go |
| --- | --- |
| `Int` | `int` |
| `Float` | `float64` |
| `String`, `ID` | `string` |
| `Boolean` | `bool` |

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
unmarshal error. Generated JSON first-pass fences are unexported implementation
details that preserve `encoding/json` method-promotion behavior. Do not refer to
them from application code.

## Typed test handlers

Generate a typed `http.Handler` from the same immutable operation plan as the
client:

```yaml
generated: internal/githubapi/generated.go
test_handler:
  generated: internal/githubapitest/generated.go
  types: client
```

`types: client` is the default. It imports the generated client package and
aliases operation types, so handler response values are directly assignable to
client types.

Use `types: local` to generate distinct wire-equivalent types in the handler
package without importing the client:

```yaml
test_handler:
  generated: internal/githubapitest/generated.go
  types: local
```

Local handler values are intentionally not assignable to client types. Local
mode also rejects reachable bindings or marshal helpers owned by the generated
client package because those references would recreate the dependency.

After `go tool octoqlgen generate`, each uppercase query or mutation has
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
	variables.Owner,
	variables.Name,
	variables.First,
)
require.NoError(t, err)
require.Equal(t, "octo-org/octo-repo", response.Data.Repository.NameWithOwner)
```

An expectation defaults to one call. Pass `Times(n)` to require exactly `n`,
`MinTimes(n)` to set a minimum, or `MinTimes(0)` to create an unlimited stub.
`Default<Operation>` is an unlimited fallback. Cleanup verifies unmet
expectations, and expectation state is safe for concurrent requests.

Partial data and errors, extensions, headers, status, and rate limits are
configurable:

```go
handler.ExpectGetRepository(variables).
	WithOptions(
		githubapitest.WithStatus(http.StatusForbidden),
		githubapitest.WithHeader("X-GitHub-Request-ID", "request-123"),
		githubapitest.WithExtensions(map[string]any{"trace": "abc"}),
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
- Import the root runtime as `github.com/willabides/octoql`. There is no
  `graphql` runtime package or public `generate` package. Invoke
  `github.com/willabides/octoql/cmd/octoqlgen`.
- Remove `use_extensions`. It was a no-op. Top-level
  `Response.Extensions` and per-error `Error.Extensions` are always retained.
- octoql does not support subscriptions. Convert supported operations to queries
  or mutations before migration.
- Explicit scalar bindings override octoql's GitHub defaults.
- Abstract selections now use `OctoqlOther` by default. Set
  `omit_unreferenced_implementations: false` temporarily when migrating an
  exhaustive type switch.
- Upgrade the runtime and generator together, then regenerate checked-in code.
  The single module intentionally keeps their versions synchronized.

## Contributing

Snapshot maintenance is a contributor workflow, not part of installing or using
octoql. Update go-snaps output only when the behavior or generated artifacts
covered by an affected test intentionally change, review every update, and run
the same focused test normally afterward. See [CONTRIBUTING.md](CONTRIBUTING.md)
for commands and repository conventions.

## Reference and project policies

- [Root runtime API](https://pkg.go.dev/github.com/willabides/octoql)
- [Annotated `octoqlgen.yaml` reference](docs/octoqlgen.yaml)
- [`@genqlient` directive reference](docs/genqlient_directive.graphql)
- [Historical design rationale](docs/design.md)
- [Runnable example](example)
- [Contributing](CONTRIBUTING.md)
- [Security policy](docs/SECURITY.md)
- [Code of conduct](docs/CODE_OF_CONDUCT.md)
- [License](LICENSE)

The root README is the primary user guide. The `docs/` directory is limited to
specialized references and project policies. Project history remains available
in Git.
