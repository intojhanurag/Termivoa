# Pending work

Date: 2026-09-27. The owner deferred these items to work on the UI first.
Ask the agent to "do pending work" to complete them.

## Road to public beta (updated 2026-09-28)

Done: private betas `v0.1.0-beta.1` and `v0.1.0-beta.2` released, website and docs live at
<https://termivoa.vercel.app> and <https://termivoa.vercel.app/docs/>.

### Before the public beta (the repo goes public)

- [ ] **Real-device tests.** Linux (VPS `t3-vps`), Windows laptop, Android
      phone. Also test the update steps in the docs once
      (`pterm shutdown` → download → `pterm launch`).
- [ ] **CI.** Run GitHub Actions after the minutes reset (early October) and
      fix what fails. See "After the GitHub Actions minutes reset" below.
- [ ] **24-hour soak test.** Sessions running for a full day; check memory
      and reconnects (`scripts/soak.sh`).
- [ ] **Secret scan of the full git history** before it becomes public.
- [ ] **Licence audit.** Finish the notice audit in
      `THIRD_PARTY_NOTICES.md` and `docs/dependencies.md`.
- [ ] **One-line installer.** `curl … | sh` for macOS and Linux. Needs a
      public repo or the binaries hosted on the website.
- [ ] **Owner confirms** the `SECURITY.md` response times (7 days / 30 days).
- [ ] **Owner makes the repo public.** Only the owner does this.

### Small code items

- [ ] Staged update refusal and diagnostics export (see "Backend gaps" below).
- [ ] `pterm setup` asks "Pair another phone? [y/N]" when a phone is already
      paired.

### Money (owner decisions, all can wait)

- [ ] **Domain** for the website, about $10–15 a year. Any time.
- [ ] **Apple Developer Program**, $99 a year, to sign and notarize the Mac
      app. Only before v1.0; `gh`/`curl` installs need no signing.
- [ ] **Windows code-signing certificate.** Only if many Windows users come.

### Before v1.0

- [ ] Security review by an outside person.
- [ ] Signed macOS and Windows apps.

## Windows (logic only; native testing is still an owner gate)

- [x] **WSL bash profile.** Done in #38 (needs a real-Windows check). Detect WSL and add a "WSL bash" shell profile when
      WSL is installed (plan section 5: a separate explicit profile, never a
      replacement for native Windows coverage). `store.PlatformWSL` exists, but
      `internal/broker/shell_profiles.go` does not add it.
- [x] **Ctrl+] at the attach startup handoff.** Done in #37 (needs a real-Windows check). On Windows a Ctrl+] pressed
      exactly while `pterm attach` starts is lost; Unix drains and scans for it.
- [x] **Scheduled Task launcher.** Kept `conhost --headless` after review; added logs, normal priority, a late-broker wait and a clean uninstall stop in #40. Tasks start pterm through
      `conhost.exe --headless`. Restart on failure and `schtasks /End` stopping
      pterm are unproven. Consider starting `pterm.exe` directly and hidden.

Also fixed before the first Windows test (#39): SQLite path URI on Windows and
the current-user-only folder ACL check (both would have stopped `pterm setup`).

## Backend gaps from the plan

- [ ] **Staged update refusal.** The broker must refuse replacement while
      sessions are active (plan section 11; `docs/install.md` marks it pending).
- [ ] **Diagnostics export.** A user action with a preview before it writes a
      local file; no secrets, input, output or environment (plan section 10).
- [ ] **Desktop mouse forwarding.** Documented as out of scope for v0; do only
      if asked.

## Owner decisions

- [ ] **PTY-session kill boundary.** Keep (recommended) or revert. See
      `docs/owner-verification.md` section 6.

## After the GitHub Actions minutes reset

- [ ] Run CI on `main` (PRs #27 and #28 merged without CI) and fix any Windows
      or Linux failures.
