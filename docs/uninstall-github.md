# How does uninstall revoke the GitHub login?

Proposed correction for #213. **DO NOT MERGE without owner review**; ADR 0011
records the decision and alternatives. Implementation and focused/race evidence are prepared; independent security and feasibility reviews pass after corrections; owner approval remains pending.

## What is the acceptance boundary?

1. Parse/inspect/describe uninstall using existing read-only checks.
2. Refuse active sessions and a broker outside removable registration.
3. Dry-run exits without token reads, deletion or outbound calls.
4. Obtain existing confirmation (or --yes), unregister/stop normally, then
   wait until the broker is stopped. No startup/network configuration is
   changed by an agent implementing/testing this feature; tests use fixtures.
5. Take `ipc.LockBrokerDataDirectory` and use its canonical path. A restarted
   broker or another runtime directory sharing the data cannot also own it.
6. Before token work, canonicalize separate deletion roots. Skip roots equal
   to or nested in the held data directory; refuse containing roots. Preserve
   top-level symlinks as unlink-only operations. Compare actual filesystem
   identity, including case-insensitive and parent-symlink aliases.
7. Within the GitHub package only, read its private token file. If absent,
   make no request. Otherwise call the same `POST /credentials/revoke` as
   Disconnect, with the existing ten-second restricted client, no proxy,
   redirects, authentication header or refresh/login. Send both available
   tokens in the existing body; never expose them to CLI output/errors/logs.
8. Delete the local login with the existing routine and erase saved data while the lock remains held. Keep the private data
   directory and `broker-data.lock` inode; removing that namespace would
   permit a replacement broker lock during cleanup. Remove separate logs and
   runtime directories normally. Parent cancellation stops before local token/data deletion. A request-only
   timeout or failure to revoke prints the manual GitHub applications link; local read/lock/delete failures
   preserve the data and return a fixed message. No partial success claim.

The broker remains the only runtime auth owner. Offline maintenance takes its
same exclusive ownership after it stops; it does not call the full service constructor or
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

Feasibility review reproduced the original proposed RemoveAll ownership gap:
removing/recreating the directory permits a replacement flock while the old
inode remains locked. Resolved by preserving the directory/marker; a live
lock regression proves replacement ownership fails until release. It also
required disclosing revocation before consent, keeping local data on parent
cancellation, and honest partial-failure output. All are implemented/tested.
The package only initializes existing request fields; it never calls New or
starts background runtime work. Local delete errors are checked; existing
best-effort overwrite/sync behavior is not overstated. A second independent feasibility probe found a separate runtime/cache root
could alias the held data directory through a parent symlink. All deletion
roots now undergo canonical identity/ancestry checks before any token action;
real alias, containing-root and live-lock regressions pass. Independent
security and feasibility reviewers reran the corrected focused race tests and
found no new implementation blocker. Security review also corrected success
wording to report request acceptance, which is all GitHub's 202 response
proves. Inherited private-file symlink and hard-link defects were reproduced
and filed separately as #255 and #256 under rule21; they are not hidden here.
Owner review remains pending. Hosted checks remain blocked: the owner
explicitly requested keeping CI disabled, so no Actions execution is claimed.
