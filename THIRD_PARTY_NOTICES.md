# Third-party notices

This file is a preparatory dependency inventory for private development. Direct and transitive versions are locked, but no distribution artifact exists yet and the full notice audit is pending. Before a release candidate is assembled, generate an SBOM from the locked dependency manifests, inspect transitive dependencies, preserve required upstream `LICENSE` and `NOTICE` material, and replace this preparatory list with the exact shipped inventory.

## Planned direct runtime dependencies

| Component | Planned baseline | Upstream license to verify against locked source |
| --- | --- | --- |
| `github.com/coder/websocket` | v1.8.15 | ISC |
| `github.com/charmbracelet/x/xpty` | v0.1.4 plus repository patch; see `docs/dependency-patches.md` | MIT |
| `github.com/charmbracelet/x/vt` | v0.0.0-20260920004010-53e2afe73ae5 plus repository patch; see `docs/dependency-patches.md` | MIT |
| `github.com/charmbracelet/ultraviolet` | initially v0.0.0-20260303162955-0b88c25f3fff | MIT |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | MIT |
| `modernc.org/sqlite` | v1.59.0 | BSD-3-Clause; locked source also carries SQLite's public-domain dedication and the bundled sqlite-vec MIT notice |
| `github.com/Microsoft/go-winio` | v0.6.2 | MIT |
| React | 19.3.0 | MIT |
| Vite | 8.3.0 | MIT |
| `@xterm/xterm` | 6.0.0 | MIT |
| `@xterm/addon-webgl` | 0.19.0 | MIT |
| `@xterm/addon-unicode-graphemes` | 0.4.0 | MIT |
| `@tanstack/react-virtual` | 3.14.13 | MIT |
| Ajv | 8.20.0 | MIT |
| quicktype | 26.0.0 | Apache-2.0 |

The patched `xpty` and `vt` sources and their upstream MIT license texts are
retained under `third_party/`. Development-only tools, GitHub Actions,
packaging tools, fonts/icons, generated code notices, and all transitive
dependencies must be added after exact versions are locked. Except for the two
explicit local replacements, a name in this file does not mean the component
is currently vendored, linked, or shipped.

Apache-2.0 applies only to original Phone Terminal code. It does not replace any third-party license.
