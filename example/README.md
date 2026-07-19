# octoql example

This package is a small generated GitHub client. Its checked-in local schema and
generated code make it useful without network access.

To regenerate the client with the repository-pinned tool:

```sh
go generate ./example
```

To run the live example, provide a GitHub token and login:

```sh
GITHUB_TOKEN=<token> go run ./example <login>
```

The example endpoint and authentication are configured in
[`main.go`](main.go). The main [README](../README.md) documents remote schema
materialization and the recommended gitignored `.octoql` workflow.
