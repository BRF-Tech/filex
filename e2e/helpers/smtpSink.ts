/**
 * A tiny SMTP server that keeps every message it is handed — for the specs
 * that read a mail the way its recipient gets it — and the settings dance
 * that points filex at it and puts the operator's mail settings back.
 *
 * ⚠ Extracted from 112-language-pack-server-text when 216-share-mail-server
 * needed the same sink (0.54): one sink, so two specs never disagree about
 * what "the mail that arrived" is.
 */
import { expect, type APIRequestContext } from '@playwright/test';
import net from 'node:net';

export interface Sink {
  port: number;
  mails: string[];
  close: () => Promise<void>;
}

export function startSink(): Promise<Sink> {
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
export function parseMail(raw: string): { h: Record<string, string>; body: string } {
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

/** The n-th message (1-based) once it has arrived. */
export async function waitMail(sink: Sink, n: number) {
  await expect.poll(() => sink.mails.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(n);
  return parseMail(sink.mails[n - 1]);
}

const SMTP_KEYS = ['smtp.host', 'smtp.port', 'smtp.from', 'smtp.tls', 'smtp.username'];

/**
 * Points filex's mail at the sink and verifies it; answers what to call in
 * `afterAll` to put the settings back EXACTLY and re-verify, so no later spec
 * finds a "Send by e-mail" pointed at a sink that is gone.
 */
export async function mailToSink(api: APIRequestContext, sink: Sink): Promise<() => Promise<void>> {
  const settings = (await (await api.get('/api/admin/settings')).json()) as Record<string, string>;
  const before: Record<string, string> = {};
  for (const k of SMTP_KEYS) before[k] = typeof settings[k] === 'string' ? settings[k] : '';
  expect((await api.patch('/api/admin/settings', {
    data: { 'smtp.host': '127.0.0.1', 'smtp.port': String(sink.port), 'smtp.from': 'filex@e2e.test', 'smtp.tls': 'none', 'smtp.username': '' },
  })).ok()).toBeTruthy();
  const verified = await (await api.post('/api/admin/settings/smtp-test', { data: {} })).json();
  expect(verified.ok, JSON.stringify(verified)).toBe(true);
  return async () => {
    await api.patch('/api/admin/settings', { data: before });
    await api.post('/api/admin/settings/smtp-test', { data: {} });
  };
}
