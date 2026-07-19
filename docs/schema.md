# Configuring octoqlgen to use your GraphQL schema

This document describes schema configuration for octoqlgen. For the complete
configuration, see the [`octoql.yaml` reference](octoql.yaml).

## Schema materialization

`schema.path` is the only schema file used during generation. For local-only
schemas, omit `schema.source` and place SDL at that path. For remote schemas,
configure exactly one pinned `github_docs`, `github_repository`, or `url` source
and the expected `schema.sha256`.

Run `go tool octoqlgen schema materialize` to verify an existing file or fetch a
missing remote file. `go tool octoqlgen generate` performs the same verification
and materialization before generation, so it never generates against unverified
bytes. Schema update behavior remains explicit through
`go tool octoqlgen schema update`.

All schema and output paths are relative to `octoql.yaml`. Materialization never
rewrites the configuration.

## Scalars

GraphQL [defines][spec#scalar] five standard scalar types, which genqlient automatically maps to the following Go types:

| GraphQL type | Go type   |
|--------------|-----------|
| `Int`        | `int`     |
| `Float`      | `float64` |
| `String`     | `string`  |
| `Boolean`    | `bool`    |
| `ID`         | `string`  |

octoqlgen also provides defaults for the custom scalars in GitHub's public
schema:

| GraphQL type | Go type |
|--------------|---------|
| `DateTime`, `PreciseDateTime`, `GitTimestamp` | `time.Time` |
| `CustomPropertyValue` | `encoding/json.RawMessage` |
| `Base64String`, `BigInt`, `Date` | `string` |
| `GitObjectID`, `GitRefname`, `GitSSHRemote` | `string` |
| `HTML`, `URI`, `X509Certificate` | `string` |

For other custom scalars, or to override any standard or GitHub mapping, use
the `bindings` option in [`octoql.yaml`](octoql.yaml). Explicit bindings
always take precedence.

[spec#scalar]: https://spec.graphql.org/draft/#sec-Scalars

### Custom scalars

Schemas can define custom scalars beyond the GitHub defaults. Tell octoqlgen
what Go types to use for those through `bindings` in `octoql.yaml`, for
example:

```yaml
bindings:
  DateTime:
    type: time.Time
```

The schema should define how custom scalars are encoded in JSON; you'll need to make sure the given type has the appropriate `MarshalJSON`/`UnmarshalJSON` or `json` tags. When using a third-party type, like `time.Time`, you can alternately define separate functions:

```yaml
bindings:
  DateTime:
    type: time.Time
    marshaler: "github.com/your/package.MarshalDateTime"
    unmarshaler: "github.com/your/package.UnmarshalDateTime"
```

See octoql's integration tests for a full example:
[types](../internal/testutil/types.go) and
[config](../internal/integration/octoql.yaml).

To leave a custom scalar as raw JSON, map it to `encoding/json.RawMessage`:

```yaml
bindings:
  JSON:
    type: encoding/json.RawMessage
```

### Integer sizing


The GraphQL spec officially defines the `Int` type to be a [signed 32-bit integer](https://spec.graphql.org/draft/#sec-Int).  GraphQL clients and servers vary wildly in their enforcement of this; for example:
- [Apollo Server](https://github.com/apollographql/apollo-server/) explicitly checks that integers are at most 32 bits
- [gqlgen](https://github.com/99designs/gqlgen) by default allows any integer that fits in `int` (i.e. 64 bits on most platforms)
- [Apollo Client](https://github.com/apollographql/apollo-client) doesn't check (but implicitly is limited to 53 bits by JavaScript)
- [shurcooL/graphql](https://github.com/shurcooL/graphql) requires integers be passed as a `graphql.Int`, defined to be an `int32`

By default, genqlient maps GraphQL `Int`s to Go's `int`, meaning that on 64-bit systems there's no client-side restriction. This is convenient for most use cases, but means the client won't prevent you from passing a 64-bit integer to a server that will reject or truncate it.

If you prefer to limit integers to `int32`, set a binding in `octoql.yaml`:

```yaml
bindings:
  Int:
    type: int32
```

Or, you can bind it to any other type, perhaps one with size-checked constructors, similar to a custom scalar.

If your schema has a big-integer type, you can bind that similarly to other custom scalars:
```yaml
bindings:
  BigInt:
    type: math/big # or int64, or string, etc.
    # if you need custom marshaling
    marshaler: "github.com/path/to/package.MarshalBigInt"
    unmarshaler: "github.com/path/to/package.UnmarshalBigInt"
```

## Extensions

Some schemas and servers make use of GraphQL extensions. Generated query and
mutation helpers always expose top-level values through `response.Extensions`.
Each GraphQL error retains its own values through `error.Extensions`.

The legacy `use_extensions` option was a no-op and is not accepted in
`octoql.yaml`. Remove it when migrating configuration.

## Hasura, Dgraph, and other generated schemas

Some GraphQL tools, like Hasura and Dgraph, generate large schemas automatically from non-GraphQL data (like database schemas). These schemas tend to be quite large and complex, and often run into trouble with GraphQL. See [#272](https://github.com/Khan/genqlient/issues/272) for discussion of how to use these tools with genqlient.
