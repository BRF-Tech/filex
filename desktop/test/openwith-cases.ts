// The measurements for "Open with filex", written ONCE and run twice.
//
// Every case here is also run against a deliberately naive implementation of
// the same six functions (openwith-naive.ts) by scripts/openwith-red.mjs, which
// fails if any of them passes there. That is the red proof: a case that a
// first-draft implementation already satisfies is a case that measures nothing,
// and this file exists so that cannot go unnoticed.
//
// Each case throws on failure (node:assert) and is given its own empty
// directory to work in.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';

import type {
  ArgvIntent,
  OpenWithSession,
  RemoteStat,
  SyncPairView,
  SyncTwin,
} from '../src/openwith.ts';

/** The six functions under measurement. */
export interface Impl {
  classifyArgv(argv: readonly string[], opts?: { defaultApp?: boolean; scheme?: string }): ArgvIntent;
  resolveSyncTwin(
    localPath: string,
    pairs: readonly SyncPairView[],
    opts?: { platform?: NodeJS.Platform },
  ): SyncTwin | null;
  scratchBasename(localPath: string, sessionId: string): string;
  writeBackAtomic(
    target: string,
    bytes: Buffer | Uint8Array,
    opts?: { fallbackDir?: string; now?: Date },
  ): Promise<void>;
  staleSessions(
    sessions: readonly OpenWithSession[],
    opts: { currentPid: number; now?: number; maxAgeMs?: number },
  ): OpenWithSession[];
  orphanScratchEntries(
    entries: readonly { basename: string; lastModified?: number }[],
    known: ReadonlySet<string>,
    opts?: { now?: number; maxAgeMs?: number },
  ): string[];
}

export interface Case {
  group: string;
  name: string;
  run(impl: Impl, dir: string): Promise<void>;
}

const EXE = 'C:\\Users\\ada\\AppData\\Local\\Programs\\filex\\filex.exe';

function session(over: Partial<OpenWithSession>): OpenWithSession {
  return {
    id: 'a1',
    accountId: 'acc',
    serverUrl: 'https://files.example',
    storage: 'docs',
    localPath: 'C:\\docs\\a.docx',
    remote: 'docs://.filex-open/a1-a.docx',
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    seen: null,
    ownerPid: 1234,
    ...over,
  };
}

export const CASES: Case[] = [
  // ── argv ───────────────────────────────────────────────────────────
  {
    group: 'argv',
    name: 'a sign-in deep link is a deep link, never a document to open',
    async run(impl) {
      const got = impl.classifyArgv([EXE, 'filex://auth?state=s&code=c']);
      assert.deepEqual(got.deepLinks, ['filex://auth?state=s&code=c']);
      assert.deepEqual(got.files, []);
    },
  },
  {
    group: 'argv',
    name: "a dev run's project path and Chromium's own switches are not documents",
    async run(impl) {
      const got = impl.classifyArgv(
        ['electron.exe', 'G:\\filex\\desktop', '--user-data-dir=C:\\tmp\\p', '--lang=en-US', 'C:\\Belgeler\\a b.docx'],
        { defaultApp: true },
      );
      assert.deepEqual(got.files, ['C:\\Belgeler\\a b.docx']);
      assert.deepEqual(got.deepLinks, []);
    },
  },
  {
    group: 'argv',
    name: 'a file:// URL becomes a path, a UNC path survives, any other scheme is ignored',
    async run(impl) {
      const got = impl.classifyArgv([
        EXE,
        'file:///C:/Bel%20geler/B%C3%BCt%C3%A7e.xlsx',
        '\\\\nas\\pay\\Bütçe Özeti.xlsx',
        'https://example.com/x.docx',
      ]);
      assert.deepEqual(got.files, [
        'C:/Bel geler/Bütçe.xlsx',
        '\\\\nas\\pay\\Bütçe Özeti.xlsx',
      ]);
    },
  },

  // ── local path → synced twin ───────────────────────────────────────
  {
    group: 'twin',
    name: 'the deepest matching pair wins, and a bare-storage remote joins without a double slash',
    async run(impl) {
      const pairs: SyncPairView[] = [
        { id: 'shallow', local: 'C:\\mirror\\docs', remote: 'docs://' },
        { id: 'deep', local: 'C:\\mirror\\docs\\reports', remote: 'docs://reports' },
      ];
      const got = impl.resolveSyncTwin('C:\\mirror\\docs\\reports\\q3.docx', pairs, { platform: 'win32' });
      assert.deepEqual(got, { pairId: 'deep', remote: 'docs://reports/q3.docx' });

      const shallowOnly = impl.resolveSyncTwin('C:\\mirror\\docs\\note.docx', [pairs[0]!], { platform: 'win32' });
      assert.deepEqual(shallowOnly, { pairId: 'shallow', remote: 'docs://note.docx' });
    },
  },
  {
    group: 'twin',
    name: 'a PAUSED pair is not a twin — saving there would never come back down',
    async run(impl) {
      const got = impl.resolveSyncTwin(
        'C:\\mirror\\docs\\q3.docx',
        [{ id: 'p', local: 'C:\\mirror\\docs', remote: 'docs://', paused: true }],
        { platform: 'win32' },
      );
      assert.equal(got, null);
    },
  },
  {
    group: 'twin',
    name: 'Windows compares paths case-insensitively, but the wire path keeps its case',
    async run(impl) {
      const got = impl.resolveSyncTwin(
        'C:\\Mirror\\Docs\\Reports\\Q3 Özet.docx',
        [{ id: 'p', local: 'c:\\mirror\\docs', remote: 'docs://' }],
        { platform: 'win32' },
      );
      assert.deepEqual(got, { pairId: 'p', remote: 'docs://Reports/Q3 Özet.docx' });
    },
  },
  {
    group: 'twin',
    name: 'a sibling folder whose name merely starts the same is NOT inside the pair',
    async run(impl) {
      const got = impl.resolveSyncTwin(
        '/home/ada/docsarchive/a.docx',
        [{ id: 'p', local: '/home/ada/docs', remote: 'docs://' }],
        { platform: 'linux' },
      );
      assert.equal(got, null);
    },
  },

  // ── scratch naming ─────────────────────────────────────────────────
  {
    group: 'scratch-name',
    name: 'the session id goes first, the extension stays last, and Turkish characters survive',
    async run(impl) {
      assert.equal(impl.scratchBasename('C:\\Belgeler\\Bütçe Özeti.xlsx', 'a1b2c3d4e5f6'), 'a1b2c3d4e5f6-Bütçe Özeti.xlsx');
      assert.equal(impl.scratchBasename('/home/ada/Rapor.DOCX', 'abc123'), 'abc123-Rapor.docx');
    },
  },
  {
    group: 'scratch-name',
    name: 'characters no path segment may carry are replaced, and an empty stem still yields a name',
    async run(impl) {
      assert.equal(impl.scratchBasename('C:\\x\\b:c*d?e.docx', 'aa'), 'aa-b_c_d_e.docx');
      assert.equal(impl.scratchBasename('C:\\x\\  ..  .docx', 'aa'), 'aa-document.docx');
    },
  },
  {
    group: 'scratch-name',
    name: 'a name long enough to break a filesystem is capped, extension intact',
    async run(impl) {
      const long = 'Ö'.repeat(400) + '.docx';
      const got = impl.scratchBasename('/tmp/' + long, 'aa');
      assert.ok(got.endsWith('.docx'), got.slice(-20));
      assert.ok(got.length <= 80, 'name is ' + got.length + ' characters');
    },
  },

  // ── write-back ─────────────────────────────────────────────────────
  {
    group: 'write-back',
    name: 'the replace goes through a temp file in the SAME directory and never writes over the document',
    async run(impl, dir) {
      const target = path.join(dir, 'Bütçe.docx');
      await fs.promises.writeFile(target, 'OLD');

      const wrote: string[] = [];
      const renamed: Array<[string, string]> = [];
      const realWrite = fs.promises.writeFile;
      const realRename = fs.promises.rename;
      // @ts-expect-error — deliberately swapping the module's own function
      fs.promises.writeFile = async (p: string, data: unknown) => {
        wrote.push(String(p));
        return realWrite(p as never, data as never);
      };
      // @ts-expect-error — same
      fs.promises.rename = async (from: string, to: string) => {
        renamed.push([String(from), String(to)]);
        return realRename(from as never, to as never);
      };
      try {
        await impl.writeBackAtomic(target, Buffer.from('NEW'));
      } finally {
        fs.promises.writeFile = realWrite;
        fs.promises.rename = realRename;
      }

      assert.equal(await fs.promises.readFile(target, 'utf8'), 'NEW');
      assert.ok(
        !wrote.includes(target),
        'the document itself was written to directly — a crash mid-write truncates the only copy',
      );
      const landing = renamed.find(([, to]) => to === target);
      assert.ok(landing, 'nothing was renamed onto the document');
      assert.equal(
        path.dirname(landing![0]),
        path.dirname(target),
        'the temp file was outside the target directory — that rename is EXDEV across drives',
      );
      assert.deepEqual(await fs.promises.readdir(dir), ['Bütçe.docx'], 'leftovers in the directory');
    },
  },
  {
    group: 'write-back',
    name: 'a document deleted while it was open is NOT resurrected — the edit lands beside it',
    async run(impl, dir) {
      const target = path.join(dir, 'gone.docx');
      let err: unknown = null;
      try {
        await impl.writeBackAtomic(target, Buffer.from('NEW'), { now: new Date('2026-09-04T10:15:00Z') });
      } catch (e) {
        err = e;
      }
      assert.ok(err, 'writing to a document that no longer exists reported success');
      assert.equal(fs.existsSync(target), false, 'a deleted document was recreated behind the user');
      const kept = (err as { keptAt?: string }).keptAt;
      assert.ok(kept, 'the edit was thrown away instead of being kept somewhere');
      assert.equal(await fs.promises.readFile(kept!, 'utf8'), 'NEW');
    },
  },
  {
    group: 'write-back',
    name: 'a rename the OS refuses (a locked document) keeps the edit and leaves the original alone',
    async run(impl, dir) {
      const target = path.join(dir, 'locked.docx');
      await fs.promises.writeFile(target, 'OLD');
      const realRename = fs.promises.rename;
      let calls = 0;
      // @ts-expect-error — deliberately swapping the module's own function
      fs.promises.rename = async (from: string, to: string) => {
        // Only the landing rename fails; the recovery rename must still work,
        // or the test would measure the wrong thing.
        if (++calls === 1) throw Object.assign(new Error('EPERM'), { code: 'EPERM' });
        return realRename(from as never, to as never);
      };
      let err: unknown = null;
      try {
        await impl.writeBackAtomic(target, Buffer.from('NEW'), { now: new Date('2026-09-04T10:15:00Z') });
      } catch (e) {
        err = e;
      } finally {
        fs.promises.rename = realRename;
      }
      assert.ok(err, 'a failed replace was reported as a success — the user believes they saved');
      assert.equal(await fs.promises.readFile(target, 'utf8'), 'OLD', 'the original was damaged');
      const kept = (err as { keptAt?: string }).keptAt;
      assert.ok(kept, 'the edit was not kept anywhere');
      assert.equal(await fs.promises.readFile(kept!, 'utf8'), 'NEW');
    },
  },

  // ── sweeps ─────────────────────────────────────────────────────────
  {
    group: 'sweep',
    name: 'a session this process just opened is left alone; another run\'s and an ancient one are not',
    async run(impl) {
      const now = Date.parse('2026-09-04T12:00:00Z');
      const mine = session({ id: 'mine', ownerPid: 4242, createdAt: '2026-09-04T11:55:00Z' });
      const theirs = session({ id: 'theirs', ownerPid: 9999, createdAt: '2026-09-04T11:55:00Z' });
      const ancient = session({ id: 'ancient', ownerPid: 4242, createdAt: '2026-08-30T09:00:00Z' });
      const got = impl.staleSessions([mine, theirs, ancient], { currentPid: 4242, now });
      assert.deepEqual(
        got.map((s) => s.id).sort(),
        ['ancient', 'theirs'],
        'a document being edited right now would have had its working copy deleted',
      );
    },
  },
  {
    group: 'sweep',
    name: 'only an OLD copy with no session record is an orphan; a fresh one, or one without an mtime, is not',
    async run(impl) {
      const now = Date.parse('2026-09-04T12:00:00Z');
      const day = 24 * 60 * 60 * 1000;
      const got = impl.orphanScratchEntries(
        [
          { basename: 'known.docx', lastModified: now - 30 * day },
          { basename: 'old.docx', lastModified: now - 30 * day },
          { basename: 'fresh.docx', lastModified: now - 60 * 1000 },
          { basename: 'undated.docx' },
        ],
        new Set(['known.docx']),
        { now },
      );
      assert.deepEqual(got, ['old.docx']);
    },
  },
];
