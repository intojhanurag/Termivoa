# ADR 0011: revoke the stored GitHub login before deleting computer data

- Status: proposed, **DO NOT MERGE without owner review**.
- Date: 2026-10-08
- Issue: #213
- Design: [uninstall-github.md](../uninstall-github.md)

## What problem does this solve?

`pterm uninstall --delete-data` removes the saved GitHub login without making
Disconnect's existing token-revocation request. A copied pair remains usable
until expiration or manual revocation. The command already deletes stored
secrets; this change tries to revoke their remote capability first.

## What decision is proposed?

After explicit uninstall confirmation, removal of startup registration and
confirmation that the broker stopped, acquire its existing exclusive data
lock. The GitHub package performs one bounded best-effort revoke of the
stored access/refresh pair using the same restricted HTTPS client and endpoint
as Disconnect, then deletes the login locally before lifecycle deletion.
Hold exclusive data ownership during this operation; no broker or credential
helper may refresh or replace that pair concurrently. The CLI sees only a
status, never token strings. No new protocol, endpoint, scopes or stored data.

Dry runs, unconfirmed commands, uninstall without --delete-data and refusal
while sessions/broker run never revoke. A failed revoke does not claim success
or prevent the expressly requested local deletion; print the existing manual
GitHub applications link. Local ownership/read/delete failures stop data
removal with a fixed non-secret error. No automatic retries or new login.

## Which alternatives were considered?

| Alternative | Decision |
| --- | --- |
| Call the running broker's Disconnect before shutdown | Does not cover uninstall when it is already stopped; a running login/setup could race the later deletion. A live broker remains the runtime owner, but offline cleanup needs exclusive ownership. |
| Launch a broker just for uninstall | Rejected: would load unrelated runtime state, start background work and impose configuration/startup requirements during removal. |
| Construct the full GitHub Service in the CLI | Rejected: its constructor scans Git, sweeps clone fixtures and creates background state unrelated to revocation. |
| Delete first and direct the user to GitHub only | Existing defect: discards the only local pair before trying revocation. |
| Refuse local deletion when GitHub is offline | Rejected: Disconnect is already best effort; users must be able to remove local data offline, with an honest manual-revocation warning. |

## What review is required?

Independent security review must verify single-writer ownership, token secrecy,
fixed destination/redirect/proxy restrictions and no network action before
consent. Independent feasibility review must check stopped/racing brokers,
platform locks, timeout/failure behavior and the alternatives above. Record
findings and resolutions in the design. Owner review remains required before
merging this design or implementation (AGENTS.md rule 24).
