# macOS packaging

Unsigned local artifacts are built first; signing and notarization are a
separate owner-authorized step.

```sh
./packaging/macos/build-app.sh   # dist/macos/Phone Terminal.app (unsigned, arm64)
./packaging/macos/build-dmg.sh   # artifacts/Phone-Terminal-<version>-macos-arm64-unsigned.dmg (+ .sha256)
```

`PTERM_VERSION` (default `0.0.0-dev`) sets the version; the embedded build ID
is `<version>+<commit>` (`.dirty` for uncommitted trees).

Bundle layout:

- `Contents/MacOS/Phone Terminal`: small Go launcher (`launcher/`). It runs
  `pterm launch`, which starts or checks the per-user broker and gateway and
  opens `http://127.0.0.1:8787/`. `LSUIElement` keeps it out of the Dock.
- `Contents/MacOS/pterm`: the CLI (`CGO_ENABLED=0`, `-trimpath`). Users who
  want it on their PATH link it themselves; nothing changes PATH automatically.
- Bundle ID placeholder: `dev.phone-terminal.companion`.

Startup registration is not part of the bundle. `pterm setup --apply` writes
two per-user LaunchAgents (`dev.phone-terminal.broker`, `dev.phone-terminal.gateway`)
after describing them and asking for confirmation. It refuses to register a
pterm running from the mounted DMG or from App Translocation.

## Signing (owner only)

`sign-and-notarize.sh` refuses to run unless `DEVELOPER_ID_APPLICATION` (the
keychain identity name) and `NOTARYTOOL_KEYCHAIN_PROFILE` (a profile stored
with `xcrun notarytool store-credentials`) are set. It signs with the hardened
runtime, notarizes and staples the app, then builds, signs, notarizes and
staples `artifacts/Phone-Terminal-<version>-macos-arm64.dmg`. No credential is
read from or written to the repository.
