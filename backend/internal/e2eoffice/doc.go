// Package e2eoffice is the server's half of editing an end-to-end encrypted
// office document with ONLYOFFICE in the browser (task #189): a relay that
// puts the editors' sealed entries in one order without being able to read
// them.
//
// # The model
//
// The editor runs in the browser. Its Document Server protocol is answered by
// a bridge in the editor's frame (the filex-office-editor app, AGPL), never by a
// Document Server, so the document, its changes, its locks and its images do
// not leave the browser in the clear. Whatever has to be shared with the other
// people editing - a batch of changes, a lock request, a released lock - the
// filex page seals with the session key (AES-256-GCM, packages/core
// lib/e2eoffice.ts) and hands to this relay. The relay:
//
//   - gives every entry its place: an entry is written against the head the
//     writer has seen (Append's Base) and lands right after it or not at all
//     (ConflictError). The writer binds the position into the entry's
//     authenticated data, so the order the relay hands out is the order the
//     writers sealed, and a reader notices a gap, a swap or a replay;
//   - keeps the Document Server's "save lock" promise with a lease: changes
//     are appended only by the one member holding it, and the lease goes only
//     to a member that has seen every change already in the log (Lease). Two
//     editors therefore never send changes built on different states - the
//     guarantee CryptPad's bridge gives up by always answering "not locked";
//   - writes the control entries everyone has to agree on itself: who joined
//     (with the indexUser the editor needs, never reused in a session), who
//     left, and which part of the log a save holds (Saved). Locks are not
//     arbitrated here: their requests are sealed, every bridge applies the
//     Document Server's rules to the same sequence and reaches the same
//     table;
//   - holds the sealed session key, the sealed base document and its sealed
//     images, so a late joiner opens exactly the bytes the first editor did.
//
// What the relay sees is metadata: who is in which session, when, how many
// entries of which kind and how big. docs/E2E-OFFICE.md lists it.
//
// # Status: prototype
//
// Nothing routes to this package yet; no surface of the product can reach it,
// so there is no switch to turn it off. It keeps its state in memory, in one
// process: a restart forgets every session, and two filex instances behind
// one database would each have their own. The routes, the database store
// (sessions and log as tables, the lease as a compare-and-set row) and the
// blob store inside the encrypted folder come with the slice that wires it,
// behind a setting that is off by default.
package e2eoffice
