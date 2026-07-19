# Getting started with octoql

This document describes how to set up octoql and use it for simple queries. See
also the full worked [example](../example), the [FAQ](faq.md), and the rest of
the [documentation](./).

## Step 1: Download your schema

You want the schema in GraphQL [Schema Definition Language (SDL)](https://graphql.org/learn/schema/#type-language) format.  For example, to query the GitHub API, you could download the schema from [their documentation](https://docs.github.com/en/graphql/overview/public-schema).  Put this in `schema.graphql`.

## Step 2: Write your queries

Next, write your GraphQL query or mutation. This is often easiest to do in an interactive explorer like [GraphiQL](https://github.com/graphql/graphiql/tree/main/packages/graphiql#readme). Put it in `genqlient.graphql`:
```graphql
query getUser($login: String!) {
  user(login: $login) {
    name
  }
}
```

## Step 3: Run octoqlgen

Create a `genqlient.yaml` configuration file, then run
`go run github.com/willabides/octoql/cmd/octoqlgen generate`. This produces a
file `generated.go` with your queries.

## Step 4: Use your queries

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

## Step 5: Repeat

Over time, as you add or change queries, run
`go run github.com/willabides/octoql/cmd/octoqlgen generate` to regenerate
`generated.go`. Or add
`//go:generate go run github.com/willabides/octoql/cmd/octoqlgen generate` to
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
(You don't need to do anything with the constant, just keep it somewhere in the source as documentation and for the next time you run genqlient.)  In this case you'll need to update `genqlient.yaml` to tell it to look at your Go code.

All the filenames above, and many other aspects of genqlient, are configurable; see the [full documentation](.) for usage guides, reference information, and documentation on how to contribute to genqlient.
