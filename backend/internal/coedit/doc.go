// Package coedit is filex's relay for editing a file together (task #189):
// the server half of the app SDK's `coedit.*` bridge methods, for an app
// granted `files:co-edit` (docs/APP-PLUGINS-API.md → Editing together).
//
// # The model
//
// Every member's editor applies the same log entries in the same order. The
// filex page (AppFrame, packages/core lib/coeditSession) seals each entry an
// app appends with the session's key (AES-256-GCM, lib/e2eoffice) and hands
// the sealed bytes here. The relay:
//
//   - gives every entry its place: an entry is written against the head the
//     writer has seen (AppendRequest.Base) and lands right after it or not
//     at all (ConflictError). The writer binds the place into the entry's
//     authenticated data, so the order the relay hands out is the order the
//     writers sealed, and a reader notices a gap, a swap or a replay;
//   - keeps the "save lock" promise a Document Server makes with a lease:
//     changes are appended only by the one member holding it, and the lease
//     goes only to a member that has seen every change already in the log;
//   - writes the entries everybody has to agree on itself: who joined (with
//     the editor's per-session user index, never given twice), who left
//     (and when a member's page went away: dropped after MemberTTL), and
//     which part of the log a save holds;
//   - keeps the session's sealed blobs (the base document a member who joins
//     late opens, the images added while editing) as files under its
//     directory.
//
// The relay never opens an entry, a cursor or a blob. Who a member is and
// whether it may write are the server's answer - the account signed in on
// the request and the file's rules, asked by the HTTP layer - never something
// the joining connection says about itself.
//
// # Keys
//
// A session's key is made once, when it starts, and how it is kept depends on
// the folder (OpenRequest.KeyMode):
//
//   - KeyServer, an ordinary folder: the server makes the key and keeps it
//     sealed with FILEX_SECRET_KEY (Options.Keys); each member's filex page is
//     handed the key when it joins. The server holds the file in the clear
//     anyway, so this is not end-to-end encryption: it is the SAME path as an
//     encrypted folder's, and it keeps the unsaved work in the database and
//     the data directory sealed at rest. Without a secret key an ordinary
//     folder is not edited together (ErrNoServerKey).
//   - KeyFolder, an encrypted folder: the members' browsers seal the key with
//     the folder key (SetKey), and the server cannot open it.
//
// # State
//
// Everything is in the database (migration 00107): sessions move by
// compare-and-set on their `rev`, so two filex processes on one database can
// serve one session. Live delivery is per process: a member's socket waits on
// this process's notice of a new entry (Watcher.Notify) and reads the entries
// from the database, so a member connected to another process still gets
// every entry - on that process's poll (Options.PollEvery). Cursor frames are
// not kept: they reach the members connected to the same process.
package coedit
