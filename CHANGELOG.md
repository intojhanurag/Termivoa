# Changelog

All notable changes will be documented here. The project intends to follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and Semantic Versioning once releases begin.

## [Unreleased]

### Added

- Private repository policy and open-source preparation documents.
- Initial architecture and locked-decision records.
- Milestone progress and release-readiness checklists.
- Authoritative Go terminal core, normalized frame protocol, and display-only
  browser/desktop client foundations.
- Broker controller leases, bounded structured-input ingress, and bounded local
  IPC handshake framing.
- Broker-owned live PTY session runtime with viewer-independent query replies,
  structured input, final-screen retention, and ordered lifecycle shutdown.
- Broker-local session registry with random process generations, create-operation
  deduplication, bounded live/retired resources, and coordinated shutdown.
- Broker-owned SQLite migrations and validated project, shell-profile, and
  session metadata, including restart recovery that marks prior live sessions
  lost without replaying commands.
- Canonical bounded local broker-management RPC for session list, create,
  rename, terminate, and status, with mutation operation IDs and stable errors.

### Not yet implemented

- Durable mutation receipts, authentication/settings persistence, platform IPC
  listeners, and separate gateway process.
- Connected desktop attachment and complete phone interface.
- Pairing, private network setup, installers, signing, and release artifacts.

No version has been released.
