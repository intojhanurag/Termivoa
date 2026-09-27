# ADR 0003: Loopback gateway with private Tailscale Serve access

- Status: Accepted by the authoritative plan; implemented in v0 (2026-09-27). Loopback gateway and pairing are verified on the macOS development profile; Tailscale Serve HTTPS with a real phone is not yet verified
- Date: 2026-09-20

## Context

The phone needs authenticated HTTPS access without creating a hosted relay, public tunnel, domain dependency, or cloud account. Private network reachability alone is not adequate application authentication.

## Decision

Bind the production gateway only to `127.0.0.1:8787`. Use an owner-authorized installed Tailscale client and Tailscale Serve for the configured private HTTPS origin. Preserve existing Serve configuration and report conflicts; never enable Funnel. Enforce application pairing, hashed browser credentials, strict cookies, exact Origin/Host validation, and broker-authoritative revocation. Ignore externally supplied forwarded-user identity headers.

Use a distinct, explicit loopback-only development profile for local tests. Failure to configure production TLS or Tailscale must not silently enable development authentication.

## Consequences

No cloud VM, relay, hosted auth, domain purchase, or public listener is required for v0. Setup requires explicit owner participation for Tailscale login or configuration changes. Browser access remains unavailable when the private network cannot reach the host, but host sessions continue independently.
