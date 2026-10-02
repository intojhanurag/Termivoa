# Gate 0 harness

Gate 0 is the first gate of the multi-computer feature (`docs/multi-computer.md`, section 6). It answers one question on a real iPhone and a real Android phone:

> Can a Home Screen app on computer A get, keep and send computer B's `__Host-` `SameSite=Strict` HttpOnly cookie, on a credentialed `fetch` and on a WebSocket?

If the answer is no on iPhone (checks 1 or 2 fail), stop: the design goes to plan B.

This is a test tool only. It is not part of `pterm`, it does not touch `pterm` or its data, and it keeps no state on disk.

## Files

| File | What it does |
| --- | --- |
| `main.go` | The server. `-role home` serves the test app. `-role peer` sets and echoes the test cookie. |
| `index.html` | The test app: big buttons, one box per check, PASS or FAIL with details. |
| `sw.js` | The service worker, for the push checks (5 and 6). |
| `main_test.go` | Unit test for the peer: CORS, cookie flags, WebSocket origin check. Runs in `go test ./...`. |
| `local-check.mjs` | The local two-host check in Chrome and WebKit (see "Local check"). |

The Go code uses no new dependency. It uses the repository's own `internal/webpush` (VAPID push) and `github.com/coder/websocket` (already in `go.mod`).

## Flags

| Flag | Role | Meaning |
| --- | --- | --- |
| `-role home\|peer` | both | Which side to run. |
| `-peer https://…` | home | The peer origin, for example `https://peer.tailX.ts.net:10000`. |
| `-home https://…` | peer | The home origin. Only this exact origin gets credentialed CORS and the WebSocket. Use commas for more than one. |
| `-port N` | both | Port on 127.0.0.1. Default 8441 (home), 8442 (peer). |
| `-tls-self-signed` | both | HTTPS with a throwaway certificate. For the local check only. Do not use it behind Tailscale Serve. |

Both roles listen on **127.0.0.1 only**. Nothing is reachable from the network until you add Tailscale Serve.

## Owner steps (real phones)

You need two computers in **one** tailnet: **home** (H) and **peer** (P). Below, `H.tailX.ts.net` and `P.tailX.ts.net` are their MagicDNS names (see `tailscale status`). Both need this branch and Go 1.27, or copy a binary: `GOOS=linux GOARCH=amd64 go build -o gate0 ./scripts/gate0`.

The steps change Tailscale Serve (AGENTS.md rule 12). Only the owner runs them. They use HTTPS port **10000**, so your `pterm` on port 443 does not change.

1. On each computer, look at the Serve setup now and write it down:

   ```sh
   tailscale serve status
   ```

   Port 8443 is Termivoa's dev-server preview, so these steps use port 10000. If 10000 is in use, pick another free port and use it everywhere below.

2. On P, start the peer, then expose it:

   ```sh
   go run ./scripts/gate0 -role peer -home https://H.tailX.ts.net:10000
   tailscale serve --bg --https=10000 http://127.0.0.1:8442      # in a second terminal
   ```

3. On H, start home, then expose it:

   ```sh
   go run ./scripts/gate0 -role home -peer https://P.tailX.ts.net:10000
   tailscale serve --bg --https=10000 http://127.0.0.1:8441      # in a second terminal
   ```

4. **iPhone:** open `https://H.tailX.ts.net:10000` in Safari → Share → **Add to Home Screen**. Close Safari. Open **Gate 0** from the Home Screen. The top line must say `installed app: yes`.
5. Tap **Run checks 1, 2 and 4**. Then do check 3 (kill the app, open it; restart the phone, open it). Then checks 5 and 6 if you want them.
6. **Android:** do the same in Chrome (see check 7).
7. Tap **Copy results** on each phone and report (see "How to report").

### Remove it afterwards

On **each** computer:

```sh
tailscale serve --https=10000 off
tailscale serve status               # must match what you wrote down in step 1
```

Then stop `gate0` with Ctrl-C. Do **not** use `tailscale serve reset`: it also removes your `pterm` setup.

On the phones: delete the **Gate 0** app from the Home Screen. On Android, also clear the site data in Chrome: **Settings → Site settings → All sites → H.tailX.ts.net → Delete data**, and the same for P.

## The 7 checks

The design (section 6) numbers its checks a little differently; the last column maps them. Design check 6 (paste-pairing) needs the real app, so it is not here.

| # | Check | How | PASS means | FAIL means | Design |
| --- | --- | --- | --- | --- | --- |
| 1 | A credentialed `fetch` to the peer sets its cookie | Button. `GET /set`, then `GET /echo`. | The second request carried the cookie the peer set. | The phone did not store or did not send the cookie. **Stop: plan B.** | 1 |
| 2 | Later requests and a WebSocket send it | Same button. A preflighted `POST /echo` and a WebSocket to `/ws`. | Both carried the same cookie, and the WebSocket echoed `ping`. | One of them did not carry it. **Stop: plan B.** | 1 |
| 3 | The cookie survives an app kill and a phone restart | Runs by itself on each open; each open adds a line. iPhone: also open `https://P.tailX.ts.net:10000/echo` in Safari. | Every line says `sent: yes` (`WAIT` until two opens). Safari shows `"received": ""`, so the app keeps its own cookie jar. | A line says `NO`: the cookie was lost. **Stop: plan B.** | 2 |
| 4 | The cookie's expiry | Same button. The peer asks for `Max-Age` 30 days. Read the real expiry by hand (see below). | The peer set the cookie. Then report the expiry you read. | The peer did not set it. | 3 |
| 5 | A push-woken service worker sends the cookie (optional) | Button, then close the app within 15 s. | The worker's request to the peer carried the cookie. `openPages: 0` means it woke with no app open. This is the threat in the design; report it either way. | The worker's request had no cookie, or the push failed (the details say why). | 4 |
| 6 | Two scoped service workers, each with its own push key (optional) | Button. Registers `/a/` and `/b/`, each with its own VAPID key, and pushes both. | Both workers reported back. Peer push can work. | Only one or none reported. Peer push needs another plan. | 5 |
| 7 | Android (Chrome WebAPK) | Chrome menu → **Add to Home screen** → **Install**. Run 1–6 again. | Same as above. | Same as above. | 7 |

**Reading the real expiry (check 4).** iPhone: connect it to a Mac, then Mac Safari → **Develop** → your iPhone → **Gate 0** → **Storage** → **Cookies** → `__Host-gate0` on the P host → **Expires**. If it is not listed there, use **Network** → the `/set` request → **Cookies**, and write down that Storage did not list it. About 7 days from now means the WebKit IP cap applies (that is fine; pterm refreshes the cookie). About 30 days means it does not. Android: `chrome://inspect` on a computer → the Gate 0 app → **Application** → **Cookies**.

**IPv4.** The design asks for IPv4. The check 1 details show `client`: the phone's tailnet address as P saw it. `100.x.y.z` means IPv4. `fd7a:…` means IPv6; write that down, because the WebKit IP cap is about IPv4. If it shows `127.0.0.1`, Serve did not pass the address.

**Restarts.** Home makes new push keys each time it starts. After a restart of home, tap the push buttons again.

## How to report

On each phone, tap **Copy results** and paste the text into the Gate 0 issue or pull request (or send it to the agent). Add:

- the phone model and OS version;
- the expiry you read in check 4;
- for check 3: which opens were after an app kill and which after a phone restart;
- for check 5: if the notification came while the app was closed;
- anything odd (errors, prompts, a different `client` family).

The results hold only test values. There are no pterm credentials in them.

## Local check (two hosts on one Mac)

Cookies ignore ports, so this check gives each server its own host name: `--host-resolver-rules` (Chrome) or a small proxy (WebKit) sends `*.tailx.ts.net` and `*.taily.ts.net` to 127.0.0.1. `control.taily.ts.net` is a **cross-site control**: another tailnet suffix. The peer allows it in CORS here only so that the page can read what the peer got.

```sh
go build -o /tmp/gate0 ./scripts/gate0
/tmp/gate0 -role peer -tls-self-signed -home https://home.tailx.ts.net:8441,https://control.taily.ts.net:10000 &
/tmp/gate0 -role home -tls-self-signed -peer https://peer.tailx.ts.net:8442 &
/tmp/gate0 -role home -tls-self-signed -port 10000 -peer https://peer.tailx.ts.net:8442 &   # the control

rm -rf /tmp/gate0-chrome
node scripts/gate0/local-check.mjs chrome 'https://home.tailx.ts.net:8441/?auto=1' \
  'https://control.taily.ts.net:10000/?auto=1' 'https://home.tailx.ts.net:8441/'
node scripts/gate0/local-check.mjs chrome 'https://home.tailx.ts.net:8441/'          # new browser process, same profile

# WebKit needs a playwright-core whose WebKit build is installed (here 1.59.1 with webkit-2272):
PW=/path/to/node_modules/playwright-core node scripts/gate0/local-check.mjs webkit \
  'https://home.tailx.ts.net:8441/?auto=1' 'https://control.taily.ts.net:10000/?auto=1' 'https://home.tailx.ts.net:8441/'
kill %1 %2 %3
```

Results on 2026-10-02, macOS, Chrome 154.0.8037.93 (headless) and Playwright WebKit 26.4:

| Page | Engine | 1 | 2 (preflighted fetch, WebSocket) | 3 | 4 | `Sec-Fetch-Site` |
| --- | --- | --- | --- | --- | --- | --- |
| home (`tailx`) | Chrome | PASS | PASS (both sent the cookie) | — | PASS | `same-site` |
| home (`tailx`) | WebKit | PASS | PASS (both sent the cookie) | — | PASS | `same-site` |
| control (`taily`) | Chrome | FAIL: nothing sent | FAIL: nothing sent on either | — | PASS (set was asked, not stored) | `cross-site` |
| control (`taily`) | WebKit | FAIL: nothing sent | FAIL: nothing sent on either | — | PASS (set was asked, not stored) | `cross-site` |
| home, reopened in a new Chrome process | Chrome | — | — | PASS (sent on each open) | — | `same-site` |

The control ran after home in the same browser profile, so the peer cookie was in the jar. It was still never sent to the cross-site page, and the control's own `Set-Cookie` was not stored. That is the expected result.

Checks 5 and 6 also passed in headless Chrome 154 (real push through FCM, with the page open, so `openPages: 1`). They were not run in WebKit: Playwright WebKit has no push service. None of this proves the iPhone Home Screen app, the WebKit IP cap or the Android WebAPK. Only the real phones can show that.
