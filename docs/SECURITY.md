# Security policy

## Supported versions

Security fixes are released against the latest published version. Upgrade to the
latest release before reporting an issue, and regenerate your client so it picks
up runtime fixes: generated code is self-contained, so a fix in octoqlgen only
reaches your application after regeneration.

## Redirects and credentials

Generated clients refuse redirects by default. Configure the final GraphQL
endpoint directly whenever possible. If a GitHub Enterprise Server appliance
behind a redirecting load balancer or vanity hostname cannot avoid redirects,
enable them explicitly with `Client.SetAllowRedirects(true)`. The opt-in follows
at most 10 redirects and removes bearer `Authorization` when a redirect leaves
the original scheme, host, and port.

Credentials added by a custom `http.RoundTripper` are reapplied on every
redirect hop. octoql cannot remove those credentials, so a transport that
follows redirects can bypass the generated client's credential protections.
Prefer `Client.SetBearerToken` for OAuth 2.0 bearer authentication. Do not use
redirects to upgrade an `http://` endpoint to `https://`: configure the HTTPS
endpoint directly so credentials are never sent in cleartext.

## Schema evolution and authorization checks

Generated clients accept enum strings outside the generated constants.
With the default `omit_unreferenced_implementations: true`, they also decode an
unrecognized abstract `__typename` into the generated `OctoqlOther` catch-all.
This is a deliberate forward-compatibility policy: a client continues decoding
when GitHub adds a valid enum value or interface implementation after
generation.

Treat both sets as open when writing authorization or policy checks. Permit
specific known enum constants and concrete types, and use an explicit denying
`default` in both enum and type switches. A negative enum comparison admits
unknown strings, while a concrete-type assertion does not match `OctoqlOther`;
either pattern can otherwise skip a deny condition without an error. See
[Fragments, enums, and abstract types](../README.md#fragments-enums-and-abstract-types)
for concrete fail-closed examples.

## Reporting a vulnerability

Report security vulnerabilities privately through the repository's
[Security advisories](https://github.com/WillAbides/octoql/security/advisories/new)
page. Do not open a public issue for an undisclosed vulnerability.
