// The generated "how to connect" instructions.
//
// ⚠ Same reasoning as shareCli.test.ts: the builder lives in
// @brftech/filex-core (the desktop app, the admin panel and the explorer
// overlay all render it, and the point of putting it there is that they
// cannot drift), the core package has no test runner of its own, and this
// is a pure function — so it is exercised here, in the app that ships it.
//
// A wrong command here is worse than no command: it is pasted into a
// terminal or a Windows address bar on someone else's machine, where the
// failure is invisible to us and looks like a filex bug.
import { describe, it, expect } from 'vitest';
import {
  buildGuide,
  buildWebdavGuide,
  guideProtocols,
  hostOf,
  isPlainHttp,
  type GuideContext,
} from '@brftech/filex-core/src/lib/connectionGuides';

/** Identity translator: the assertions below are about the GENERATED parts
 *  (host, storage, username), not about the prose, so the key is enough. */
const t = (key: string, vars: Record<string, string | number> = {}) =>
  Object.entries(vars).reduce((acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)), key);

const ctx: GuideContext = {
  origin: 'https://files.example.com',
  user: 'ada@example.com',
  storages: ['depo', 'photos'],
};

const codeOf = (guide: ReturnType<typeof buildWebdavGuide>, clientId: string) =>
  (guide.clients.find((c) => c.id === clientId)?.blocks ?? [])
    .map((b) => `${b.code ?? ''}\n${(b.steps ?? []).join('\n')}\n${b.text ?? ''}`)
    .join('\n');

describe('buildWebdavGuide', () => {
  it('names the real deployment and the real account', () => {
    const g = buildWebdavGuide(ctx, t);
    const address = g.facts[0];
    expect(address.value).toBe('https://files.example.com/dav/');
    expect(g.facts[1].value).toBe('ada@example.com');
    // The password is never a value we hold — only a hint.
    expect(g.facts[2].placeholderOnly).toBe(true);
  });

  it('scopes the address to one storage when one is chosen', () => {
    const g = buildWebdavGuide({ ...ctx, storage: 'depo' }, t);
    expect(g.facts[0].value).toBe('https://files.example.com/dav/depo/');
    expect(codeOf(g, 'windows')).toContain('net use Z: "https://files.example.com/dav/depo/"');
    expect(codeOf(g, 'rclone')).toContain('rclone lsl filex:depo');
  });

  it('carries the three Windows limits that look like filex bugs', () => {
    const win = codeOf(buildWebdavGuide(ctx, t), 'windows');
    // 4 GB, not the 50,000,000-byte default that stops transfers at ~47.7 MB.
    expect(win).toContain('FileSizeLimitInBytes /t REG_DWORD /d 4294967295');
    // ~1,000 entries per folder without this.
    expect(win).toContain('FileAttributesLimitInBytes');
    // The service must be bounced or neither takes effect.
    expect(win).toContain('net stop webclient && net start webclient');
    // ⚠ BasicAuthLevel is never SET — telling a user to put it to 2 is
    // telling them to send Basic credentials over cleartext.
    expect(win).not.toContain('BasicAuthLevel /t');
  });

  it('gives rclone a config it can actually load', () => {
    const rc = codeOf(buildWebdavGuide(ctx, t), 'rclone');
    expect(rc).toContain('type = webdav');
    expect(rc).toContain('url = https://files.example.com/dav');
    expect(rc).toContain('user = ada@example.com');
    // The password is obscured, never inlined: rclone rejects a plain one.
    expect(rc).toContain('rclone obscure');
  });

  it('turns the address into a davs:// URL for GNOME/KDE', () => {
    const linux = codeOf(buildWebdavGuide(ctx, t), 'linux');
    expect(linux).toContain('sudo mount -t davfs https://files.example.com/dav/');
    expect(linux).toContain('gio mount davs://files.example.com/dav/');
    // davfs2 hangs against in-memory locks; the guide must say so.
    expect(linux + JSON.stringify(buildWebdavGuide(ctx, t).clients)).toContain('use_locks 0');
  });

  it('warns when the deployment is on plain http', () => {
    const secure = buildWebdavGuide(ctx, t);
    const plain = buildWebdavGuide({ ...ctx, origin: 'http://192.168.1.10:5212' }, t);
    const warnKey = 'conn.guide.webdav.note.http';
    expect(secure.notes.some((n) => n.text === warnKey)).toBe(false);
    expect(plain.notes[0]).toEqual({ kind: 'warn', text: warnKey });
  });

  it('stays readable before /api/auth/me answers', () => {
    const g = buildWebdavGuide({ ...ctx, user: '' }, t);
    // A placeholder, never `undefined` in the middle of a command line.
    expect(g.facts[1].value).toBe('conn.guide.userPlaceholder');
    expect(codeOf(g, 'windows')).not.toContain('undefined');
  });
});

describe('the registry', () => {
  it('resolves a protocol to its builder, and is honest about the rest', () => {
    expect(guideProtocols()).toContain('webdav');
    expect(buildGuide('webdav', ctx, t)?.id).toBe('webdav');
    // S3 and SFTP arrive as a builder each — until then, null, not a
    // half-rendered page.
    expect(buildGuide('s3', ctx, t)).toBeNull();
  });
});

describe('helpers', () => {
  it('extracts a host and survives a malformed origin', () => {
    expect(hostOf('https://files.example.com')).toBe('files.example.com');
    expect(hostOf('files.example.com/')).toBe('files.example.com');
  });

  it('detects cleartext', () => {
    expect(isPlainHttp('http://x.local')).toBe(true);
    expect(isPlainHttp('https://x.local')).toBe(false);
  });
});
