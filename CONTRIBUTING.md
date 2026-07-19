# Contributing to octoql

Contributions are welcome. For substantial changes, open an issue first so the
approach can be discussed before implementation.

## Development

Use `script/fmt`, `script/test`, `script/lint`, and `script/generate --check`
to work with the repository. Generator changes should update the existing
snapshot tests as needed. Use `UPDATE_SNAPS=true go test ./generate` for
generator snapshots, or `UPDATE_SNAPS=true go test ./...` when checked-in
generated integration output also changes. CLI usage, configuration formatting,
and diagnostics use inline snapshots. Generated Go and JSON output stays in
external snapshots because the Go artifacts are compiled from those files.
To remove obsolete external generator snapshots, run:

```sh
rm -rf generate/testdata/snapshots
UPDATE_SNAPS=true go test ./generate
```

Review the recreated files, then run `go test ./generate` normally.

## Scripts

- `script/fmt` formats Go and shell source.
- `script/generate` runs generators, while `--check` verifies no generated
  output is stale. Typically `--check` should only be used in CI. Locally you
  can run `script/generate` and commit any changes.
- `script/lint` runs Go and shell linters.
- `script/test` runs the Go test suite with `-shuffle=on` to detect order
  dependencies.
