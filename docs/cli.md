# CLI reference

`octoqlgen` generates GraphQL client code for a given schema and set of
operations.

Run it with an explicit release version so the generator does not become a
dependency of the module that owns the generated client:

```sh
go run github.com/willabides/octoql/cmd/octoqlgen@<version> <command>
```

Replace `<version>` with a release tag from the
[releases page](https://github.com/WillAbides/octoql/releases). The examples
below shorten that invocation to `octoqlgen`.

Generation itself normally runs from a `go:generate` directive. See the
[README](../README.md#generate-a-client).

## Global flags

| Flag           | Effect                      |
| -------------- | --------------------------- |
| `-h`, `--help` | Show context-sensitive help |
| `--version`    | Show version information    |

## `init`

Create an octoqlgen configuration and fetch its schema.

```sh
octoqlgen init
octoqlgen init --schema-version ghec
octoqlgen init --schema-version ghes-3.21
```

| Flag                       | Effect                                                                      |
| -------------------------- | --------------------------------------------------------------------------- |
| `--config=PATH`            | Path for the new octoqlgen configuration                                    |
| `--schema-version=VERSION` | GitHub Docs schema version: `fpt`, `ghec`, or `ghes-X.Y`. Defaults to `fpt` |

This resolves and fetches the requested GitHub Docs schema, then writes a
configuration pinning its commit revision and SHA-256 digest. It also creates
`.octoql/.gitignore`.

GitHub authentication must be available through `GH_TOKEN`, `GITHUB_TOKEN`, or
the `gh` CLI.

## `generate`

Generate GraphQL client code.

```sh
octoqlgen generate --config ../../octoqlgen.yaml
```

| Flag            | Effect                                  |
| --------------- | --------------------------------------- |
| `--config=PATH` | Path to an octoqlgen configuration file |

Generation verifies or fetches the configured schema before it writes code, so
a separate `schema fetch` is not required. When `test_handler` is configured, a
typed test handler is generated from the same operation plan.

## `schema fetch`

Fetch or verify a pinned GraphQL schema.

```sh
octoqlgen schema fetch
octoqlgen schema fetch --output schema.graphql
```

| Flag                  | Effect                                                                |
| --------------------- | --------------------------------------------------------------------- |
| `--config=PATH`       | Path to an octoqlgen configuration file. Defaults to `octoqlgen.yaml` |
| `-o`, `--output=PATH` | Write the exact schema bytes to a file instead of stdout              |

This verifies an existing schema file against its configured digest, or fetches
a missing remote file.

## `schema update`

Fetch the latest configured GitHub schema and update its revision and checksum.

```sh
octoqlgen schema update
git diff -- octoqlgen.yaml
```

| Flag            | Effect                                  |
| --------------- | --------------------------------------- |
| `--config=PATH` | Path to an octoqlgen configuration file |

This fetches the latest version of the configured repository path from its
default branch, validates and writes it, then updates `schema.source.revision`
and `schema.sha256` in the configuration. Review that diff before regenerating.

Run schema updates serially.
