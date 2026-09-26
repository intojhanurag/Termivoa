# Installing Phone Terminal

> Status: private development. Only **unsigned** artifacts exist. Signed and
> notarized releases need the owner's Apple Developer ID and Windows
> code-signing identity. Nothing below has passed the release gate yet.

Phone Terminal runs two per-user processes from one `pterm` executable:

- the **broker** owns every terminal session, the terminal state and the database;
- the **gateway** serves the web page on `127.0.0.1:8787` and talks to the broker.

They start separately at login and restart on failure. Neither runs as root,
SYSTEM or an administrator. No Node.js, Go or compiler is needed on the host.

## Install

### macOS (Apple Silicon)

1. Open `Phone-Terminal-<version>-macos-arm64.dmg`.
2. Drag **Phone Terminal.app** to **Applications**. Do not run it from the
   disk image: `pterm setup --apply` refuses a pterm on a mounted image or in
   App Translocation, because that path disappears later.
3. Optional, for the CLI on your PATH (you do this; the app never changes PATH):

   ```sh
   mkdir -p ~/.local/bin
   ln -s "/Applications/Phone Terminal.app/Contents/MacOS/pterm" ~/.local/bin/pterm
   ```

   The rest of this guide writes `pterm` for that path.

Unsigned development builds are blocked by Gatekeeper when downloaded. Signed
and notarized builds are pending owner signing.

### Windows 11 (x64)

1. Run `Phone-Terminal-<version>-windows-x64-setup.exe`. It needs no
   administrator rights and installs to
   `%LOCALAPPDATA%\Programs\Phone Terminal\versions\<version>\`.
2. Choose tasks:
   - **Start Phone Terminal when I sign in** (checked): runs
     `pterm setup --apply --yes`, which registers two scheduled tasks for your
     account only.
   - **Add pterm to my PATH** (unchecked): edits only your user PATH.

## First run

```sh
pterm setup                      # read-only checks and the planned changes
pterm setup --apply              # register startup after you confirm
pterm project add ~/code/app     # register a project root
pterm pair                       # show the expiring pairing link and its QR code
pterm pair approve <pending-id>  # approve after the codes match
```

`pterm setup` first runs only read-only checks: the pterm location, data and
runtime directories, whether the broker and gateway respond, startup
registration, available shells, and `tailscale status --json` /
`tailscale serve status --json` if Tailscale is installed. Then it prints the
exact files and commands it would use. It changes nothing without `--apply`,
and `--apply` asks for confirmation unless you pass `--yes`.

What `--apply` registers:

| | macOS | Windows |
| --- | --- | --- |
| Broker | `~/Library/LaunchAgents/dev.phone-terminal.broker.plist` | task `Phone Terminal Broker (<user>)` |
| Gateway | `~/Library/LaunchAgents/dev.phone-terminal.gateway.plist` | task `Phone Terminal Gateway (<user>)` |
| Start | at login (`RunAtLoad`) | at your logon (gateway 10 s later) |
| Restart | on failure (`KeepAlive` / `SuccessfulExit=false`) | on failure, every minute |
| Command | `launchctl bootstrap gui/<uid> <plist>` | `schtasks /Create /TN <name> /XML <file> /F`, then `/Run` |
| Logs | `~/Library/Application Support/Phone Terminal/logs/` | Task Scheduler history (no stdout capture) |

`pterm pair` prints the link and, when the output is a terminal, a QR code of
the same link drawn in black on white so it scans on dark and light themes.
`--no-qr` prints only the link. Both are a single-use secret that expires after
two minutes: do not share them in chats, issues, screenshots or logs.

`pterm doctor` repeats the read-only checks and exits non-zero if it finds a
problem. It never prints secrets.

`pterm launch` (the app and the Start-menu shortcut use it) starts the broker
and gateway if they are not running and opens `http://127.0.0.1:8787/`.

## Phone access with Tailscale Serve (you run this)

pterm never changes Tailscale. After `pterm setup` shows the gateway is
healthy, run this yourself on the host:

```sh
tailscale serve --bg --https=443 http://127.0.0.1:8787
```

Then open `https://<host>.<tailnet>.ts.net/` on your phone (on the same
tailnet). `pterm setup` reports if HTTPS 443 already serves something else:
resolve that yourself; do not overwrite another service.

**Never use Tailscale Funnel.** Funnel makes the page public on the internet.
`pterm setup` and `pterm doctor` warn if Funnel is on.

## Keep the host awake; broker lifetime

Sessions live inside the broker on your computer. The phone only views and
controls them.

- If the computer sleeps, the phone cannot reach it. Keep it awake and on
  power while you work from the phone (macOS: System Settings > Battery /
  Energy options; Windows: Settings > System > Power).
- Logging out, rebooting, or a broker crash **ends every live session**. The
  broker restarts, marks those sessions lost, and never re-runs old commands.
- Restarting only the gateway keeps sessions alive.

## Stop and shut down

```sh
pterm stop                       # stop the gateway only; sessions keep running
pterm shutdown                   # confirm, end every session, stop broker and gateway
```

`pterm stop` asks the running gateway to exit through its private control
endpoint in the runtime directory (a current-user-only socket or named pipe).
It never looks up or signals processes by name. The broker and every session
keep running, and `pterm list` still shows them; phones cannot connect until
the gateway starts again (`pterm launch`, or your next login).

`pterm shutdown` shows how many sessions are active and asks for confirmation,
like `pterm terminate`. Then the broker ends every session and its process tree
through the normal termination path, stops, and the gateway stops after it.
Phones that are still connected see the host as unavailable.

Both commands exit the processes cleanly, so the supervisor does not restart
them on failure. If they are registered for startup, the supervisor starts them
again at your next login (or `pterm launch` starts them). The command then
prints the exact command that unloads the registration, but never runs it:

| | macOS | Windows |
| --- | --- | --- |
| Gateway | `launchctl bootout gui/<uid>/dev.phone-terminal.gateway` | `schtasks /Change /TN "Phone Terminal Gateway (<user>)" /Disable` |
| Broker | `launchctl bootout gui/<uid>/dev.phone-terminal.broker` | `schtasks /Change /TN "Phone Terminal Broker (<user>)" /Disable` |

`pterm setup --apply` registers them again.

## Updates (manual, staged)

v0 has no automatic updater. Nothing downloads code at startup.

1. Install the new version beside the old one:
   - Windows: run the new installer. It adds
     `...\Phone Terminal\versions\<new-version>\` and keeps the old directory for
     rollback. It never closes a running broker.
   - macOS: keep the old app (for example rename it
     `Phone Terminal <old-version>.app`), then copy the new app to Applications.
2. The new pterm re-points startup with `pterm setup --apply` (the Windows
   installer does this). If the registered broker changes and the broker has
   **active sessions, setup refuses** and tells you to finish them or end them
   with `pterm terminate <session-id>`. It does not restart a broker with live
   sessions.
3. On Windows, re-registering the task does not stop the running broker; the
   new version starts at your next logon. On macOS, `--apply` reloads the
   LaunchAgents, which restarts the broker (only when no session is active).
4. Rollback: run `pterm setup --apply` from the previous version.

To update now instead of waiting for sessions to finish, run `pterm shutdown`
first: it ends every session only after you confirm. Pending: a broker-side
refusal for replacement (an update RPC) needs broker RPC support. Today the
check is done by `pterm setup` / `pterm uninstall` through the session list
before they change the broker registration. Database backup before migration is
not yet implemented.

## Uninstall

```sh
pterm uninstall --dry-run        # show what would be removed; changes nothing
pterm uninstall                  # remove startup registration after confirmation
pterm uninstall --delete-data    # also delete paired devices, projects, settings and logs
```

Uninstall stops and removes the two startup entries. It refuses while the
broker has active sessions. Your data stays unless you pass `--delete-data`.

Then remove the application:

- macOS: move Phone Terminal.app to the Trash and delete any `~/.local/bin/pterm` link.
- Windows: Settings > Apps > Phone Terminal > Uninstall. The uninstaller runs
  `pterm uninstall`, asks whether to delete data (default: keep), and removes
  its PATH entry.

Data locations:

- macOS: `~/Library/Application Support/Phone Terminal` and `~/Library/Caches/Phone Terminal`
- Windows: `%LOCALAPPDATA%\Phone Terminal`

If you enabled Tailscale Serve, remove it yourself (`tailscale serve reset`
removes **all** Serve configuration; use `tailscale serve status` first).

## Development harness

```sh
pterm up --dev                   # broker + gateway, isolated development profile
```

It starts the broker and gateway as two child processes in separate process
groups, prints the loopback URL (default `http://127.0.0.1:8788/`), and stops
both on Ctrl+C. Killing the gateway does not stop the broker. `--data-dir`,
`--runtime-dir` and `--listen` select other development locations.
