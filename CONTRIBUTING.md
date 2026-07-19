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

Run focused package tests and lint while developing. Run the full shuffled suite
for repository-wide module, generator entrypoint, or release changes.

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

## Project guidance

- The root [README](README.md) is the primary user guide.
- [AGENTS.md](AGENTS.md) records architecture and repository conventions.
- [docs/design.md](docs/design.md) preserves historical design rationale.
- Report vulnerabilities through the [security policy](docs/SECURITY.md).
- Follow the [code of conduct](docs/CODE_OF_CONDUCT.md).

Project history remains in Git. The repository does not maintain a changelog.
Release publication is disabled; contributors may validate local snapshots but
must not publish artifacts.
