# Issue #209: refused creates keep the allowance

Date: 2026-10-08. Baseline: `08f0332`. Go: `go1.27.1`.
Only synthetic herdr socket fixtures were used; no real agents, provider
credits, notifications, host configuration or credentials were touched.

## Reproduction before the fix

New black-box regressions against unchanged runtime:

```sh
go test -run '^TestProvider(PreflightRefusalsLeaveCreateQuota|AgentTabRefusalsLeaveCreateQuota)$' ./internal/broker
```

Exit 1, 1.763 s. Three local rejections exhausted the allowance; the next
otherwise valid create returned `limit_exceeded` with
`too many requests; wait a minute and try again`.

| Regression | Refusals before the valid create | Baseline result |
| --- | --- | --- |
| stale group | 3 `session_missing` responses | valid create denied |
| missing kind | 3 `invalid_request` responses | valid create denied |
| not git | 3 `not_a_repository` responses | valid create denied |
| agent tab with a stale item | 3 `session_missing` responses | valid create denied |

## Corrected behavior and bounds

The provider marks only local rejections before its first mutating operation.
The broker reserves a create unit before calling it; an attempted operation
keeps its charge, a marked refusal refunds it. Error codes alone are not
used for refunds: `not_a_repository` and `session_missing` can also follow
mutating calls. The private wrapper preserves `errors.Is`, error text and
existing public error codes. No protocol payload changes or automatic retry.

| Verification | Actual result |
| --- | --- |
| same four baseline regressions after fix | PASS, 1.720 s |
| focused race regressions, command below | PASS, 9.432 s, five runs |
| `go test -race ./internal/herdr ./internal/broker` | PASS, 34.679 / 102.118 s |
| `go vet ./internal/herdr ./internal/broker` | PASS |
| `git diff --check` | PASS |
| required frozen docs-site install and Astro build | PASS, 29 pages, all internal links valid |

```sh
go test -race -count=5 -run '^TestProvider(PreflightRefusalsLeaveCreateQuota|AgentTabRefusalsLeaveCreateQuota|UnavailableRefusalsLeaveCreateQuota|AttemptedFailuresKeepCreateQuota|Reservation|ConcurrentReservations)' ./internal/broker
```

Tests prove recovered provider availability does not retain refusal charges,
three real synthetic RPC attempts still use all three units (including
post-RPC error codes that resemble preflight failures), and agent tabs share
the create budget. Concurrent unit tests admit exactly 3 of 64 reservations;
8 concurrent refund attempts per reservation return each unit once. A
committed unit cannot later be refunded. Old-window refunds cannot erase new
charges after minute rollover or a clock reset, even with matching timestamps.
Routes and devices stay independent. The existing 30-manage-action budget is
unchanged and still counts refused actions.

## Remaining gates and scope

The whole repository check, hosted CI and the owner's real-phone checks are
not claimed here. Root coordinates integration and site deployment after
merge. Unknown or ambiguous outcomes remain charged; a user should inspect
the item and existing terminal before manually trying again. There is no
operation replay or new client idempotency key. No new network surface,
authentication, stored secrets, schema migration or pairing change.
