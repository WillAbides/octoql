# Getting started with octoql

This document describes how to set up octoql and use it for simple queries. See
also the full worked [example](../example), the [FAQ](faq.md), and the rest of
the [documentation](./).

## Step 1: Initialize octoqlgen.yaml

Run:

```sh
go tool octoqlgen init
```

This creates a minimal `octoqlgen.yaml` and `.octoql/.gitignore`. It does not fetch
a schema.

## Step 2: Configure and materialize your schema

For a local schema, put SDL at the configured `schema.path`. For a pinned remote
schema, add one `schema.source` variant and its SHA-256 as shown in the
[`octoqlgen.yaml` reference](octoqlgen.yaml), then run:

```sh
go tool octoqlgen schema materialize
```

Materialization verifies an existing local file or fetches a missing remote
file, validates its checksum and SDL, and writes only the configured schema
path. It never rewrites `octoqlgen.yaml`.

## Step 3: Write your queries

Next, write your GraphQL query or mutation. This is often easiest to do in an interactive explorer like [GraphiQL](https://github.com/graphql/graphiql/tree/main/packages/graphiql#readme). Put it in `genqlient.graphql`:
```graphql
query getUser($login: String!) {
  user(login: $login) {
    name
  }
}
```

## Step 4: Run octoqlgen

Set `operations` and `generated` in `octoqlgen.yaml`, then run
`go tool octoqlgen generate`. Generation verifies or materializes the configured
schema before producing the client and optional exported operations.

## Step 5: Use your queries

Finally, write your code!  The generated code will expose a function with the same name as your query, here
```go
func getUser(
  ctx context.Context,
  client *octoql.Client,
  login string,
) (*octoql.Response[getUserResponse], error)
```

As for the arguments:
- for `ctx`, pass your local context (see [`go doc context`](https://pkg.go.dev/context)) or `context.Background()` if you don't need one
- for `client`, call [`octoql.NewClient`](https://pkg.go.dev/github.com/willabides/octoql#NewClient), e.g. `octoql.NewClient("https://your.api.example/path", http.DefaultClient)`
- for `login`, pass your GitHub username (or whatever the arguments to your query are)

The response's `Data` field is a struct with fields corresponding to each GraphQL field; for the exact details check its GoDoc (perhaps via your IDE's autocomplete or hover).  For example, you might do:
```go
ctx := context.Background()
client := octoql.NewClient("https://api.github.com/graphql", http.DefaultClient)
resp, err := getUser(ctx, client, "benjaminjkraft")
fmt.Println(resp.Data.User.Name, err)
```

Now run your code!

## Step 6: Repeat

Over time, as you add or change queries, run
`go tool octoqlgen generate` to regenerate
`generated.go`. Or add
`//go:generate go tool octoqlgen generate` to
your source, then run [`go generate`](https://go.dev/blog/generate). If you're
using an editor or IDE plugin backed by
[gopls](https://github.com/golang/tools/blob/master/gopls/README.md), keep
`generated.go` open in the background and reload it after each run.

If you prefer, you can specify your queries as string-constants in your Go source, prefixed with `# @genqlient` -- at Khan we put them right next to the calling code, e.g.
```go
_ = `# @genqlient
  query getUser($login: String!) {
    user(login: $login) {
      name
    }
  }
`

resp, err := getUser(...)
```
(You don't need to do anything with the constant, just keep it somewhere in the
source as documentation and for the next generation run.) In this case, update
`operations` in `octoqlgen.yaml` to include your Go source.

All the filenames above, and many other aspects of genqlient, are configurable; see the [full documentation](.) for usage guides, reference information, and documentation on how to contribute to genqlient.
