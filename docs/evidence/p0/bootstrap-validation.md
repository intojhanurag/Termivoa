# P0 bootstrap validation

Date: 2026-09-20 17:02 IST
Environment: Apple Silicon arm64, macOS 26.5.2
Repository: `intojhanurag/phone-terminal`

## Repository preflight

The authenticated GitHub account was verified as `intojhanurag`. The target did
not exist before creation. It was created with an explicit private flag, then
verified through the GitHub API as owner `intojhanurag`, visibility `private`,
with admin/push/pull access. No token value was recorded.

## Toolchain resolution

- `GOTOOLCHAIN=go1.27.1 go version` returned Go 1.27.1 for darwin/arm64.
- Existing nvm Node 24.11.0 was selected; pnpm 10.33.0 was activated.
- `go mod download` resolved the pinned direct Go modules and wrote `go.sum`.
- `pnpm install` resolved the pinned workspace and wrote `pnpm-lock.yaml`.

## Checks run

- `./scripts/check`: passed after adding Vite CSS module declarations. It ran
  protocol generation, Go formatting/vet/tests, TypeScript checking, web tests,
  and the production Vite build. No frontend test files exist yet; Vitest
  accurately reported that and exited under the explicit P0 scaffold policy.
- `go test -race ./...`: passed; current packages contain scaffold code only.
- `CGO_ENABLED=0 go build -trimpath ./cmd/pterm`: passed and produced a local
  arm64 Mach-O development binary outside the repository.
- Gitleaks 8.30.1 directory scan: passed with no leaks found.
- govulncheck 1.8.0: passed with no vulnerabilities found in the current Go
  scaffold.
- `pnpm audit --audit-level high`: reported no known vulnerabilities.
- `git diff --cached --check` and explicit staged-path/secret-pattern review:
  passed after removing one trailing blank line from the license file.

These checks validate the bootstrap, not PTY behavior, protocol completeness,
platform integration, authentication, phone UX, installation, or release
readiness. Those gates remain pending.
