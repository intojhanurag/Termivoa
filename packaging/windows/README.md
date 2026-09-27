# Windows packaging

Per-user Inno Setup installer. No administrator rights, services or
machine-wide changes.

```powershell
./packaging/windows/build-installer.ps1            # unsigned
./packaging/windows/build-installer.ps1 -Sign      # owner only, see below
```

It cross-builds `dist/windows/pterm.exe` (`GOOS=windows GOARCH=amd64
CGO_ENABLED=0`, `-trimpath`) and compiles `phone-terminal.iss` with Inno Setup 6
(`ISCC` env var or the default install path) into
`artifacts/Phone-Terminal-<version>-windows-x64-unsigned-setup.exe` (+ `.sha256`).
CI builds it in `.github/workflows/packaging.yml` with a pinned Inno Setup.

Installer behavior:

- Installs to `%LOCALAPPDATA%\Programs\Phone Terminal\versions\<version>\pterm.exe`.
  Each version gets its own directory, so an update never overwrites the
  executable a running broker uses, and the previous version stays for rollback.
- Task "Start Phone Terminal when I sign in" (checked) runs
  `pterm setup --apply --yes`, which registers two per-user scheduled tasks
  (broker, gateway): at logon, least privilege, hidden, restart on failure,
  started through `conhost.exe --headless` so no console window appears.
  If the broker has active sessions, or a broker or gateway already runs
  without startup registration, setup refuses and the installer says so.
- Task "Add pterm to my PATH" is unchecked by default and edits only the
  current user's PATH.
- The uninstaller first asks whether to delete data (default: keep), then runs
  `pterm uninstall --dry-run` with that choice and stops if the broker has
  active sessions, does not answer, or (when deleting data) runs without
  startup registration. It then removes the tasks; data is deleted only after
  no broker is running.

## Signing (owner only)

`sign.ps1` refuses unless `PTERM_SIGN_CERT_THUMBPRINT` (40 hex characters; the
certificate stays in the user's certificate store) and
`PTERM_SIGN_TIMESTAMP_URL` are set. `-Sign` signs `pterm.exe`, the installer
and the uninstaller with SignTool (SHA-256, RFC 3161 timestamp).
