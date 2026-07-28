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

## Reporting a vulnerability

Report security vulnerabilities privately through the repository's
[Security advisories](https://github.com/WillAbides/octoql/security/advisories/new)
page. Do not open a public issue for an undisclosed vulnerability.
