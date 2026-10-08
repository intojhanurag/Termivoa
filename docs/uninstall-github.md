# How does uninstall revoke the GitHub login?

Proposed correction for #213. **DO NOT MERGE without owner review**; ADR 0011
records the decision and alternatives. Implementation/tests are pending.

## What is the acceptance boundary?

1. Parse/inspect/describe uninstall using existing read-only checks.
2. Refuse active sessions and a broker outside removable registration.
3. Dry-run exits without token reads, deletion or outbound calls.
4. Obtain existing confirmation (or --yes), unregister/stop normally, then
   wait until the broker is stopped. No startup/network configuration is
   changed by an agent implementing/testing this feature; tests use fixtures.
5. Take `ipc.LockBrokerDataDirectory` and use its canonical path. A restarted
   broker or another runtime directory sharing the data cannot also own it.
6. Within the GitHub package only, read its private token file. If absent,
   make no request. Otherwise call the same `POST /credentials/revoke` as
   Disconnect, with the existing ten-second restricted client, no proxy,
   redirects, authentication header or refresh/login. Send both available
   tokens in the existing body; never expose them to CLI output/errors/logs.
7. Delete the local login with the existing routine and apply requested
   lifecycle directory removal while the lock remains held. Failure to revoke
   prints the manual GitHub applications link; local read/lock/delete failures
   preserve the data and return a fixed message. No partial success claim.

The broker remains the only runtime auth owner. Offline maintenance takes its
same exclusive ownership after it stops; it does not instantiate a service or
run a second runtime. No schema/setting migration or fresh pairing is needed.

## What will be verified?

| Gate | Required evidence |
| --- | --- |
| Baseline | Synthetic saved pair deleted by current uninstall with no revoke |
| Package request | Both tokens, exact endpoint/body, bounded request and no token output |
| Consent | Dry-run, declined/no confirmation and data-preserving uninstall make zero calls |
| Ordering | Revoke precedes deletion and follows broker shutdown; live lock refuses safely |
| Outage | Local deletion still follows requested consent; output says remote revocation was not confirmed |
| Local failure | Read/lock/delete error stops data deletion without secret-bearing details |
| Regression | CLI/lifecycle/GitHub tests and race suites, Go vet, existing platform guards |
| Docs | Internal map/design/roadmap/progress; public CLI/uninstall/security/troubleshooting, changelog/README; frozen Astro build |
| Review | Independent security and feasibility/alternatives, findings resolved, explicit owner approval before merge |

## Which review findings are resolved?

Independent reviews are pending. Record actual findings here before asking the
owner to approve the concrete design/implementation. Hosted required checks
remain blocked while CI is manually disabled and Actions-minutes permission
is pending; no gate is claimed passed.
