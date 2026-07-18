[![Go Reference](https://pkg.go.dev/badge/github.com/willabides/octoql.svg)](https://pkg.go.dev/github.com/willabides/octoql)
[![Test Status](https://github.com/willabides/octoql/actions/workflows/ci.yaml/badge.svg)](https://github.com/willabides/octoql/actions)
[![Contributor Covenant](https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg)](docs/CODE_OF_CONDUCT.md)

# octoql: a truly type-safe Go GraphQL client

octoql is a standalone project derived from Khan/genqlient. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for attribution.

## What is octoql?

octoql is a Go library that generates type-safe code to query a GraphQL API. It takes advantage of the fact that both GraphQL and Go are typed languages to ensure at compile-time that your code is making a valid GraphQL query and using the result correctly, all with a minimum of boilerplate.

octoql provides:

- Compile-time validation of GraphQL queries: never ship an invalid GraphQL query again!
- Type-safe response objects: genqlient generates the right type for each query, so you know the response will unmarshal correctly and never need to use `interface{}`.
- Production-readiness: its inherited generator is used in production at Khan Academy, where it supports millions of learners and teachers around the world.

## How do I use octoql?

You can run octoqlgen with `go run github.com/willabides/octoql/cmd/octoqlgen generate`. To set your project up to use octoql, see the [getting started guide](docs/introduction.md), or the [example](example). For more complete documentation, see the [docs](docs).

## How can I help?

octoql welcomes contributions. Check out the [contribution guidelines](CONTRIBUTING.md), or file an issue [on GitHub](issues).

## Why another GraphQL client?

Most common Go GraphQL clients have you write code something like this:
```go
query := `query GetUser($id: ID!) { user(id: $id) { name } }`
variables := map[string]interface{}{"id": "123"}
var resp struct {
	Me struct {
		Name graphql.String
	}
}
client.Query(ctx, query, &resp, variables)
fmt.Println(resp.Me.Name)
// Output: Luke Skywalker
```

This code works, but it has a few problems:

- While the response struct is type-safe at the Go level; there's nothing to check that the schema looks like you expect.  Maybe the field is called `fullName`, not `name`; or maybe you capitalized it wrong (since Go and GraphQL have different conventions); you won't know until runtime.
- The GraphQL variables aren't type-safe at all; you could have passed `{"id": true}` and again you won't know until runtime!
- You have to write everything twice, or hide the query in complicated struct tags, or give up what type safety you do have and resort to `interface{}`.

These problems aren't a big deal in a small application, but for serious production-grade tools they're not ideal.  And they should be entirely avoidable: GraphQL and Go are both typed languages; and GraphQL servers expose their schema in a standard, machine-readable format.  We should be able to simply write a query and have that automatically validated against the schema and turned into a Go struct which we can use in our code.  In fact, there's already good prior art to do this sort of thing: [99designs/gqlgen](https://github.com/99designs/gqlgen) is a popular server library that generates types, and Apollo has a [codegen tool](https://www.apollographql.com/docs/devtools/cli/#supported-commands) to generate similar client-types for several other languages.  (See the [design note](docs/design.md) for more prior art.)

genqlient fills that gap: you just specify the query, and it generates type-safe helpers, validated against the schema, that make the query.
