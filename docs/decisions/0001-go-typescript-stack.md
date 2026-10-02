# ADR 0001: Go host runtime and TypeScript phone interface

- Status: Accepted; implemented in v0 (2026-09-27); still current (2026-10-02)
- Date: 2026-09-20

## Context

The shipped companion must own native PTYs on macOS and Windows, supervise durable host processes, serve a small phone interface, and install without a Node runtime. A single explicit stack avoids split lifetime ownership and divergent protocol implementations.

## Decision

Use Go 1.27.1 for every shipped host-runtime component, with standard-library services and the pinned terminal/network dependencies. Use strict TypeScript, React, Vite, and CSS Modules for the phone interface. Embed the production web build in the Go executable. JSON Schema Draft 2020-12 is the canonical protocol source, with generated Go and TypeScript types.

Node.js 24 LTS and pnpm are build-time tools only. Exact compatible dependency versions and workflow actions are pinned (done in P0).

## Consequences

The installer does not require Node.js. Broker, gateway, and CLI share Go protocol and platform foundations, while the web application retains strong browser tooling. Builds must include protocol-generation drift checks and both Go and TypeScript validation. A Node companion, Rust backend, cloud shell, and second wire encoding are outside v0.

Implementation note (2026-09-27): the phone interface uses one global stylesheet (`web/src/styles/global.css`) instead of CSS Modules.
