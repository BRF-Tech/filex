/**
 * Shared pieces of the realenv specs (e2e/realenv/README.md): who is running,
 * an admin API session, the DNS server the ACME authority asks, TLS
 * handshakes read the way a client reads them.
 */
import { expect, type APIRequest, type APIRequestContext } from '@playwright/test';
import fs from 'node:fs';
import https from 'node:https';
import tls from 'node:tls';
import { X509Certificate } from 'node:crypto';

export const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL ?? 'admin@local';
export const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD ?? 'admin';

/** Why a spec of this stage cannot run here, or '' when it can. */
export function missing(flag: string, stage: string, needs: string): string {
  if (process.env[flag]) return '';
  return `the realenv "${stage}" stage is not running (needs ${needs}): run e2e/realenv/run.sh ${stage}`;
}

export function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set: run this through e2e/realenv/run.sh`);
  return v;
}

/** An API session as the bootstrap administrator (a bearer token). */
export async function adminApi(request: APIRequest, baseURL: string): Promise<APIRequestContext> {
  const anon = await request.newContext({ baseURL });
  const login = await anon.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  expect(login.ok(), `admin sign-in on ${baseURL}: ${login.status()} ${await login.text()}`).toBe(true);
  const { token } = (await login.json()) as { token: string };
  await anon.dispose();
  return request.newContext({ baseURL, extraHTTPHeaders: { Authorization: `Bearer ${token}` } });
}

/** JSON of a response that must be a success, with the body in the message. */
export async function okJSON<T = any>(res: { ok(): boolean; status(): number; text(): Promise<string> }, what: string): Promise<T> {
  const text = await res.text();
  expect(res.ok(), `${what}: HTTP ${res.status()} ${text.slice(0, 600)}`).toBe(true);
  return (text ? JSON.parse(text) : {}) as T;
}

// ── pebble-challtestsrv: the DNS the ACME authority (and filex) ask ──

async function dnsCall(path: string, body: unknown): Promise<void> {
  const res = await fetch(`${env('REALENV_DNS_MGMT')}${path}`, { method: 'POST', body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`challtestsrv ${path}: HTTP ${res.status} ${await res.text()}`);
}

export const dns = {
  addA: (host: string, ip: string) => dnsCall('/add-a', { host, addresses: [ip] }),
  clearA: (host: string) => dnsCall('/clear-a', { host }),
  setCNAME: (host: string, target: string) => dnsCall('/set-cname', { host, target }),
  clearCNAME: (host: string) => dnsCall('/clear-cname', { host }),
};

// ── TLS, as a client sees it ──

export interface Handshake {
  ok: boolean;
  error: string;
  /** The leaf the server presented. */
  leaf?: X509Certificate;
  /** Every certificate the server sent, leaf first. */
  chain: X509Certificate[];
  alpn: string | false | null;
}

/** One TLS handshake to ip:port asking for servername. Never verifies: the
 *  caller judges the chain (verifyChain). */
export function handshake(ip: string, port: number, servername: string): Promise<Handshake> {
  return new Promise((resolve) => {
    const sock = tls.connect({ host: ip, port, servername, rejectUnauthorized: false, ALPNProtocols: ['http/1.1'] });
    const done = (h: Handshake) => {
      sock.destroy();
      resolve(h);
    };
    sock.setTimeout(20_000, () => done({ ok: false, error: 'timeout', chain: [], alpn: false }));
    sock.once('error', (err) => done({ ok: false, error: err.message, chain: [], alpn: false }));
    sock.once('secureConnect', () => {
      const chain: X509Certificate[] = [];
      let c: any = sock.getPeerCertificate(true);
      const seen = new Set<string>();
      while (c && c.raw && !seen.has(c.fingerprint256)) {
        seen.add(c.fingerprint256);
        chain.push(new X509Certificate(c.raw));
        c = c.issuerCertificate;
      }
      done({ ok: true, error: '', leaf: chain[0], chain, alpn: sock.alpnProtocol });
    });
  });
}

/** Whether a presented chain leads, signature by signature, to the given root
 *  and the leaf names the host and is valid now. */
export function verifyChain(h: Handshake, root: X509Certificate, host: string): string {
  if (!h.leaf) return 'no certificate';
  if (!h.leaf.checkHost(host)) return `the certificate does not name ${host} (${h.leaf.subjectAltName})`;
  const now = Date.now();
  if (now < Date.parse(h.leaf.validFrom) || now > Date.parse(h.leaf.validTo)) return 'the certificate is not valid now';
  const chain = [...h.chain];
  for (let i = 0; i < chain.length; i++) {
    const cert = chain[i]!;
    if (cert.checkIssued(root) && cert.verify(root.publicKey)) return '';
    const next = chain[i + 1];
    if (!next || !cert.checkIssued(next) || !cert.verify(next.publicKey)) {
      return `the chain breaks after ${cert.subject.replace(/\n/g, ', ')}`;
    }
  }
  return 'the chain does not end at the expected root';
}

/** A Pebble's issuing root (it makes a new one each start), fetched from
 *  its management interface, which presents Pebble's fixed test certificate. */
export function pebbleRoot(mgmt: string): Promise<X509Certificate> {
  const ca = fs.readFileSync('/work/pebble.minica.pem');
  const u = new URL(`${mgmt}/roots/0`);
  return new Promise((resolve, reject) => {
    https
      .get({ host: u.hostname, port: u.port, path: u.pathname, ca, servername: 'pebble' }, (res) => {
        let body = '';
        res.on('data', (d) => (body += d));
        res.on('end', () => {
          if (res.statusCode !== 200) return reject(new Error(`${u}: HTTP ${res.statusCode} ${body}`));
          resolve(new X509Certificate(body));
        });
      })
      .on('error', reject);
  });
}

export function readLog(file: string): string {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return '';
  }
}

/** The lines of a log that mention every one of the needles. */
export function logLines(file: string, ...needles: string[]): string[] {
  return readLog(file)
    .split('\n')
    .filter((l) => needles.every((n) => l.includes(n)));
}

export async function until<T>(what: string, fn: () => Promise<T | undefined | false>, timeoutMs = 60_000, everyMs = 1000): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let last: unknown;
  for (;;) {
    try {
      const v = await fn();
      if (v) return v as T;
    } catch (err) {
      last = err;
    }
    if (Date.now() > deadline) throw new Error(`${what}: not within ${timeoutMs} ms${last ? ` (${String(last)})` : ''}`);
    await new Promise((r) => setTimeout(r, everyMs));
  }
}
