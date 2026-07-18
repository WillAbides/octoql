# Contributing to octoql

Contributions are welcome. For substantial changes, open an issue first so the
approach can be discussed before implementation.

## Development

Use `script/fmt`, `script/test`, `script/lint`, and `script/generate --check`
to work with the repository. Generator changes should update the existing
snapshot tests as needed.

## Scripts

- `script/cibuild` runs the checks used by CI, including a release snapshot.
- `script/fmt` formats Go and shell source.
- `script/generate` runs generators, while `--check` verifies no generated
  output is stale.
- `script/lint` runs Go and shell linters.
- `script/test` runs the Go test suite.
