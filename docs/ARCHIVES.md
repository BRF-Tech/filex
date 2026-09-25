# Archives

filex can create and extract archives directly from the file explorer. Select
one or more files or folders and choose **Create archive…**; select a supported
archive file and choose **Extract archive…**. Archive files are detected by
extension, so extraction is available from both the context menu and the
selection toolbar. Opening an archive shows its contents like a folder, and an
encrypted one asks for its password first.

## Providers and formats

| Provider | Create | Extract | Encryption |
|---|---|---|---|
| Built in (Go) | ZIP | ZIP, TAR, TAR.GZ, TAR.BZ2, gzip, bzip2 | No |
| 7-Zip (`7zz` or `7z`) | ZIP, 7z, TAR, TAR.GZ, TAR.BZ2, TAR.XZ | 7z, XZ, TAR.XZ, password-protected ZIP; RAR when that 7-Zip reads it | ZIP and 7z |

filex reads plain ZIP, the TAR family and gzip/bzip2 itself, so those work on
every install. Everything else needs 7-Zip **25.01 or newer**: filex checks the
version and refuses an older or unidentifiable binary (25.00 and 25.01 fixed
symlink handling during extraction and crashes in the ZIP, RAR5 and
compound-document readers — CVE-2025-11001/11002, CVE-2025-53816/53817,
CVE-2025-55188). The full Docker image ships it (Alpine 3.24, 7-Zip 26.01).
The slim image deliberately does not; install a provider alongside filex or
point filex at one with the environment variables below. A server without 7-Zip
offers only ZIP in the create dialog and no password fields.

RAR extraction needs a 7-Zip built with RAR support. Alpine's `7zip` package —
the one in the full image — is built without it (the unRAR licence), so the
image does not extract RAR; the official 7-Zip Linux build from 7-zip.org
(`7zz`) does, and `FILEX_ARCHIVE_7Z_BIN` can point at it. **Settings →
Archives** lists RAR among the extraction formats only when the 7-Zip in use
reads it, and filex does not run 7-Zip for a RAR otherwise. RAR creation is
deferred to a later release because it requires the proprietary RAR CLI and a
licence.

Compressed TAR creation streams a TAR-producing 7-Zip process directly into a
gzip, bzip2 or xz process. Both processes are started without a shell, managed
as one cancellable operation and report through one progress item. The TAR
layer is never materialised on disk.
Bare gzip, bzip2 and xz remain extraction-only because they are single-stream
compressors rather than multi-file archive formats. TAR formats do not support
archive passwords; use ZIP or 7z when encryption is required. A ZIP password
can use ASCII characters only (7-Zip refuses anything else); 7z takes any.

The create dialog exposes 7z's solid mode and a bounded dictionary-size
selection (4–256 MiB). Larger dictionaries can improve compression for some
data, but require more memory both when creating and extracting the archive.
The bounds are enforced by the API as well as the browser; arbitrary 7-Zip
switches are never accepted from a request.

Archive creation and extraction run through filex's asynchronous
file-operation queue, in a lane of their own: one archive operation at a time,
never in front of copies, moves, deletes and upload commits. Once the request
has been validated, the dialog closes and progress appears in the operations
center while source files are staged, compressed and written to the
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
policy instead: whether 7-Zip is used, allowed/default creation formats,
expanded-size and member limits, timeout, provider status and an encrypted
round-trip test.

Example:

```yaml
archive:
  sevenzip_bin: /usr/local/bin/7zz
  work_dir: /var/lib/filex/archive-work
```

### The workspace

Extraction unpacks into a private folder under `archive.work_dir` and copies
the members into the destination from there, so that folder needs room for one
archive's expanded size (the limit set under **Settings → Archives**). Put it on
fast local storage, and preferably not on the file system that holds the
database: an archive that is being unpacked fills the workspace first.

How the limits hold while an archive is unpacked depends on who reads it:

- **TAR, TAR.GZ, TAR.BZ2, gzip, bzip2 — and TAR.XZ/XZ, whose xz layer alone
  7-Zip decompresses into a pipe:** filex reads every member itself. A member
  is counted before it is created and every byte goes through a writer that
  refuses the first one past the limit, so an archive whose headers lie about
  its size (a gzip "bomb") stops at the limit exactly. Nothing is written
  twice: a `.tar.gz` is read in one pass, without an intermediate `.tar`.
- **Plain ZIP** is read by Go's `archive/zip`, which stops each member at the
  size it declares; the declared total is checked before anything is written.
- **7z, RAR and password-protected ZIP:** 7-Zip writes into the workspace. Its
  decoders stop at each member's declared size (a member whose headers lie
  ends in a data error at the size it claimed), and the declared totals are
  checked before extraction starts; a member that declares no size at all
  (possible in RAR5) is refused. While 7-Zip runs, filex also walks the
  workspace (every 100 ms, slowing to at most every 2 s on a large tree) and
  stops 7-Zip if either limit is crossed, then checks once more at the end.
  That walk is a backstop — it can be passed by what 7-Zip writes between two
  looks (measured on a gzip whose trailer lies: 5–20 MiB past the limit) —
  which is why the formats filex can read itself never rely on it.

## Safety model

- Commands are executed directly without a shell and have a fixed argument
  shape. The archive type is always named (`-t7z`, `-tzip`, …) from the file's
  extension, never guessed from its bytes, so a file reaches only the 7-Zip
  reader for the format it claims to be.
- Passwords reach 7-Zip on its standard input, never on its command line
  (which other accounts on the host can read). They are used only for the
  requested operation and are never stored in the database.
- Input archives and source files are copied into a private workspace first.
- Member paths are validated before and after extraction; absolute paths and
  traversal are rejected. So is the whole archive when it holds a link
  (symbolic, hard, or a RAR5 file copy) or a special file (FIFO, device,
  socket) — whether 7-Zip lists it as a link or only by its file mode.
- Declared sizes and entry counts are checked before extraction; the limits
  hold while extracting as described above.
- Every member lands through the same gate as any other write: ACL, app locks
  (a document under signature is not replaced), filex's own folders
  (`.versions`, `.thumbs`, …) refused, overwrite snapshots and the catalogue.
- Provider commands inherit request cancellation and have a configurable hard
  timeout.

Queue metadata is persistent, but the executable archive job is kept in memory
so a password is never written anywhere. If filex restarts before an archive
operation finishes, it is marked failed and must be submitted again; its
password cannot be recovered from the queue.
