# filex-watch changelog

filex-watch has a release line of its own: tags `filex-watch-v<version>` in the
filex repository, built by `.github/workflows/filex-watch.yml`. The version is
`AgentVersion` in `backend/internal/watchproto`; the release workflow refuses a
tag that says another one. What it is and how to install it:
[docs/FILEX-WATCH.md](../../../docs/FILEX-WATCH.md).

## [Unreleased]

## [0.1.0] - 2026-11-15

### Added

- The first release (task #352). filex-watch runs on the machine that owns a
  storage's disk - a NAS whose share filex mounts over the network, the server
  an SFTP storage points at - watches the folder, and reports each folder
  whose contents changed to filex, which looks at those folders alone. It
  needs filex 0.61 or later.
- A start, a lost connection, a full queue or an answer from a filex that
  started again since the last one (its `instance` changed) asks filex to
  bring the whole storage up to date once (a resync); a folder the operating
  system will not let it watch (its watch limit) is re-read on a timer
  instead.
- Settings from a JSON file, `FILEX_WATCH_*` variables and flags; the key read
  from a file or `FILEX_WATCH_KEY`, never from a flag. The certificate check
  can be turned off for a self-signed certificate
  (`--insecure-skip-tls-verify`, `FILEX_WATCH_TLS_SKIP_VERIFY`), or a CA
  added (`ca_file`).
- `filex-watch check` sends one report and says what filex answered.
- Binaries for Linux, Windows and macOS on amd64 and arm64 with
  `checksums.txt`, a systemd unit, a launchd daemon, a Windows service, and
  the image `ghcr.io/brf-tech/filex-watch:0.1.0` for linux/amd64 and
  linux/arm64.
