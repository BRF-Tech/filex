// No screen composes a notification's sentence.
//
// ⚠⚠ The maintainers' decision (2026-10-08, task #191): "the browser sends a
// key and its values; the server builds the sentence and sends it where it
// goes". Every notification is said by the SERVER, in Go, in the reader's
// language (backend/internal/notify/say.go): `GET /api/notifications` answers
// each row with its words already said, and the same code path says them to a
// phone (Web Push), in an email and to a webhook. Until then the sentence was
// composed by every reader's screen from a phrase table in
// packages/core/src/lib/notificationText.ts, and a second time in Go for a
// push - a Turkish bell said "Yeni dosya: rapor.pdf" while the phone said
// "rapor.pdf - Rapor: 1 dosya eklendi".
//
// This holds the client side of that: no phrase table, no renderer, no copy of
// a `server.notify.*` phrase and no read of one, anywhere in the code a screen
// runs - the explorer, the admin panel, the desktop app, the service worker.
// The one thing a screen still does is name an item inside an encrypted folder
// it has unlocked, in the place the server marked (core lib/notificationText
// `notificationText`): it builds no words.
//
// ⚠ Red on the code before it: notificationText.ts exported
// NOTIFICATION_PHRASES and renderNotification, and the desktop main process
// and the bell composed with them.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { sourceFiles } from '../helpers/shownStrings';

const ROOT = path.resolve(__dirname, '../../..');
const rel = (f: string) => path.relative(ROOT, f).split(path.sep).join('/');

/** Every file a screen runs: the packages, the admin panel, the desktop app and the worker. */
function clientFiles(): string[] {
  return [
    ...sourceFiles(path.join(ROOT, 'packages/core/src'), /\.(vue|[cm]?ts|js)$/),
    ...sourceFiles(path.join(ROOT, 'web/src'), /\.(vue|[cm]?ts|js)$/),
    ...sourceFiles(path.join(ROOT, 'desktop/src'), /\.([cm]?ts|js)$/),
    ...sourceFiles(path.join(ROOT, 'web/public'), /\.js$/),
  ];
}

/** The server catalogue's notification phrases that carry words around a
 *  placeholder - what a screen would have to copy to compose a sentence. */
function serverPhrases(): string[] {
  const out: string[] = [];
  for (const lang of ['en', 'tr']) {
    const table = JSON.parse(
      fs.readFileSync(path.join(ROOT, 'backend/internal/srvtext/locales', `${lang}.json`), 'utf8'),
    ) as Record<string, string>;
    for (const [k, v] of Object.entries(table)) {
      if (!k.startsWith('server.notify.')) continue;
      // Words AND a placeholder: "New file: {name}", "{count} dosya geldi".
      if (/\{\w+\}/.test(v) && v.replace(/\{\w+\}/g, '').replace(/[\s\p{P}\p{S}]/gu, '').length >= 3) out.push(v);
    }
  }
  return out;
}

describe('no screen composes a notification', () => {
  const files = clientFiles();

  it('reads the code it guards (a scan of nothing passes everything)', () => {
    expect(files.length).toBeGreaterThan(300);
    expect(files.map(rel)).toContain('packages/core/src/lib/notificationText.ts');
    expect(files.map(rel)).toContain('desktop/src/notifications.ts');
    expect(serverPhrases().length).toBeGreaterThan(40);
  });

  it('has no phrase table and no renderer', () => {
    const gone = /\b(NOTIFICATION_PHRASES|renderNotification|notificationVars|fillTemplate|DIGEST_PARTS|notificationTextFor|notificationToast|NotifyLocale)\b/;
    const found: string[] = [];
    for (const f of files) {
      const src = fs.readFileSync(f, 'utf8');
      // useFileApi has a fillTemplate of its own for endpoint URLs - not words.
      const scan = rel(f) === 'packages/core/src/composables/useFileApi.ts' ? src.replace(/\bfillTemplate\b/g, '') : src;
      const m = scan.match(gone);
      if (m) found.push(`${rel(f)}: ${m[0]}`);
    }
    expect(found).toEqual([]);
  });

  it('neither reads a server.notify.* phrase nor keeps a copy of one', () => {
    const phrases = serverPhrases();
    const found: string[] = [];
    for (const f of files) {
      const src = fs.readFileSync(f, 'utf8');
      if (/['"`]server\.notify\./.test(src)) found.push(`${rel(f)}: reads a server.notify key`);
      for (const p of phrases) if (src.includes(p)) found.push(`${rel(f)}: copies "${p}"`);
    }
    expect(found).toEqual([]);
  });

  it('the one module a screen reads a row through only puts a name in a marked place', () => {
    const src = fs.readFileSync(path.join(ROOT, 'packages/core/src/lib/notificationText.ts'), 'utf8');
    const exported = [...src.matchAll(/^export (?:function|const|let|class) (\w+)/gm)].map((m) => m[1]);
    expect(exported).toEqual(['notificationText']);
    // No import at all: nothing to compose with.
    expect(src).not.toMatch(/^import /m);
  });
});
