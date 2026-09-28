// Drafts (issue #71) — the client half every caller shares (lib/drafts) and
// the one question an editor asks of a path: is this a draft, and which?
//
// A draft is found by its PATH: the explorer, the standalone editor tab and
// the desktop's document windows all hand the viewer a path and nothing else,
// so `draftKeyOf` has to agree with the server's shape (syspath.IsDraftOf)
// exactly — too loose and an ordinary file grows a Save bar, too strict and a
// draft opens as a plain file with nowhere to go.
import { describe, expect, it, vi } from 'vitest';
import { draftKeyOf, INTERNAL_DIR_NAMES } from '@brftech/filex-core/src/lib/internalPaths';
import {
  draftFolderLabel,
  draftLimitOf,
  draftsClient,
  isDraftFolderGone,
  isDraftLimit,
} from '@brftech/filex-core/src/lib/drafts';

describe('draftKeyOf', () => {
  it('names the key of a draft file, in the wire form every host carries', () => {
    expect(draftKeyOf('docs://.filex-drafts/7/0123456789abcdef/notes.txt')).toBe('0123456789abcdef');
    expect(draftKeyOf('docs://.filex-drafts/12/abcdef0123456789/LICENSE')).toBe('abcdef0123456789');
  });

  it.each([
    ['docs://Documents/notes.txt'],
    ['docs://.filex-drafts'],
    ['docs://.filex-drafts/7'],
    ['docs://.filex-drafts/7/0123456789abcdef'], // the draft's folder, not a draft
    ['docs://.filex-drafts/7/0123456789abcdef/a/b.txt'], // deeper than a draft
    ['docs://.filex-drafts/abc/0123456789abcdef/notes.txt'], // not an id
    ['docs://.filex-drafts/007/0123456789abcdef/notes.txt'],
    ['docs://.filex-drafts/7/NOT-A-KEY/notes.txt'],
    ['docs://.filex-drafts/7/0123456789abcdef/.keepdir'],
    ['docs://Documents/.filex-drafts/7/0123456789abcdef/notes.txt'], // not at the root
  ])('is not a draft: %s', (path) => {
    expect(draftKeyOf(path)).toBe('');
  });

  it('the drafts area is one of filex’s own names', () => {
    expect(INTERNAL_DIR_NAMES).toContain('.filex-drafts');
  });
});

function transport(answers: Array<{ status: number; body: unknown }>) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const request = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    const a = answers.shift() ?? { status: 500, body: {} };
    return new Response(JSON.stringify(a.body), { status: a.status });
  });
  return { calls, request };
}

describe('draftsClient.save', () => {
  it('answers where the file went', async () => {
    const t = transport([{ status: 200, body: { ok: true, path: 'docs://Documents/notes.txt', name: 'notes.txt', target_dir: 'docs://Documents' } }]);
    const out = await draftsClient('/api/files/drafts', { request: t.request }).save('0123456789abcdef');
    expect(out).toEqual({ saved: true, path: 'docs://Documents/notes.txt', name: 'notes.txt', targetDir: 'docs://Documents' });
    expect(t.calls[0].url).toBe('/api/files/drafts/0123456789abcdef/save');
    expect(t.calls[0].init?.method).toBe('POST');
    expect(t.calls[0].init?.body).toBe('{}');
  });

  it('a taken name is a QUESTION, not a failure — with the free name the server offers', async () => {
    const t = transport([
      {
        status: 409,
        body: { code: 'TARGET_TAKEN', name: 'notes.txt', suggested: 'notes (2).txt', target_dir: 'docs://Documents' },
      },
    ]);
    const out = await draftsClient('/api/files/drafts/', { request: t.request }).save('0123456789abcdef');
    expect(out).toEqual({
      saved: false,
      taken: true,
      name: 'notes.txt',
      suggested: 'notes (2).txt',
      targetDir: 'docs://Documents',
    });
  });

  it('saves under exactly the name the person agreed to', async () => {
    const t = transport([{ status: 200, body: { path: 'docs://Documents/notes (2).txt', name: 'notes (2).txt' } }]);
    await draftsClient('/api/files/drafts', { request: t.request }).save('0123456789abcdef', 'notes (2).txt');
    expect(JSON.parse(String(t.calls[0].init?.body))).toEqual({ as: 'notes (2).txt' });
  });

  it('any other refusal throws, and says which it is', async () => {
    const t = transport([{ status: 409, body: { code: 'FOLDER_GONE', error: 'gone' } }]);
    const err = await draftsClient('/api/files/drafts', { request: t.request, locale: 'en' })
      .save('0123456789abcdef')
      .catch((e) => e);
    expect(err).toBeInstanceOf(Error);
    expect(isDraftFolderGone(err)).toBe(true);
    // Said in words, never the body.
    expect(String((err as Error).message)).not.toContain('{');
  });
});

describe('the draft limit', () => {
  it('is recognised by its code, with the number it names', async () => {
    const t = transport([{ status: 409, body: { code: 'DRAFT_LIMIT', count: 50, error: 'no', limit: 50 } }]);
    const err = await draftsClient('/api/files/drafts', { request: t.request, locale: 'en' })
      .create('docs://', 'a.txt', 'txt', { exactName: true })
      .catch((e) => e);
    expect(isDraftLimit(err)).toBe(true);
    expect(draftLimitOf(err)).toBe(50);
    expect((err as Error).message).toContain('50');
  });

  it('an ordinary conflict is not the limit', async () => {
    const t = transport([{ status: 409, body: { error: 'x', code: 'NAME_TAKEN' } }]);
    const err = await draftsClient('/api/files/drafts', { request: t.request })
      .create('docs://', 'a.txt', 'txt')
      .catch((e) => e);
    expect(isDraftLimit(err)).toBe(false);
  });
});

describe('draftFolderLabel', () => {
  it('reads the folder with its storage first', () => {
    expect(draftFolderLabel('docs://Reports/2026')).toBe('docs / Reports / 2026');
    expect(draftFolderLabel('docs://')).toBe('docs');
  });
});
