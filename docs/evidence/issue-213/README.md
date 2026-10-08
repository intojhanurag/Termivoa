# Issue213: revoke saved GitHub login before deleting data

Date 2026-10-08; branch fix/issue-213, base08f0332. Design-first draft PR250,
ADR0011. DO NOT MERGE without explicit owner review (AGENTS rule24).
All accounts/paths are synthetic temporary fixtures; no owner token was read,
no GitHub request or real startup action was performed.

| Gate | Actual result |
| --- | --- |
| Baseline CLI with inert test seam | Revoke-before-delete fails: zero calls; remote-warning and local-error preservation regressions fail. Consent cases pass |
| Focused correction | CLI4tests/subcases, package4tests/subcases and lifecycle2tests pass, including parent cancellation, request timeout, no redirect/retry, both token fields, no-account and local failure |
| Ownership feasibility blocker | Reviewer reproduced deletion/recreation allowing replacement flock against an unlinked original inode. Preserve directory/marker; live test now refuses replacement ownership until release and removes no symlink target |
| Consent/order | Dry-run/no confirmation/no delete-data make zero calls. Existing broker/session refusal tests preserved. Cleanup receives canonical data path under exclusive lock; token file exists before request |
| Full regression/race | `go test -race ./cmd/pterm ./internal/github ./internal/lifecycle ./internal/ipc -count=1 -timeout=180s` passes: 30.641s,17.183s,3.229s,2.899s |
| Second ownership blocker | Reviewer reproduced parent-symlink alias root removing retained directory. Canonical filesystem identity/ancestry validation before token work resolves it; alias/containing/top-level-symlink/live-lock tests pass independently with race |
| Independent reviews | Security and feasibility reviewers find no new blocker after both ownership fixes and request-acceptance wording. Focused race passes CLI/GitHub/lifecycle/IPC; inherited #255/#256 filed separately |
| Broad checks | Full scripts/check passes protocol drift, all Go/third-party tests, vet, typecheck, 1048 web tests and production build before final alias/wording patch. Full four-package race and vet pass; frozen Astro29-page/link build passes. Final changed-code/docs verification follows below |
| Required follow-up | Final changed-code/docs checks and explicit owner approval; required hosted checks blocked by owner's request to keep CI disabled |

The initial local test fixture used /tmp versus canonical /private/tmp and was
corrected to compare canonical paths. A synthetic HTTP timeout handler first
waited for disconnect before draining its body and hung; that owned fixture
process was terminated, then fixed to drain/close the body. Corrected tests
have explicit deadlines and finish; no hung run is claimed passed.

Existing uninstall directory-absence assertions were updated to assert only
the private ownership marker remains. This change is required for safe token
cleanup and is disclosed before consent/in docs, not an unrelated cleanup.
Windows GitHub remains unsupported; native Windows/Linux execution is pending.
Context-cancellation evidence is not a guarantee remote revocation did not
occur: it preserves local data and tells the user to check GitHub manually.

Final changed-code gate after alias/acceptance/Windows fixture corrections:
`go test -race ./cmd/pterm ./internal/github ./internal/lifecycle ./internal/ipc
-count=1 -timeout=180s` passes (29.965s/16.376s/2.070s/3.115s). Vet passes.
Frozen site install and Astro29-page build/link validation pass. No runtime
changes after these checks. Required hosted checks and explicit owner approval
remain pending; inherited #255/#256 will need separate safe-token PRs.
