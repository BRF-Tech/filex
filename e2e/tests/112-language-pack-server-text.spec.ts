/**
 * 112-language-pack-server-text — what the SERVER writes, in a pack's language.
 *
 * 114 walks a pack through every screen. This walks it through the text the
 * server composes, which knew English and Turkish only until v0.43.0: with a
 * Spanish pack installed and a Spanish account, a share-link e-mail arrived
 * as "informe.txt dosyası sizinle paylaşıldı" — TURKISH, the `else` of an
 * en/tr pair — the drop notice to the owner too, the bell said "1 file
 * received", and a Spanish browser's drop page said "Send files" (measured
 * 2026-09-22 before the server catalogue).
 *
 * The mails are read off a real SMTP conversation: this file runs a tiny SMTP
 * server, points filex at it, and restores the settings afterwards.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import net from 'node:net';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-srvtext-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PACK_NAME = 'lang-es-srvtext-e2e';

/** A PARTIAL pack on purpose: the keys it lacks must arrive in English. */
const ES: Record<string, string> = {
  'nav.notifications': 'Notificaciones',
  'server.mail.greeting': 'Hola:',
  'server.mail.share.subject_file': '{name} se ha compartido contigo',
  'server.mail.label.file': 'Archivo: {name}',
  'server.mail.share.download': 'Descárgalo aquí:',
  'server.mail.valid_days': 'Este enlace es válido durante {count} días.',
  'server.mail.drop_received.subject': 'Nueva subida de archivos',
  'server.mail.drop_received.body': '{who} dejó {count} archivos en «{folder}» ({submission}).',
  'server.mail.drop_received.body_one': '{who} dejó un archivo en «{folder}» ({submission}).',
  'server.notify.word.someone': 'Alguien',
  'server.notify.drop.received.title': '{count} archivos recibidos',
  'server.notify.drop.received.title_one': 'Un archivo recibido',
  'server.public.drop_title': 'Enviar archivos',
  'server.public.drop_heading': 'Enviar archivos',
  'server.public.footer': 'Compartido con {filex}',
};

/* ── a tiny SMTP server: keeps every message it is handed ─────────────── */

interface Sink {
  port: number;
  mails: string[];
  close: () => Promise<void>;
}

function startSink(): Promise<Sink> {
  const mails: string[] = [];
  const server = net.createServer((c) => {
    let buf = '';
    let inData = false;
    let body: string[] = [];
    const say = (l: string) => c.write(`${l}\r\n`);
    say('220 e2e-sink ESMTP');
    c.on('data', (d) => {
      buf += d.toString('utf8');
      let i: number;
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i).replace(/\r$/, '');
        buf = buf.slice(i + 1);
        if (inData) {
          if (line === '.') {
            inData = false;
            mails.push(body.join('\n'));
            body = [];
            say('250 Ok');
          } else body.push(line);
          continue;
        }
        const verb = line.split(' ')[0].toUpperCase();
        if (verb === 'EHLO' || verb === 'HELO') {
          say('250-e2e-sink');
          say('250 8BITMIME');
        } else if (verb === 'DATA') {
          inData = true;
          say('354 go');
        } else if (verb === 'QUIT') {
          say('221 bye');
          c.end();
        } else say('250 Ok');
      }
    });
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const port = (server.address() as net.AddressInfo).port;
      resolve({ port, mails, close: () => new Promise((r) => server.close(() => r())) });
    });
  });
}

/** Headers (RFC 2047 decoded) and body of one received message. */
function parse(raw: string): { h: Record<string, string>; body: string } {
  const [head, ...rest] = raw.split('\n\n');
  const h: Record<string, string> = {};
  for (const line of head.split('\n')) {
    const i = line.indexOf(': ');
    if (i < 0) continue;
    h[line.slice(0, i)] = line
      .slice(i + 2)
      .replace(/=\?utf-8\?b\?([^?]+)\?=\s*/gi, (_m, b64: string) => Buffer.from(b64, 'base64').toString('utf8'))
      .trim();
  }
  return { h, body: rest.join('\n\n') };
}

async function waitMail(sink: Sink, n: number) {
  await expect.poll(() => sink.mails.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(n);
  return parse(sink.mails[n - 1]);
}

const SMTP_KEYS = ['smtp.host', 'smtp.port', 'smtp.from', 'smtp.tls', 'smtp.username'];
const PREFS = '/api/me/prefs?surface=web';

test.describe.serial('Language pack — the text the server writes', () => {
  let api: APIRequestContext;
  let sink: Sink;
  let smtpBefore: Record<string, string> = {};
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    sink = await startSink();
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const settings = (await (await api.get('/api/admin/settings')).json()) as Record<string, string>;
    for (const k of SMTP_KEYS) smtpBefore[k] = typeof settings[k] === 'string' ? settings[k] : '';
    const prefs = await api.get(PREFS);
    prefsBefore = prefs.ok() ? ((await prefs.json()).prefs ?? {}) : {};

    expect((await api.patch('/api/admin/settings', {
      data: { 'smtp.host': '127.0.0.1', 'smtp.port': String(sink.port), 'smtp.from': 'filex@e2e.test', 'smtp.tls': 'none', 'smtp.username': '' },
    })).ok()).toBeTruthy();
    const verified = await (await api.post('/api/admin/settings/smtp-test', { data: {} })).json();
    expect(verified.ok, JSON.stringify(verified)).toBe(true);

    const list = await (await api.get('/api/admin/app-plugins')).json();
    for (const p of list.plugins ?? []) if (p.name === PACK_NAME) await api.delete(`/api/admin/app-plugins/${p.id}`);
    const manifest = {
      manifest_version: 1, name: PACK_NAME, version: '1.0.0', label: { en: 'Spanish (server text e2e)' },
      permissions: [], ui_locales: { es: ES },
    };
    const inst = await api.post('/api/admin/app-plugins', {
      multipart: { manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(manifest)) } },
    });
    expect(inst.ok(), await inst.text()).toBeTruthy();

    expect((await api.patch('/api/auth/profile', { data: { locale: 'es' } })).ok()).toBeTruthy();
    await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'es' } } });

    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: 'informe.txt', mimeType: 'text/plain', buffer: Buffer.from('hola') } },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: 'buzon' } });
    expect(mk.ok(), await mk.text()).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    const list = await (await api.get('/api/admin/app-plugins')).json();
    for (const p of list.plugins ?? []) if (p.name === PACK_NAME) await api.delete(`/api/admin/app-plugins/${p.id}`);
    // ⚠ Put the mail settings back EXACTLY and re-verify, so no later spec
    // finds a "Send by e-mail" pointed at a sink that is gone.
    await api.patch('/api/admin/settings', { data: smtpBefore });
    await api.post('/api/admin/settings/smtp-test', { data: {} });
    await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api.patch('/api/auth/profile', { data: { locale: 'en' } }).catch(() => undefined);
    await dropStorageByName(request, STORAGE);
    await api.dispose();
    await sink.close();
  });

  test('a share-link mail arrives in the pack language, English where the pack is silent', async () => {
    const made = await api.post('/api/files/share', { data: { path: `${STORAGE}://informe.txt` } });
    expect(made.ok()).toBeTruthy();
    const url = (await made.json()).share.url;
    const before = sink.mails.length;
    const sent = await api.post('/api/files/permissions/share-mail', {
      data: { path: `${STORAGE}://informe.txt`, email: 'amigo@e2e.test', url, locale: 'es', is_dir: false, size: 4, expires_days: 7 },
    });
    expect(sent.ok(), await sent.text()).toBeTruthy();
    const { h, body } = await waitMail(sink, before + 1);
    expect(h.Subject).toBe('informe.txt se ha compartido contigo');
    expect(h['Content-Language']).toBe('es');
    expect(body).toContain('Hola:');
    expect(body).toContain('Archivo: informe.txt');
    expect(body).toContain(`Descárgalo aquí:\n${url}`);
    expect(body).toContain('Este enlace es válido durante 7 días.');
    expect(body).toContain('A file has been shared with you:'); // not in the pack
    expect(body).not.toMatch(/paylaşıldı|Merhaba/);
  });

  test('a drop tells its Spanish owner in Spanish — by mail and in the bell', async ({ page, request }) => {
    const drop = await api.post('/api/files/share', { data: { path: `${STORAGE}://buzon`, kind: 'drop' } });
    expect(drop.ok(), await drop.text()).toBeTruthy();
    const token = (await drop.json()).share.token;
    const before = sink.mails.length;
    const up = await request.post(`/d/${token}`, {
      multipart: { 'file[]': { name: 'factura.txt', mimeType: 'text/plain', buffer: Buffer.from('x') } },
    });
    expect(up.ok(), await up.text()).toBeTruthy();
    const { h, body } = await waitMail(sink, before + 1);
    expect(h.Subject).toBe('Nueva subida de archivos');
    expect(body).toContain('Alguien dejó un archivo en «buzon»');

    // The bell's words are the READER's: the Spanish phrase, its singular.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/notifications');
    await expect(page.getByText('Un archivo recibido').first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('1 file received')).toHaveCount(0);

    // The no-JavaScript drop page a Spanish browser gets.
    const html = await (await request.get(`/d/${token}`, { headers: { Accept: '*/*', 'Accept-Language': 'es-ES,es;q=0.9' } })).text();
    // `dir` follows `lang` on every server-rendered page (feat/043-rtl).
    expect(html).toContain('<html lang="es" dir="ltr">');
    expect(html).toContain('Enviar archivos');
    expect(html).toContain('Drop your files here'); // not in the pack: English
    expect(html).toContain('Compartido con <a href="https://filex.sh"');
  });
});
