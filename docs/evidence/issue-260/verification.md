# Issue #260: owned browser teardown verification

Date: 2026-10-09. Branch: `fix/issue-260`, fresh from `origin/main` at `08f0332`.
This is a build/test helper change, not a shipped host runtime or public feature.

## Reproduction

With the new delayed-child regression and the unchanged helper:

```sh
node --test --test-name-pattern='delayed real child' scripts/e2e-multicomputer/browser.test.mjs
```

The assertion failed after 25.922 ms: `close resolved before the child exited`,
actual `exitCode: null`, expected `0`. The fixture acknowledges the CDP close
request, writes `last-write` 200 ms later, then exits. Its isolated child and
pending timers are cleaned even when the baseline fails. This reproduces the
completion race independently of a retrying filesystem cleanup.

## Verification after the fix

Node 24; an existing Chrome for Testing executable selected with `CHROME`.
No installation or dependency change.

```sh
node --check scripts/e2e-multicomputer/lib.mjs
node --check scripts/e2e-multicomputer/browser.test.mjs
CHROME='<existing Chromium executable>' node --test scripts/e2e-multicomputer/browser.test.mjs
```

Both syntax checks pass. Focused runner: **10 pass, 0 fail, 0 skipped**, 14.574 s.

| Case | Observed result |
| --- | --- |
| Real delayed child and concurrent close | Both callers finish after exit and final write; one CDP close and one socket close |
| Acknowledgement without child exit | TERM after the 3 s graceful wait; delayed final write and exit observed, about 3.22 s |
| Real child ignores TERM | Only its own child receives KILL after the next 1 s; `signalCode: SIGKILL`, about 4.03 s |
| Pending call during teardown | Request rejects, map empty, socket closed, new request refused |
| Unexpected socket close | Pending request timer released without waiting 60 s |
| Synchronous send exception | Pending map and timer released |
| Delayed socket close | Close waits for the event, about 103 ms |
| Socket never closes | Teardown rejects after its 1 s bound |
| Child never exits | Teardown rejects after 3 + 1 + 1 s; socket and pending calls still cleaned |
| Actual installed Chromium | `Chrome/151.0.7922.34`; owned child exited, socket closed, temporary profile removed immediately without retries, about 950 ms |

The phase bounds total at most 6 s plus event-loop scheduling delay. Child
completion is observed through exit state/events, not a CDP response or signal
return value. Only the stored owned `ChildProcess` is signaled. The helper clears
its request timers/listeners; an unclosed WebSocket causes an explicit failure
rather than a false successful teardown. Concurrent callers share the same
completion or failure.

## Scope and limitations

Synthetic data only; the actual browser test reads its CDP version and uses an
isolated temporary profile. No GUI/app interaction, production terminal history,
installed service, Tailscale or startup changes, user process lookup/termination,
new dependencies or provider credits. No public protocol or Go runtime change.

Full multi-computer/app flows, physical phone checks and native Windows are not
claimed. Public docs/site are unchanged. Hosted CI remains disabled by the
owner's explicit decision. Private PR, coordinating adversarial review and merge
are pending with the root agent.
