# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial monorepo skeleton (Go backend + npm packages + admin UI)
- Storage driver interface with reference implementations: `local`, `s3` (Hetzner-tested), `sftp`, `webdav`
- Auth driver interface with reference implementations: `local` (bcrypt), `oidc` (Keycloak-tested), `ldap`
- DB driver interface with reference implementations: `sqlite` (default, modernc.org/sqlite), `mysql`, `postgres`
- Sync worker with ETag-based diff and tombstone-false-positive guard
- Bleve full-text search (embedded)
- Thumbnail pipeline (image GD, video ffmpeg, PDF ghostscript, Office libreoffice; capability-aware)
- Vue 3 admin UI (embedded into Go binary via `go:embed`)
- `@brftech/filex-core` — Vue 3 SFC source of truth
- `@brftech/filex` — Web Component wrapper (`<filex-explorer>`)
- `@brftech/filex-react` — React adapter via `@lit/react`
- First-run console banner with admin credentials + embed instructions
- Multi-platform release matrix (Linux / macOS / Windows × amd64 / arm64) via goreleaser
- Docker images: `brftech/filex:slim` (~40 MB) and `brftech/filex:full` (~250 MB w/ thumbnail tools)
- GitLab CI pipeline (lint + test + build + npm publish + Docker push + release matrix)
- Plug & play external services: OnlyOffice, Drawio (URL-configured, capability-discovered)
- Monaco eager-load with highlight.js fallback for code preview/edit
- MIT license

## [0.1.0] - TBD

First public release.
