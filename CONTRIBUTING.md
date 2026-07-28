# Contributing to octoql

Contributions are welcome. For substantial changes, open an
[issue](https://github.com/WillAbides/octoql/issues/new/choose) first so the
approach can be discussed.

## Prerequisites

- Go 1.26 or newer
- Git

Clone the repository, then download the module dependencies:

```sh
git clone https://github.com/WillAbides/octoql.git
cd octoql
go mod download
```

Repository scripts install their pinned development tools on demand.

## Development commands

```sh
script/fmt
go test -shuffle=on ./...
script/lint
script/generate
```

Run focused package tests while developing, for example
`go test -shuffle=on ./internal/generate`. `script/lint` always covers the whole
module; to lint one package, install the pinned tools once with
`script/bindown -q install golangci-lint` and then run
`bin/golangci-lint run ./internal/generate/...`. Run the full shuffled suite for
repository-wide module, generator entrypoint, or release changes.

`script/generate --check` is CI-only. Locally, run `script/generate`, then
inspect all generated changes:

```sh
git status --short
git diff --stat
git diff
```

Commit only intended generated output. Do not commit local `bin/`, `.bindown/`,
`dist/`, root binaries, or the materialized `.octoql` schema.

## Snapshot tests

Use inline go-snaps snapshots for compact help, diagnostics, and small values.
External snapshots are reserved for generated files that are compiled or are
otherwise impractical to review inline.

Update only the affected package:

```sh
UPDATE_SNAPS=true go test ./internal/generate
go test -shuffle=on ./internal/generate
```

If checked-in generated integration output also changes, update the packages
that own those fixtures. Review every snapshot change. Snapshot updates are a
contributor workflow and are not required to use octoql.

When obsolete external generator snapshots must be removed, recreate them
explicitly:

```sh
rm -rf internal/generate/testdata/snapshots
UPDATE_SNAPS=true go test ./internal/generate
go test -shuffle=on ./internal/generate
```

Do not add global snapshot cleanup, sorting, or `TestMain` lifecycle behavior.

## Documentation

`docs/configuration.md` is generated from `octoqlgen.schema.yaml` by
`internal/configdocgen`. Edit the schema, not the Markdown:

- keep `description` terse and single-line, since it is what editors show on
  hover through the `# yaml-language-server: $schema=` comment
- put long-form Markdown prose in `x-doc`, which is available on the schema
  root, on each `$defs` entry, and on every property

Generation fails when a property has no `x-doc`, so a new configuration option
cannot ship undocumented. Run `script/generate` after changing the schema.

`docs/directive.md` and `docs/cli.md` are handwritten. Update `docs/cli.md` from
actual `--help` output when CLI flags change.

## Project guidance

- The root [README](README.md) is the primary user guide.
- [docs/](docs/README.md) holds the configuration, directive, and CLI
  references.
- [AGENTS.md](AGENTS.md) records architecture and repository conventions.
- Report vulnerabilities through the [security policy](docs/SECURITY.md).
- Follow the [code of conduct](docs/CODE_OF_CONDUCT.md).

Project history remains in Git. The repository does not maintain a changelog.
Release publication is disabled; contributors may validate local snapshots but
must not publish artifacts.
