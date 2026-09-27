# Security policy

## Supported versions

Termivoa is in private pre-release development. There is no supported release, published package, installer, or public security-support window yet. This file will be updated with supported versions before v0.1.0 is published.

## Reporting a vulnerability

Do not open a public issue containing vulnerability details, credentials, pairing data, terminal content, personal paths, or exploit material.

Report vulnerabilities privately by email to **aojharaj2004@gmail.com**. Use a subject that starts with `[Termivoa security]`.

What to expect (best effort; this is a one-person project):

- an acknowledgement within 7 days;
- a first assessment (confirmed, needs more information, or not a vulnerability) within 30 days;
- a fix or mitigation plan for confirmed issues, and credit in the release notes if you want it;
- please give us a reasonable time to ship a fix before you disclose publicly.

Include only the minimum sanitized information needed to reproduce the issue:

- affected commit or build identifier;
- affected host OS/browser and versions;
- threat scenario and demonstrated impact;
- sanitized reproduction steps or a minimal fixture;
- whether a real credential may have been exposed.

Never send real secrets, even by email. If a credential was exposed, revoke or rotate it first; deleting it from the current tree is not sufficient.

## Security boundaries

The v0 design is single-owner and single-host. Browser authentication does not
protect against malware already running as the same host user. Tailscale
connectivity does not replace application authentication. The host shell
keeps the owner's filesystem permissions; registered projects are convenience
roots, not sandboxes.

What is implemented (see [docs/threat-model.md](docs/threat-model.md) and
[docs/architecture.md](docs/architecture.md)):

- the gateway listens on loopback only; phones reach it through private
  Tailscale Serve HTTPS. Tailscale Funnel and public tunnels are out of scope,
  and `pterm setup` / `pterm doctor` warn when Funnel is on;
- pairing uses a single-use, two-minute link plus owner approval after comparing
  a code; paired devices use `HttpOnly`, `SameSite=Strict` session cookies and
  can be revoked at once with `pterm revoke`;
- clients receive normalized screen frames, never raw terminal bytes, and send
  only structured input;
- terminal content is not written to the database; there is no telemetry.

These controls have automated tests but no independent security audit, and
real-device and real-tailnet checks are still pending
([docs/owner-verification.md](docs/owner-verification.md)).
