# genqlient documentation

Welcome to the genqlient documentation! This documentation is made possible by viewers like you; if you see something unclear, file an [issue] or make a [pull request] to improve it!

[issue]: https://github.com/willabides/octoql/issues/new/choose
[pull request]: https://github.com/willabides/octoql/compare

## Usage/recipes

- [Getting started guide](introduction.md)
- [Runnable usage example](../example)
- [Handling your GraphQL schema](schema.md)
- [Client configuration and usage](client_config.md)
- [Writing your GraphQL operations](operations.md)

## Configuration editor support

The repository's [`octoql.yaml` example](octoql.yaml) uses a
`yaml-language-server` directive that resolves to
[`schema/octoql.schema.yaml`](../schema/octoql.schema.yaml) in this repository.
The YAML schema is generated from the reviewed JSON Schema source, alongside
[`schema/octoql.schema.json`](../schema/octoql.schema.json). Downstream projects
can point an editor at either committed file or at its raw `main`-branch URL:
`https://raw.githubusercontent.com/WillAbides/octoql/main/schema/octoql.schema.yaml`.

# Reference

- [Go package reference](https://pkg.go.dev/github.com/willabides/octoql)
- [`octoql.yaml` configuration example](octoql.yaml)
- [`@genqlient` directive reference](genqlient_directive.graphql)
- [changelog](CHANGELOG.md)

## Background

- [Why genqlient](../README.md#why-another-graphql-client)
- [Notes on the design of genqlient](design.md)
- Blog posts on the [usage][blog1] and [design][blog2] of genqlient
- [Contributing to genqlient](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

[blog1]: https://blog.khanacademy.org/genqlient-a-truly-type-safe-go-graphql-client/
[blog2]: https://blog.khanacademy.org/where-go-and-graphql-collide-behind-the-curtain-with-genqlient/
