# Archives

filex can create and extract archives directly from the file explorer. Select
one or more files or folders and choose **Create archive…**; select a supported
archive file and choose **Extract archive…**. Archive files are detected by
extension, so extraction is available from both the context menu and the
selection toolbar.

## Providers and formats

| Provider | Create | Extract | Encryption |
|---|---|---|---|
| Built-in Go ZIP | ZIP | ZIP | No |
| 7-Zip (`7zz` or `7z`) | ZIP, 7z, TAR, TAR.GZ, TAR.BZ2, TAR.XZ | ZIP, 7z, RAR, TAR, TAR.GZ, TAR.BZ2, TAR.XZ, gzip, bzip2, xz | ZIP and 7z |

The full Docker image includes 7-Zip. The slim image deliberately does not;
install a provider alongside filex or point filex at one with the environment
variables below. 7-Zip can extract RAR archives, but RAR creation is deferred
to a later release because it requires the proprietary RAR CLI and a licence.

Compressed TAR creation streams a TAR-producing 7-Zip process directly into a
gzip, bzip2 or xz process. Both processes are started without a shell, managed
as one cancellable operation and report through one progress item. The TAR
layer is never materialised on disk.
Bare gzip, bzip2 and xz remain extraction-only because they are single-stream
compressors rather than multi-file archive formats. TAR formats do not support
archive passwords; use ZIP or 7z when encryption is required.

The create dialog exposes 7z's solid mode and a bounded dictionary-size
selection (4–256 MiB). Larger dictionaries can improve compression for some
data, but require more memory both when creating and extracting the archive.
The bounds are enforced by the API as well as the browser; arbitrary 7-Zip
switches are never accepted from a request.

Archive creation runs through filex's asynchronous file-operation queue. Once
the request has been validated, the dialog closes and progress appears in the
operations center while source files are staged, compressed and written to the
destination. Completion and failure use the same in-app feedback as copy, move
and delete operations. Creation emits one `archive.created` notification and
extraction emits one `archive.extracted` notification, rather than reporting
the component writes as generic "new file" events. Both retain the ordinary
realtime folder update and antivirus scan behaviour.

## Process configuration

| Environment variable | `config.yaml` | Default |
|---|---|---|
| `FILEX_ARCHIVE_7Z_BIN` | `archive.sevenzip_bin` | resolve `7zz`, then `7z`, from `PATH` |
| `FILEX_ARCHIVE_WORK_DIR` | `archive.work_dir` | `<data-dir>/archive-work` |

Executable paths are not editable in the admin UI. Allowing an HTTP request to
select a program for the server to execute would turn archive configuration
into arbitrary command execution. **Settings → Archives** controls the live
policy instead: enabled state, allowed/default creation formats, expanded-size
and member limits, timeout, provider status and an encrypted round-trip test.

Example:

```yaml
archive:
  sevenzip_bin: /usr/local/bin/7zz
  work_dir: /var/lib/filex/archive-work
```

## Safety model

- Commands are executed directly without a shell and have a fixed argument
  shape.
- Input archives and source files are copied into a private workspace first.
- Member paths are validated before and after extraction; absolute paths,
  traversal, links and special files are rejected.
- Declared and actual expanded sizes and entry counts are bounded by policy.
- Extraction writes through the same ACL, overwrite/versioning and catalogue
  hooks as ordinary file operations.
- Provider commands inherit request cancellation and have a configurable hard
  timeout.

Passwords are used only for the requested operation and are never stored in
the database. Queue metadata is persistent, but the executable archive job is
kept in memory for this reason. If filex restarts before an encrypted archive
finishes, the operation is marked failed and must be submitted again; its
password cannot be recovered from the queue. The 7-Zip command-line interface accepts passwords as command
arguments, so on operating systems where users can inspect another process's
command line, run filex under a dedicated OS account and restrict process
visibility.
