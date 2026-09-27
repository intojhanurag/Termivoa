# Pending work

Date: 2026-09-27. The owner deferred these items to work on the UI first.
Ask the agent to "do pending work" to complete them.

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
