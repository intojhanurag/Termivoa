# Security policy

## Supported versions

Phone Terminal is in private pre-release development. There is no supported release, published package, installer, or public security-support window yet. This file will be updated with supported versions before v0.1.0 is published.

## Reporting a vulnerability

Do not open a public issue containing vulnerability details, credentials, pairing data, terminal content, personal paths, or exploit material.

During private development, report security concerns to the repository owner through an existing private communication channel. **Before public release, the owner must approve and add a real confidential vulnerability-reporting route here.** No email address or security intake service has been invented for this placeholder.

Include only the minimum sanitized information needed to reproduce the issue:

- affected commit or build identifier;
- affected host OS/browser and versions;
- threat scenario and demonstrated impact;
- sanitized reproduction steps or a minimal fixture;
- whether a real credential may have been exposed.

Do not send secrets until a secure route is agreed. If a credential was exposed, revoke or rotate it first; deleting it from the current tree is not sufficient.

## Security boundaries

The intended v0 is a single-owner, single-host product. Browser authentication does not protect against malware already running as the same host user. Tailscale connectivity does not replace application authentication. The host shell retains the owner's filesystem permissions; registered projects are convenience roots, not sandboxes.

Production browser access is intended to use private HTTPS through Tailscale Serve to a loopback-only gateway. Tailscale Funnel and public tunnels are out of scope. Terminal contents are not intended to be persisted in v0.

These are design requirements, not claims that the controls have been implemented or audited.
