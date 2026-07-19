# Agent guide

## Project boundaries

- `octoql` is a standalone project derived from Khan/genqlient. Preserve
  attribution in `LICENSE` and `THIRD_PARTY_NOTICES.md`.
- The module path is `github.com/willabides/octoql`, with Go version `1.26.0`.
- Reusable runtime APIs belong in the root `octoql` package. The generator
  command is `cmd/octoqlgen`.
- Do not update `docs/CHANGELOG.md` unless a task explicitly requires it.
- `octoqlgen.yaml` is the only user-facing generator configuration. Do not restore
  `genqlient.yaml` parsing, discovery, compatibility adapters, or config merging.
- octoql does not support GraphQL subscriptions. Preserve top-level
  `Response.Extensions` and per-error `Error.Extensions`; do not restore the
  removed no-op `use_extensions` option.

## Development

- Write tests first using a red/green strategy where possible. It's also useful
  to commit the failing test first.
- Use Kong declarative structs and `Run` methods for CLI commands. Keep parsing,
  dependency construction, and command execution separately testable. Put all
  Kong metadata in one consolidated `kong:"..."` tag; do not use separate
  metadata tags such as `cmd`, `help`, `name`, `type`, or `default`.
- Use gopls first for Go symbols, references, package APIs, renames, and
  diagnostics. Follow existing Go style and repository patterns rather than
  introducing parallel abstractions.
- Assign variables, including errors, before conditionals rather than using
  initializer clauses in `if` statements.
- Avoid `else` in handwritten Go. Prefer early exits, `switch`, or
  default-then-override. Generated Go and templates are exempt.
- Test helpers that take `*testing.T` use `t.Context()` internally. Use
  `t.Helper()` only for assertion helpers.
- Use `testify/require` for test prerequisites and `testify/assert` for
  non-fatal checks whenever they make tests clearer.
- The runtime config model is generated from `octoqlgen.schema.yaml` with the
  repository-pinned `script/jsonschematogo`; do not add handwritten user config
  structs.
- Typed test-handler generation is an optional renderer over the same immutable
  parsed and converted operation plan as client generation. Do not add a second
  config load, schema parse, operation parser, abstract-type analysis, or
  subscription path.
- Generated test handlers use `test_handler.types: client` by default to import
  and alias generated client types. `types: local` emits distinct operation
  types in the handler package without importing the client package.
- Both test-handler type strategies render from the same immutable generation
  plan and shared type renderer. Do not add a second config load, schema parse,
  operation parser, type conversion, or abstract-type analysis.
- Preserve destination-neutral binding and marshal-helper references in the
  shared plan, then resolve imports per renderer. Local handlers reject
  reachable references owned by the generated client package.
- Keep GitHub-focused generator fixtures and defaults. The pinned public GitHub
  schema is materialized on demand, remains ignored, and must not be committed.
- Do not add file-level copyright or SPDX headers to new Go files. Preserve
  project-level attribution in `LICENSE` and `THIRD_PARTY_NOTICES.md`, and
  preserve generated `Code generated ... DO NOT EDIT.` notices.
- Authentication belongs in the supplied `http.Client` or `http.RoundTripper`.
  Do not add automatic retry or sleep behavior.
- Run targeted tests and lint for affected packages. Run `go test ./...` for
  repository-wide module or entrypoint changes.
- Use `script/generate --check` to verify generated output. Do not run broad
  audit targets when targeted validation covers the change.

## Snapshot testing

- Use go-snaps inline snapshots first for compact help text, errors, and small
  values. Use external snapshots only for generated files that are compiled or
  otherwise impractical to review inline.
- Update affected snapshots with targeted
  `UPDATE_SNAPS=true go test ./path/to/package` runs.
- Do not add `TestMain`, automatic cleanup hooks, `Clean`, `Sort`, clean mode, or
  other global snapshot lifecycle behavior.
- When obsolete external snapshots must be removed, delete the relevant snapshot
  directory manually with `rm -rf`, regenerate it with a targeted
  `UPDATE_SNAPS=true` test, review every recreated file, then run the same test
  normally and confirm it leaves the worktree clean.
- Normalize nondeterministic values at the source so snapshots remain stable;
  do not hide nondeterminism with snapshot ordering or cleanup machinery.
- Test-handler generator coverage compiles checked-in generated fixtures and
  exercises expectation counts, defaults, cleanup verification, response
  controls, abstract types, and concurrent requests under `go test -race`.
- Treat `WillAbides/gqltesthandler` commit
  `0badc27d4cac3d21bc7e0ccad7842bad47763438` as bounded implementation input
  only. Keep its attribution in `THIRD_PARTY_NOTICES.md`; do not add its CLI,
  config, parser, or module dependency.

## Tooling and release safety

- Edit `.bindown.yaml` only through `script/bindown`; do not edit it directly.
  Update checksums with `script/bindown checksums sync`.
- Keep release publication disabled. Snapshot and configuration checks are
  allowed, but do not add publication credentials, triggers, or jobs.
- CI runs independent `test`, `lint`, `generate`, and `release` jobs.
- Remove generated local tool and build artifacts such as `bin/`, `.bindown/`,
  `dist/`, and root-level binaries before finishing work.

## Git and pull requests

- Do not create commits unless explicitly authorized. Do not add
  `Co-authored-by` or `Copilot-Session` trailers.
- Draft PR descriptions should describe behavior, rationale, imported-baseline
  dependency, migration role, attribution, and scoped plan. Do not add a
  validation section unless requested.
