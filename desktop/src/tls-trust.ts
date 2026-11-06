// Which servers this app reaches without checking their certificate (#298).
//
// A saved connection has its own switch (Account.insecureSkipTLSVerify), for a
// filex server with a self-signed or private certificate that cannot be trusted
// any other way. Trusting the certificate on the computer keeps the check on
// and is the better way: Chromium and the sync engine both read the system's
// store.
//
// ⚠⚠ Everything the app does on the network goes through one of two places, and
// the switch has to reach both:
//
//   - Chromium's stack (`net.fetch`, `net.request`, the renderer's own requests,
//     a download): session.setCertificateVerifyProc (main.ts) asks certVerdict.
//   - the sync engine, a child process: engineTlsEnv, next to FILEX_URL.
//
// ⚠ The verify callback is told a host name and no port, so the answer is per
// host: a second filex on another port of that machine goes unchecked too.
//
// ⚠⚠ Chromium keeps the answer for the life of the process. Measured on Electron
// 44 with a count of the callback's calls: after its first answer for a host and
// certificate it is not asked again, in either direction, and closeAllConnections,
// setting the callback again (or null first), setSSLConfig, clearCache,
// clearData, clearHostResolverCache and clearAuthCache change nothing. So a
// certificate let through stays let through after the switch goes off, until the
// app restarts: CertAnswers knows when that is the case, and Settings says so
// and offers the restart. The engine is a child process and hears at once.
//
// ⚠ No `electron` import, so node:test can drive it.

import type { DesktopState } from './account-state.js';
import type { Bilingual } from './strings.js';

/** cliclient.EnvInsecureSkipTLSVerify. */
export const ENV_SKIP_TLS_VERIFY = 'FILEX_INSECURE_SKIP_TLS_VERIFY';

/** What setCertificateVerifyProc's callback takes: accept the certificate, or
 *  stay with the answer Chromium came to by itself. */
export const CERT_ACCEPT = 0;
export const CERT_CHROMIUMS_ANSWER = -3;

/** What carries the switch: a saved account, or a sign-in under way. */
export interface TlsSwitch {
  serverUrl: string;
  insecureSkipTLSVerify?: boolean;
}

export function hostOf(serverUrl: string): string | null {
  try {
    return new URL(serverUrl).hostname.toLowerCase();
  } catch {
    return null;
  }
}

/** The hosts whose certificate is not checked now. `pending` is the sign-in in
 *  flight: its exchange happens before there is an account to carry the switch. */
export function skippedHosts(state: Pick<DesktopState, 'accounts'>, pending: TlsSwitch | null): Set<string> {
  const hosts = new Set<string>();
  for (const c of [...state.accounts, ...(pending ? [pending] : [])]) {
    const host = c.insecureSkipTLSVerify ? hostOf(c.serverUrl) : null;
    if (host) hosts.add(host);
  }
  return hosts;
}

/** The answer for a certificate from `hostname`. */
export function certVerdict(hostname: string, hosts: ReadonlySet<string>): number {
  return hosts.has(hostname.toLowerCase()) ? CERT_ACCEPT : CERT_CHROMIUMS_ANSWER;
}

/**
 * What this process has told Chromium about the certificates Chromium itself
 * would have refused. A certificate that passes by itself is not noted: the
 * switch makes no difference to it.
 */
export class CertAnswers {
  private readonly answers = new Map<string, boolean>();

  note(hostname: string, chromiumsError: number, accepted: boolean): void {
    if (chromiumsError !== 0) this.answers.set(hostname.toLowerCase(), accepted);
  }

  /** This run refused the host's certificate, and goes on doing so. */
  refused(hostname: string): boolean {
    return this.answers.get(hostname.toLowerCase()) === false;
  }

  /** The hosts where what Chromium keeps saying is not what the switches say. */
  stale(hosts: ReadonlySet<string>): string[] {
    return [...this.answers].filter(([host, accepted]) => accepted !== hosts.has(host)).map(([host]) => host);
  }
}

/**
 * What the engine is told about this account, on or off.
 *
 * ⚠ Off is said too: the engine would otherwise read ~/.filex/cli.yaml, where a
 * terminal login to the same address may have saved the switch on, and this
 * process's own environment.
 */
export function engineTlsEnv(acc: { insecureSkipTLSVerify?: boolean }): Record<string, string> {
  return { [ENV_SKIP_TLS_VERIFY]: acc.insecureSkipTLSVerify ? '1' : '0' };
}

/**
 * The switch's words, for the sign-in window and Settings: one table for both.
 * `note` is the sentence under every setting that turns a protection off, the
 * admin panel's `securityOff.note` word for word
 * (desktop/test/tls-trust.test.ts holds them together); `risk` is what this one
 * exposes.
 */
export const TLS_WORDS: Bilingual = {
  label: ['Skip certificate verification (insecure)', 'Sertifika doğrulamasını atla (güvensiz)'],
  help: [
    'Accepts any certificate from this server: for a self-signed or private certificate that cannot be trusted any other way.',
    'Bu sunucudan gelen her sertifikayı kabul eder: başka bir yolla güvenilemeyen, kendinden imzalı ya da özel bir sertifika için.',
  ],
  note: [
    'This is not safe and not recommended. Apply it only if you know what you are doing.',
    'Bu ayar güvenli değildir ve önerilmez. Ne yaptığınızı biliyorsanız uygulayın.',
  ],
  risk: [
    'Anybody who can get between this computer and the server can pose as it: read and change what crosses, and take your session. It holds for every filex on this host.',
    'Bu bilgisayar ile sunucunun arasına girebilen herkes onun yerine geçebilir: geçenleri okuyup değiştirebilir ve oturumunuzu ele geçirebilir. Bu ana bilgisayardaki her filex için geçerlidir.',
  ],
  advanced: ['Advanced', 'Gelişmiş'],
  restart: [
    'A changed certificate switch applies to this window when filex restarts: until then it goes on answering as it did.',
    'Değişen sertifika anahtarı bu pencere için filex yeniden başlayınca geçerli olur: o zamana kadar önceki gibi cevap vermeye devam eder.',
  ],
  restartNow: ['Restart now', 'Şimdi yeniden başlat'],
  restartFirst: [
    "This run of filex has already refused that server's certificate and keeps that answer until it closes. Restart filex, then sign in again with the box ticked.",
    'filex bu çalışmasında o sunucunun sertifikasını reddetti ve kapanana kadar bu cevapta kalır. filex\'i yeniden başlatın, sonra kutuyu işaretleyerek tekrar oturum açın.',
  ],
};
