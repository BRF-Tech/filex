# File names

A file in filex keeps exactly the name it was given: Turkish, Chinese,
Arabic, emoji, two spaces in a row, a space at the end, `#`, `%`, `?`, a
quote. The name is stored byte for byte, and every surface - the explorer,
the API, WebDAV, S3, SFTP, FTPS, NFS, a ZIP download, a share link, the
desktop app - hands the same bytes back.

This page says which names are refused, what each surface does with a name it
cannot carry, and how filex is tested against hostile names.

## The names filex refuses

One rule, asked by every door that creates a file or a folder or renames one
(the explorer's upload, new folder, new document and rename, the file-request
page, the agent API and MCP tools, WebDAV, S3, SFTP, FTPS and NFS):

| Refused | Why |
|---|---|
| an empty name, and a name of only spaces | there is nothing to call it; Windows drops trailing spaces, and the new-folder and rename boxes trim it to nothing |
| `/` or `\` in the name | a name is one path segment; Windows, a Windows client and an unzip on Windows read `\` as a separator |
| a NUL character | no file system stores it |
| any other control character (U+0001-U+001F, U+007F: a tab, a line break, an escape) | Windows refuses them, an FTP or XML listing cannot carry them, and nobody can see them |
| a name of only dots and spaces with at least one dot (`.`, `..`, `...`, `. .`, `. . .`) | the folder itself or its parent; Windows trims trailing dots and spaces, so on a Windows host each of them names the folder it is in or its parent |

The answer is the client's mistake, in the protocol's own words: `400` from
the API and WebDAV, `InvalidArgument` from S3, `553` from FTPS, a failure
status with the reason in its message from SFTP, `NFS3ERR_ACCES` from NFS (the
NFS server answers every failed create that way), "invalid argument" from a
`filex mount` drive. Only a NEW name is asked: a file that already carries
such a name (written to the disk by something else) can still be read,
overwritten and moved.

Everything else is a name, however it looks. Two kinds of name are accepted
and deserve a word:

- **A name the storage cannot hold for its length.** A local disk holds at
  most 255 bytes of UTF-8 in one name (ext4, XFS, APFS): about 127 Turkish
  letters, or 85 Chinese characters. An object store holds more. filex does
  not cut a name and does not refuse it in advance; when the storage says no,
  the client is told "name too long" (`400 NAME_TOO_LONG` from the API, `400`
  from WebDAV, `KeyTooLongError` from S3, `553` from FTPS, "name too long" from
  SFTP, `NFS3ERR_NAMETOOLONG` from NFS, "file name too long" from a
  `filex mount` drive) and never shown the server's own path.
- **A name with an invisible character.** A zero-width space or a bidi
  control is legal. A RIGHT-TO-LEFT OVERRIDE (U+202E) is the old trick for
  hiding an extension: `invoice` + U+202E + `fdp.exe` is an `.exe` that text
  renders as `invoiceexe.pdf`. Wherever filex shows a name - the explorer's
  rows, grid, breadcrumb, preview title and messages, the public share pages,
  notifications and mail - each bidi control is shown as `�` (U+FFFD), so the
  true extension stays at the end. The stored name, links and searches use
  the real one. A download saves it with `_` in place of the control.

## Spaces at the ends

The explorer's **Rename** and **New folder** boxes trim spaces at either end
of what you type: a space you did not mean is the usual case. An upload, a
dropped folder, WebDAV, S3, SFTP, FTPS and NFS keep the name exactly, so a file
named ` notes.txt` or `report.txt ` exists, opens and downloads under its own
name - never as its neighbour without the space.

## The same name, two spellings

- **Composed and decomposed.** macOS hands names over decomposed (`u` +
  U+0308 for `ü`); a keyboard types them composed. filex stores what it is
  given, so both spellings can sit side by side, and a search finds either
  with either ([Search](SEARCH.md)).
- **Letter case.** `Rapor.docx` and `rapor.docx` are two files on Linux and on
  object storage, one on Windows and macOS.

## What each surface does

- **Downloads.** The `Content-Disposition` header is plain ASCII and carries
  the real name as `filename*` (RFC 6266): what the browser saves is the
  file's own name, with a path separator, a control character or a bidi
  control replaced by `_`. A name with `%` in it is sent in `filename*` too,
  because Chromium decodes `%20` in the plain parameter.
- **ZIP downloads** write every name as UTF-8 with the UTF-8 flag set, so
  Windows, macOS and `unzip` show `Bütçe.xlsx`, not `B├╝t├ºe.xlsx`. A bidi
  control or a control character in a member's name becomes `_`, as in a
  download's name: the copy on someone's disk cannot pass an `.exe` off as a
  `.pdf`. An archive created in filex follows the same rule, and two members
  whose names become one that way are both kept, the second as `a_b (2).txt`.
- **Extracting an archive** keeps every member's name. A ZIP written without
  the UTF-8 flag (Windows' Compressed folders) is read in the DOS code page of
  the account's language: 857 for Turkish and Azerbaijani, 850 for Western
  European, 852 for Central European, 866 for Cyrillic, 862 for Hebrew, and
  437, the ZIP standard's, for any other. A member whose name no door accepts,
  or that is too long for the storage, is left out, and the result lists every
  member left out and why (`skipped`: `name_not_allowed`, `name_too_long`,
  `unsafe_path`).
- **Shared folders** open every file, a backslash in its name included, on a
  storage whose disk is not Windows'.
- **WebDAV** reads `.` and `..` in a URL path as directions, never as names,
  and answers `403` for a copy or a move onto another spelling of the same
  file (`./a.txt`).
- **S3** refuses a key with an empty, `.` or `..` segment (`a//b`, `a/../b`)
  and a key over 1024 bytes (`KeyTooLongError`), and honours
  `encoding-type=url` in listings, so the AWS CLI, boto3 and rclone get
  `C++ notes+draft.txt` and `100%20off.txt` back exactly. A key that already
  holds a control character can be listed exactly only with
  `encoding-type=url`; XML cannot carry it. A `DELETE` removes at most the
  object it names, as on S3: `DELETE folder/` removes an empty folder and
  leaves a folder that still holds files as it is (`204` either way), and
  `DELETE folder` without the slash removes nothing.
- **SFTP** reads a `\` in a path as part of a name on every server, Windows
  included (so a new name holding one is refused, never turned into a
  folder).
- **SFTP, FTPS and NFS** remove a folder with `RMDIR`/`RMD` only when it is
  empty; one that still holds files answers "directory not empty". A
  file-removal command (SFTP `REMOVE`, FTP `DELE`) never removes a folder: it
  answers "is a directory".
- **FTPS** speaks UTF-8 only and cannot carry a line break in a command or a
  listing line: a name holding one (made over another protocol) is left out
  of FTP listings.
- **NFS** leaves a name over 255 bytes out of a listing (NFSv3 cannot carry
  one, and a client would refuse the whole folder), refuses a name holding
  `/`, `.` or `..`, and never removes or renames the exported folder itself.
- **Folder sync** brings a server item this computer cannot hold under its own
  name (on Windows `CON`, `aux.txt`, a `:` or `?`, a trailing dot or space; a
  name longer than the disk holds; on Windows and macOS `Rapor.docx` beside
  `rapor.docx`) down under a safe name (`_CON`, `a_b.txt`, `rapor (2).docx`)
  and says so under the folder; a change made here goes back to the server's
  original name, and nothing on the server is renamed or deleted because of a
  name. When the safe name is taken here, the item is not downloaded and the
  folder says why. A file here whose name the server refuses is reported, not
  uploaded ([Names your computer cannot hold](SYNC.md#names-your-computer-cannot-hold)).
- **Dragging files out of the desktop app** writes a name the computer can
  hold: a forbidden character, a control or bidi control, a trailing dot or
  space becomes `_`, a Windows device name gets a `_` in front, and two names
  that are one name on that computer are kept apart with ` (2)`.

## Known limits

- **NFS answers a refusal in the words its server library allows.** A name
  filex refuses is answered `NFS3ERR_ACCES` (permission denied), and removing
  a folder that is not empty `NFS3ERR_IO`: the NFS server filex is built on
  maps every failed create to the first and every failed remove to the
  second, so `NFS3ERR_INVAL` and `NFS3ERR_NOTEMPTY` never reach the client.
  Nothing is created or removed in either case.
- **A ZIP without the UTF-8 flag is read in the code page of the language of
  the account that opens it** (857 for Turkish, 437 when nothing else fits).
  The archive itself does not say which code page wrote it, so a Turkish
  archive opened from an English account lists its Turkish letters as other
  characters. An archive written with the UTF-8 flag (macOS, 7-Zip, filex,
  any current tool) is always read correctly.
- **An S3 `DELETE folder` without the slash removes nothing**, even when the
  folder is empty: on S3 there is no object of that name. `DELETE folder/`
  removes an empty folder.
- **Folder sync keeps the pairing of a safe name with the server's original
  in its own state** (`names/<pair-id>.json` under the engine's state
  folder). If that file is lost, the next pass takes a file kept under a safe
  name for a new file of its own and uploads it once, under the safe name: a
  second copy on the server, nothing lost
  ([Names your computer cannot hold](SYNC.md#names-your-computer-cannot-hold)).

## Testing against hostile names

`backend/internal/testutil/hostilenames/names.json` is the one list: Turkish,
German, Japanese, Korean, Arabic and Hebrew names, emoji with a skin tone and
a zero-width joiner, a name composed and decomposed, spaces and dots at the
ends, `%20`, `+`, `#`, `?`, `;`, `&`, quotes, a backslash, `..` and `...`,
Windows device names and forbidden characters, names of exactly 255 and 256
bytes, a name under 255 characters but over 255 bytes, a zero-width space, a
right-to-left override, control characters, a pair that differs only in case,
and a path 64 folders deep. Each entry says why it is hostile.

- Go tests read it through `package hostilenames` (`All`, `Creatable`,
  `Refused`, `ForLocalDisk`, `TooLongForLocalDisk`, `Deep`).
- The browser suite, the web unit tests and the desktop scripts read it
  through `e2e/helpers/hostile-names.mjs`.

Invisible and combining characters are written in the file as code points,
never typed. To test a new surface, walk the list; to add a name, add it to the
file - never to a test.
