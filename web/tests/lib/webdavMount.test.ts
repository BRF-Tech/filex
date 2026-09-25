// The WebDAV address forms and the OS commands built from them.
//
// ⚠ Same arrangement as connectionGuides.test.ts: the module lives in
// @brftech/filex-core, which has no test runner, so it is exercised here. It
// has TWO readers — the connection guide prints these commands, the desktop
// app's "Mount as a drive" runs them (desktop/test/drive.test.ts) — and a
// change here changes both at once, which is the point.
import { describe, it, expect } from 'vitest';
import {
  gioUri,
  isPlainHttp,
  netUseArgs,
  netUseCommand,
  netUseDeleteArgs,
  webdavUnc,
  webdavUrl,
  WEBCLIENT_LIMIT_COMMANDS,
} from '@brftech/filex-core/src/lib/webdavMount';
import { buildWebdavGuide } from '@brftech/filex-core/src/lib/connectionGuides';

describe('webdavUrl', () => {
  it('is the /dav root, or one storage under it', () => {
    expect(webdavUrl('https://fm.example.com')).toBe('https://fm.example.com/dav/');
    expect(webdavUrl('https://fm.example.com/', 'docs')).toBe('https://fm.example.com/dav/docs/');
  });

  it('leaves a storage name the way a person types it', () => {
    // net use and Explorer encode it on the wire themselves.
    expect(webdavUrl('https://fm.example.com', 'My files')).toBe('https://fm.example.com/dav/My files/');
  });
});

describe('gioUri', () => {
  it('turns https into davs and http into dav', () => {
    expect(gioUri('https://fm.example.com/dav/')).toBe('davs://fm.example.com/dav/');
    expect(gioUri('http://127.0.0.1:8080/dav/')).toBe('dav://127.0.0.1:8080/dav/');
  });

  it('puts the account in the URI, encoded, so gio asks for the password only', () => {
    expect(gioUri('https://fm.example.com/dav/docs/', 'ada@example.com')).toBe(
      'davs://ada%40example.com@fm.example.com/dav/docs/',
    );
  });

  it('percent-encodes a storage name with a space (a URI, unlike the typed form)', () => {
    expect(gioUri('https://fm.example.com/dav/My files/')).toBe('davs://fm.example.com/dav/My%20files/');
  });
});

describe('webdavUnc', () => {
  it('is what Windows lists for a mapped WebDAV drive', () => {
    // Measured on Windows 11 26200: `net use` showed \\127.0.0.1@5332\dav\docs.
    expect(webdavUnc('http://127.0.0.1:5332/dav/docs/')).toBe('\\\\127.0.0.1@5332\\dav\\docs');
    expect(webdavUnc('https://fm.example.com/dav/')).toBe('\\\\fm.example.com@SSL\\dav');
    expect(webdavUnc('https://fm.example.com:8443/dav/My files/')).toBe('\\\\fm.example.com@SSL@8443\\dav\\My files');
  });
});

describe('net use', () => {
  const o = { letter: 'Z:', url: 'https://fm.example.com/dav/My files/', user: 'ada@example.com', password: '*', persistent: false };

  it('argv keeps the address as ONE argument, whatever it contains', () => {
    expect(netUseArgs(o)).toEqual(['use', 'Z:', 'https://fm.example.com/dav/My files/', '/user:ada@example.com', '*', '/persistent:no']);
  });

  it('the pasteable line quotes the address', () => {
    expect(netUseCommand({ ...o, password: '<secret>', persistent: true })).toBe(
      'net use Z: "https://fm.example.com/dav/My files/" /user:ada@example.com <secret> /persistent:yes',
    );
  });

  it('detaching names only the letter', () => {
    expect(netUseDeleteArgs('Z:')).toEqual(['use', 'Z:', '/delete', '/y']);
  });
});

describe('one source for the guide and the button', () => {
  it('the guide prints the shared commands, not copies of them', () => {
    const g = buildWebdavGuide(
      { origin: 'https://fm.example.com', user: 'ada@example.com', storages: ['docs'], storage: 'docs' },
      (k) => k,
    );
    const win = g.clients.find((c) => c.id === 'windows')!.blocks.map((b) => b.code ?? '').join('\n');
    expect(win).toContain(
      netUseCommand({ letter: 'Z:', url: webdavUrl('https://fm.example.com', 'docs'), user: 'ada@example.com', password: 'conn.guide.secretPlaceholder', persistent: true }),
    );
    for (const line of WEBCLIENT_LIMIT_COMMANDS) expect(win).toContain(line);
    const linux = g.clients.find((c) => c.id === 'linux')!.blocks.map((b) => b.code ?? '').join('\n');
    expect(linux).toContain(`gio mount ${gioUri(webdavUrl('https://fm.example.com', 'docs'))}`);
  });

  it('isPlainHttp is still reachable from the guide module', () => {
    expect(isPlainHttp('http://x')).toBe(true);
    expect(isPlainHttp('https://x')).toBe(false);
  });
});
