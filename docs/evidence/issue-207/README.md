# Issue 207: bounded notification attempts after hung subscriptions

Date: 2026-10-08. Base: `08f0332`. macOS arm64, Go 1.27.1.
All push endpoints are synthetic and use an in-process HTTP transport;
no real notification or external push request was sent.

## Is the issue real?

Yes. The existing four workers use a 10 s HTTP timeout under one 60 s
delivery deadline. With 24 hung subscriptions listed before a healthy phone,
six waves of four use the entire window before that phone gets an attempt.

The new regression covers 25 and 64 subscriptions: every endpoint except
the last waits for request cancellation. It scales the delivery window to
1 s and the client timeout to 1/6 s, preserving the production 10/60 ratio.
The current test was run with the baseline notifier through Go's source
overlay, leaving the working files intact:

```sh
python3 - <<'PY'
import json, pathlib, subprocess, tempfile
root = pathlib.Path.cwd()
with tempfile.TemporaryDirectory(prefix='issue-207-baseline-', dir=root) as tmp:
    tmp = pathlib.Path(tmp)
    original = tmp / 'notifier.go'
    original.write_bytes(subprocess.check_output(['git', 'show', '08f0332:internal/webpush/notifier.go']))
    overlay = tmp / 'overlay.json'
    overlay.write_text(json.dumps({'Replace': {str(root / 'internal/webpush/notifier.go'): str(original)}}))
    result = subprocess.run(['go', 'test', '-overlay=' + str(overlay), '-count=1', '-run',
                             '^TestManyHungEndpointsDoNotStarveALaterPhone$', './internal/webpush'])
    print('Baseline regression exit:', result.returncode)
PY
```

Both cases failed with `accepted=0` and `context deadline exceeded`;
baseline regression exit 1. The healthy last endpoint was starved.

## What changes?

Keep four concurrent workers and the existing per-delivery 60 s deadline.
After listing subscriptions, cap each request at the smaller of 10 s and
half the remaining time divided by the number of four-worker waves. Reserve
the other half for encryption, scheduling and cleanup. Cancel each request
context immediately after its send returns. The 64-subscription cap, rate
limits and maximum 192 concurrent push requests are unchanged.

Timeout and cancellation retain a subscription for a later event. A
404/410 or invalid endpoint/key still removes it. No database field, failure
counter, retry queue, authentication rule, endpoint allow-list or protocol
message changes. No fresh database or new pairing is needed.

This is best effort, not a delivery guarantee. Many subscriptions give a
working but slow service less time (about 1.875 s at 64 entries), and a host
under severe load can still exhaust the overall window. A push-service
acceptance also does not prove that a phone displayed the notification.

## Verification

| Command | Result |
| --- | --- |
| `go test -race -count=10 ./internal/webpush` | PASS, 21.631 s |
| `go test -race ./internal/store` | PASS, 6.409 s |
| `go vet ./internal/webpush ./internal/store` | PASS |
| `git diff --check` | PASS |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | PASS with Node 24.11.0/pnpm 10.33.0; 29 pages; all internal links valid |

The regression verifies the healthy phone is accepted before the shared
deadline, all 25/64 subscriptions are attempted, at most four requests are
active, no request remains when delivery returns and timeouts retain all
subscriptions. Existing tests also verify deadline cancellation starts no
later requests, revocation/logout remove subscriptions, expired endpoints
are deleted, encryption/VAPID and session/provider/test budgets remain valid.

The notification guide remains in the existing docs sidebar; no command,
access or stored-data behavior changes. Public feature/release/troubleshooting
docs and internal architecture/design/roadmap are updated. Full repository
checks, independent adversarial PR review and hosted checks remain for the
coordinator. Real iPhone notifications and the real-device gates remain open.
