# Migrating inherited generator configuration

octoql keeps the inherited `genqlient.yaml` format, with GitHub-oriented
generation defaults.

Abstract selections now omit concrete Go structs that are not referenced by an
applicable inline or named fragment. Each selection gets an `OctoqlOther`
catch-all with the shared fields and `__typename`. Code that switches on every
schema implementation can temporarily restore the inherited behavior:

```yaml
omit_unreferenced_implementations: false
```

GitHub public-schema scalars also have built-in mappings. `DateTime`,
`PreciseDateTime`, and `GitTimestamp` use `time.Time`. `Base64String`, `BigInt`,
`CustomPropertyValue`, `Date`, `GitObjectID`, `GitRefname`, `GitSSHRemote`,
`HTML`, `URI`, and `X509Certificate` use `string`. Existing explicit `bindings`
continue to win, so compatible inherited configuration does not need to change.
Unknown custom scalars still require a binding.

These defaults apply to ordinary SDL supplied through `schema`. They do not
fetch schemas or imply separate support for internal or enterprise GitHub
schemas.
