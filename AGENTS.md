# Agent guide

## Project boundaries

- `octoql` is a standalone project derived from Khan/genqlient. Preserve
  attribution in `LICENSE` and `THIRD_PARTY_NOTICES.md`.
- The module path is `github.com/willabides/octoql`, with Go version `1.26.0`.
- Reusable runtime APIs belong in the root `octoql` package. The generator
  command is `cmd/octoqlgen`.
- New source files use `Copyright (c) 2026 octoql contributors` and
  `SPDX-License-Identifier: MIT`.
- Do not update `docs/CHANGELOG.md` unless a task explicitly requires it.

## Development

- Use Kong declarative structs and `Run` methods for CLI commands. Keep parsing,
  dependency construction, and command execution separately testable.
- Preserve generated-file notices. Generated Go output must identify
  `octoqlgen` and include `SPDX-License-Identifier: MIT`.
- Update generator snapshots when generation behavior or templates change:
  `UPDATE_SNAPSHOTS=1 go test ./generate`.
- Run targeted tests and lint for affected packages. Run `go test ./...` for
  repository-wide module or entrypoint changes.
- Use `script/generate --check` to verify generated output. Do not run broad
  audit targets when targeted validation covers the change.

## Tooling and release safety

- Edit `.bindown.yaml` only through `script/bindown`; do not edit it directly.
  Update checksums with `script/bindown checksums sync`.
- Keep release publication disabled. Snapshot and configuration checks are
  allowed, but do not add publication credentials, triggers, or jobs.
- Remove generated local tool and build artifacts such as `bin/`, `.bindown/`,
  `dist/`, and root-level binaries before finishing work.

## Git and pull requests

- Do not create commits unless explicitly authorized. Do not add
  `Co-authored-by` or `Copilot-Session` trailers.
- Draft PR descriptions should describe behavior, rationale, imported-baseline
  dependency, migration role, attribution, and scoped plan. Do not add a
  validation section unless requested.
