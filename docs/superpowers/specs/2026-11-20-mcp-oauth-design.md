# MCP OAuth

Implement issue #140 locally: connect an OAuth-only MCP client by URL, with browser sign-in through filex's configured identity providers (including Pocket ID), without a manually supplied API key.

Use filex as a small OAuth authorization server rather than forwarding Pocket ID tokens or requiring a separate proxy. This reuses existing browser sign-in and the user's permissions, and supplies dynamic client registration even when the upstream identity provider does not. Keep existing API-token authentication.

The server publishes protected-resource and authorization-server metadata, supports dynamic registration, Authorization Code with mandatory S256 PKCE, explicit browser consent, expiring opaque access tokens, rotating refresh tokens, and revocation. Default access is read-only; wider file scopes require explicit consent and the existing self-service scope ceiling. No administrative scope is issued. OAuth credentials are restricted to MCP, not usable as general-purpose filex API keys. Existing account, API-use, AI-use, tenant and file-grant checks remain authoritative.

Persist immutable clients and short-lived/one-use authorization records through one shared SQL adapter for SQLite, PostgreSQL and MySQL. Store hashes of codes/refresh tokens. Token redemption is atomic and restart-safe; revoking the backing API token also stops refresh. Bound request sizes, record lifetime and record count. Never redirect to an unregistered URI; require HTTPS callbacks except literal loopback addresses. Bind consent to a session, user, HttpOnly cookie and one-time request. Discover URLs from configured PublicURL, never untrusted forwarded headers.

Keep deployment unchanged. Validate with real database/router tests, fake upstream OIDC where needed, concurrency/replay cases, legacy-token checks and the backend suite. A live Confer/Pocket ID end-to-end test remains dependent on user authentication and is not claimed from local tests alone.

Initial scope is single-tenant mode. Multi-tenant OAuth is explicitly unavailable because the existing cross-host login handoff does not preserve the host-bound consent cookie; keeping that boundary is safer than creating a new issuer-host session for tenant users. Existing multi-tenant API keys remain unaffected. Public registrations are throttled and capped at 1,000 independently of the 10,000 total record ceiling, so anonymous clients cannot exhaust active-grant capacity.
