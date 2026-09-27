# Installing Phone Terminal

> Status: private development. Only **unsigned** artifacts exist. Signed and
> notarized releases need the owner's Apple Developer ID and Windows
> code-signing identity. Nothing below has passed the release gate yet. No
> release is published: build from source ([README quick start](../README.md#quick-start))
> or build the artifacts with [`packaging/macos`](../packaging/macos/README.md)
> and [`packaging/windows`](../packaging/windows/README.md). The short version
> of this guide is [getting-started.md](getting-started.md).

Phone Terminal runs two per-user processes from one `pterm` executable:

- the **broker** owns every terminal session, the terminal state and the database;
- the **gateway** serves the web page on `127.0.0.1:8787` and talks to the broker.

They start separately at login (see the startup table below for restarts
and logs). Neither runs as root,
SYSTEM or an administrator. No Node.js, Go or compiler is needed on the host.

## Install

### macOS (Apple Silicon)

1. Open `Phone-Terminal-<version>-macos-arm64.dmg`.
2. Drag **Phone Terminal.app** to **Applications**. Do not run it from the
   disk image: `pterm setup` refuses a pterm on a mounted image or in
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

Without the PATH option, `pterm` is not a command. In PowerShell, go to the
folder that has `pterm.exe` and write `.\pterm.exe` in its place, for example
`.\pterm.exe setup`.

#### Test on Windows with a single pterm.exe

To try a bare `pterm.exe` without the installer:

1. Put it in `%LOCALAPPDATA%\Programs\Termivoa\`. Do not use Desktop or
   Documents (OneDrive may move or sync them) or a Temp folder (`pterm setup`
   refuses Temp): the startup tasks run this exact path at every logon.
2. In PowerShell:

   ```powershell
   cd "$env:LOCALAPPDATA\Programs\Termivoa"
   .\pterm.exe setup
   .\pterm.exe doctor
   ```

3. Logs are in `%LOCALAPPDATA%\Phone Terminal\logs\` (`broker.log`,
   `gateway.log`).

Microsoft Defender or SmartScreen may warn about an unsigned `pterm.exe`.
Signed builds are pending owner signing.

## First run: three steps

1. Install (above).
2. Run `pterm setup` in a terminal.
3. Scan the QR code with your phone, compare the code, and answer `y`.

`pterm setup` first runs only read-only checks: the pterm location, data and
runtime directories, whether the broker and gateway respond, startup
registration, available shells, and `tailscale status --json` /
`tailscale serve status --json` if Tailscale is installed. Then it lists what
it will do and asks **once**:

```text
pterm setup will:
1. Start Phone Terminal when you log in (current user only, no administrator rights):
   ...the exact files and launchctl / schtasks commands...
2. Wait until the broker and the gateway answer.
3. Save the phone address https://<mac>.<tailnet>.ts.net (unless you set another one).
4. Add your home folder /Users/you as project "Home" if you have no project.
5. Show a QR code for your phone and ask you to approve the code it shows.
Before step 4 it asks separately whether to run: tailscale serve --bg --https=443 http://127.0.0.1:8787
Before step 4 it also asks separately whether to run: tailscale serve --bg --https=8443 http://127.0.0.1:8789
Never use Tailscale Funnel: it would put your terminal on the public internet.

Continue? [y/N]
```

After `y` it:

- registers startup (see the table below) and waits for the broker and gateway;
- saves `https://<Self.DNSName>` from `tailscale status --json` as the phone
  address. The running gateway accepts that Host and Origin within about a
  second, without a restart. It does not replace an address you set yourself
  with `pterm config origin`;
- asks a **separate** question before it runs
  `tailscale serve --bg --https=443 http://127.0.0.1:8787`. Your `y` there is
  the only authorization. It asks only when HTTPS 443 serves nothing else and
  Funnel is off; otherwise it lists the conflict and changes nothing. It never
  enables Funnel;
- after that, only when HTTPS 443 serves Phone Terminal, asks a **second,
  separate** question before it runs
  `tailscale serve --bg --https=8443 http://127.0.0.1:8789` for dev-server
  previews (see below). It changes nothing if HTTPS 8443 already serves
  something else;
- adds your home folder as project "Home" when no project exists;
- goes straight to pairing (below).

If Tailscale is missing or signed out, setup tells you the exact next step
(install it, or run `tailscale up`) and still continues: the pairing link then
uses `http://127.0.0.1:8787`, which works in a browser on this computer. Run
`pterm setup` again after Tailscale works to add your phone.

`pterm setup --apply --yes` is the installer path: no questions. It registers
startup and saves the Tailscale phone address if none is set. It never changes
Tailscale Serve and does not pair. `--apply` refuses while a broker or gateway
already runs without startup registration (for example after `pterm launch`):
the registered copy could not start and would restart forever. Stop the
process it names, then rerun.

What startup registration adds:

| | macOS | Windows |
| --- | --- | --- |
| Broker | `~/Library/LaunchAgents/dev.phone-terminal.broker.plist` | task `Phone Terminal Broker (<user>)` |
| Gateway | `~/Library/LaunchAgents/dev.phone-terminal.gateway.plist` | task `Phone Terminal Gateway (<user>)` |
| Start | at login (`RunAtLoad`) | at your logon (gateway 10 s later), normal priority |
| Restart | on failure (`KeepAlive` / `SuccessfulExit=false`) | Task Scheduler "restart on failure" (every minute, up to 999 times); not yet verified through `conhost.exe --headless` |
| Command | `launchctl bootstrap gui/<uid> <plist>` | `schtasks /Create /TN <name> /XML <file> /F`, then `/Run` |
| Logs | `~/Library/Application Support/Phone Terminal/logs/` | `%LOCALAPPDATA%\Phone Terminal\logs\` (pterm writes them; the headless console shows nothing) |

On both systems the gateway waits up to two minutes for the broker when it
starts, so a slow broker start at login does not stop it.

### Pairing a phone

`pterm pair` (and the end of `pterm setup`) prints the link and, on a
terminal, a QR code of the same link in black on white. Then it waits until
the link expires (two minutes). When the phone opens the link it shows a
code, and pterm asks:

```text
Approve only if the phone shows exactly this code.
Phone "iPhone" shows code 123-456. Approve? [y/N]
```

Answer `y` only if the codes are the same. Anything else leaves the phone
unpaired. pterm never approves without your answer.

- `--no-wait` prints the link and returns. Then approve with
  `pterm pair pending` and `pterm pair approve <pending-id>`.
- `--no-qr` prints only the link.
- `--origin <https-origin>` uses another address for this one link.

The link and the QR code are a single-use secret: do not share them in chats,
issues, screenshots or logs.

Other commands: `pterm project add <path>` adds more projects,
`pterm devices` lists phones, `pterm devices rename <device-id> <label>`
renames one, `pterm revoke <device-id>` removes one. Flags such as `--dev`
may come before or after IDs and paths.

`pterm doctor` repeats the read-only checks and exits non-zero if it finds a
problem. It never prints secrets.

`pterm launch` (the app and the Start-menu shortcut use it) starts the broker
and gateway if they are not running and opens `http://127.0.0.1:8787/`.

## Phone access with Tailscale Serve

`pterm setup` offers to run this for you, and runs it only after you answer
`y`. You can also run it yourself on the host:

```sh
tailscale serve --bg --https=443 http://127.0.0.1:8787
```

Then open `https://<host>.<tailnet>.ts.net/` on your phone (on the same
tailnet). If HTTPS 443 already serves something else, setup reports it and
does not change it: resolve that yourself; do not overwrite another service.
If you set up Serve by hand under another name, tell pterm with
`pterm config origin https://<that-name>`; the gateway accepts it at once.

### Dev-server previews (optional, HTTPS 8443)

The phone can show a web page that a program in a session serves (for example
`npm run dev`). The page is served on a separate origin, so it needs a second
Serve port. `pterm setup` asks separately, or run it yourself:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:8789
```

Previews then open inside the app at `https://<host>.<tailnet>.ts.net:8443/`.
Only ports that the session's own processes listen on are shown. `pterm
doctor` says whether this is set up; it is optional and never a doctor
failure. The gateway serves only dev-server previews on 127.0.0.1:8789, never
the app. Design and security: [preview.md](preview.md).

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

`pterm setup --apply` registers them again. On Windows, `pterm doctor` reports
a disabled task, and setup creates it again, enabled.

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
broker has active sessions or does not answer. On Windows, ending a task may
end only its console host, so uninstall then also stops the gateway and shuts
down the broker, and only after it checks again that no session is active.
Your data stays unless you pass
`--delete-data`, which also refuses while any broker still runs (for example
one started by `pterm launch` without startup registration); stop the broker
it names first.

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
groups, prints the loopback URL (default `http://127.0.0.1:8788/`) and the
matching `pterm pair --dev ...` command, and stops both on Ctrl+C. Killing the
gateway does not stop the broker. `--data-dir`, `--runtime-dir` and `--listen`
select other development locations. `--preview-listen` sets the dev-server
preview listener (default `127.0.0.1:8790`; `off` disables it). It changes no startup, Tailscale or
production state.

A full local run from a source checkout, with isolated temporary directories:

```sh
pnpm install --frozen-lockfile
pnpm build:web && ./scripts/embed-web.sh
go build -tags webdist -o bin/pterm ./cmd/pterm
mkdir -p /tmp/pt/data /tmp/pt/run && chmod 700 /tmp/pt/data /tmp/pt/run
./bin/pterm up --dev --data-dir /tmp/pt/data --runtime-dir /tmp/pt/run
```

The `/tmp` and `chmod` lines are for macOS and Linux only. On Windows, build
`bin\pterm.exe` and leave out `--data-dir` and `--runtime-dir`: the development
profile then uses `%LOCALAPPDATA%\Phone Terminal Development`. In PowerShell:
`.\bin\pterm.exe up --dev`, and in the second terminal the same commands with
`.\bin\pterm.exe` and without `--runtime-dir /tmp/pt/run`.

In a second terminal:

```sh
./bin/pterm project add --dev --runtime-dir /tmp/pt/run --name demo <project-dir>
./bin/pterm config origin --dev --runtime-dir /tmp/pt/run http://127.0.0.1:8788
./bin/pterm pair --dev --runtime-dir /tmp/pt/run
./bin/pterm list --dev --runtime-dir /tmp/pt/run
./bin/pterm attach --dev --runtime-dir /tmp/pt/run <session-id>
```

Open the pairing link in a browser, compare the code and answer `y` (or use
`pterm pair pending` and `pterm pair approve <pending-id>` with `--no-wait`).
Start a session and tap **Take control**. `pterm attach` starts read-only;
`--control` takes control. Ctrl+] detaches without ending the session.
