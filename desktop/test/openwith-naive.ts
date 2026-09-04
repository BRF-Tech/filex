// The version of "Open with filex" that anyone would write first.
//
// This is not dead code and it is not a joke: it is the control the real
// implementation is measured against. Every case in openwith-cases.ts is run
// against BOTH, and scripts/openwith-red.mjs fails if a single case passes here
// — because a case a first draft already satisfies is a case that is measuring
// nothing while looking like it measures something.
//
// Each function below is naive in one specific way, and the comment says which
// trap that is.

import fs from 'node:fs';

import type { Impl } from './openwith-cases.ts';

export const NAIVE: Impl = {
  // Trap: "anything that is not a switch is a document". Loses the `filex://`
  // sign-in link into the document list, and takes the project directory of an
  // `electron .` dev run for a file.
  classifyArgv(argv) {
    return { deepLinks: [], files: argv.slice(1).filter((a) => !a.startsWith('-')) };
  },

  // Trap: "the first pair whose folder is a prefix of the path". Case-sensitive
  // (wrong on Windows), prefix-only (a sibling folder called `docsarchive`
  // matches `docs`), first-match (a shallow pair steals a deeper pair's file),
  // and blind to a paused pair.
  resolveSyncTwin(localPath, pairs) {
    const hit = pairs.find((p) => localPath.startsWith(p.local));
    if (!hit) return null;
    const rest = localPath.slice(hit.local.length + 1).split(/[\\/]/).join('/');
    return { pairId: hit.id, remote: hit.remote + '/' + rest };
  },

  // Trap: "make it safe by keeping only ASCII". Destroys every Turkish name,
  // which is most of the names this feature was built for.
  scratchBasename(localPath, sessionId) {
    const base = localPath.split(/[\\/]/).pop() ?? '';
    return sessionId + '-' + base.replace(/[^A-Za-z0-9._-]/g, '_');
  },

  // Trap: "write the new bytes over the file". A crash mid-write truncates the
  // user's only copy, a deleted document is silently recreated, and a write the
  // OS refuses leaves nothing behind but an error nobody sees.
  async writeBackAtomic(target, bytes) {
    await fs.promises.writeFile(target, bytes as never);
  },

  // Trap: "we are the only instance, so everything on disk is old". Deletes the
  // working copy of a document being edited right now.
  staleSessions(sessions) {
    return [...sessions];
  },

  // Trap: "no record means nobody wants it". Deletes a copy a session created
  // seconds ago on another machine, and one whose age is unknown.
  orphanScratchEntries(entries, known) {
    return entries.filter((e) => !known.has(e.basename)).map((e) => e.basename);
  },
};
