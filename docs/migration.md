# Migrating generator configuration

octoql uses `octoql.yaml` as its only user-facing generator configuration.
Move inherited generator settings into the root of `octoql.yaml`, replace the
legacy scalar or list `schema` setting with `schema.path`, and invoke generation
with `octoqlgen generate --config PATH` when using a non-default path. Legacy
`genqlient.yaml` files are not discovered, parsed, merged, or translated.

All configured paths are relative to `octoql.yaml`: `schema.path`, `operations`,
`generated`, `export_operations`, and `test_handler.generated`. The generator
verifies or materializes `schema.path` before package inference and client
generation.

Abstract selections omit concrete Go structs that are not referenced by an
applicable inline or named fragment. Each selection gets an `OctoqlOther`
catch-all with the shared fields and `__typename`. Code that switches on every
schema implementation can temporarily restore the inherited behavior:

```yaml
omit_unreferenced_implementations: false
```

GitHub public-schema scalars have built-in mappings. `DateTime`,
`PreciseDateTime`, and `GitTimestamp` use `time.Time`. `CustomPropertyValue`
uses `encoding/json.RawMessage`. `Base64String`, `BigInt`, `Date`,
`GitObjectID`, `GitRefname`, `GitSSHRemote`, `HTML`, `URI`, and
`X509Certificate` use `string`. Explicit `bindings` in `octoql.yaml` override
these defaults. Unknown custom scalars still require a binding.

These defaults apply to ordinary SDL supplied through `schema.path`. They do not
fetch schemas independently of configured materialization and do not add
subscription or typed test-handler generation.
