# Versioning policy

This document describes how we manage genqlient versions. See all published versions on [pkg.go.dev](https://pkg.go.dev/github.com/willabides/octoql?tab=versions) or [GitHub](https://github.com/willabides/octoql/releases).

## When do we make a release?

In general, we do not cut a release for every bugfix; instead we try to cut a release after major changes have had some time to bake, and in some cases after large codebases using genqlient have tried them. This ensures releases are somewhat more likely to work.

If that stability is desirable to you, use tagged releases of genqlient only (e.g. `go get github.com/willabides/octoql@latest`), and be aware that new features may take somewhat longer to make it to a release. (Feel free to make an issue to request a release if it's been a while.)

If you always want the latest and greatest changes quickly, Go has excellent support for installing packages at any commit. We do have extensive tests and try to keep the main branch safe for production use, but we're never perfect, so take care appropriate to your use case. You can install the main branch with `go get github.com/willabides/octoql@main`, or replace `main` with any commit SHA. Please report any bugs you see so they can be fixed before the next release!

For the details of actually making a release, see the [contributor docs](CONTRIBUTING.md#making-a-release).

## What is a breaking change?

We consider the following changes to be breaking:
- breaking changes to supported runtime APIs in the root `octoql` package
- changes which cause genqlient to, given the same valid query, make a breaking change to the API or behavior of the generating code (i.e. it should be safe to re-run a newer version of genqlient on existing queries)
- changes to the root runtime which break existing generated code without regeneration

We don't consider the following changes to be breaking:
- syntactic changes to the generated output for existing queries; if you check that your generated code is up to date in CI you should expect to need to update it when you update genqlient
- changes, including breaking API changes, to any double-underscore-prefixed names in the generated code (i.e. don't refer to these in your code); the same applies to root runtime names documented as generated-code-only contracts
- coordinated changes to the code-generator and generated-code-only root runtime contracts, provided regenerating updates existing generated code
- dropping support for Go versions which are no longer supported by the Go project (all but the [two newest](https://go.dev/doc/devel/release))

The generator and root runtime are released together in the same `octoql` Go
module. When upgrading octoql, rerun the generator so generated code and its
runtime contracts come from the same module version.

Note that while genqlient is on version 0.x we may make breaking changes at any time, although we still aim to do so only in minor version bumps (0.6.0, not 0.5.1), and we aim to minimize breaking changes, especially to core functionality.
