// The boot stress for task #81: `node e2e/run.mjs boot-stress`.
//
// ⚠ What it is for. Once in a few hundred sign-in loads of the full suite the
// page stayed blank: one of the five files the page cannot start without
// (the entry script, its CSS, vue-vendor, i18n, icons) never reached the
// server. Measured 2026-10-01 on the Windows workstation the suite ran on:
// the browser's connection for that file failed at the OPERATING SYSTEM,
// `net::ERR_NO_BUFFER_SPACE` (WSAENOBUFS), in the same second as Windows'
// Tcpip event 4231 ("all ephemeral ports are in use"). A navigation that hit
// it said so; a module script that hit it left a blank page and no line in
// the server's log. That is a property of the machine, not of filex.
//
// This loop opens the sign-in page in fresh browser contexts back to back,
// the way the suite does, and closes each one part-way through its service
// worker's precache (the state the suite left the browser in before both
// recorded failures). For every load it records, browser side, which
// requests were made, which finished and which failed and why. A load whose
// form does not show within 10 s is a HIT, written out in full under
// e2e/.artifacts/boot-stress/; any request that failed with a host network
// error is named as such. Exit code: 0 when no load failed, 1 otherwise.
import fs from 'node:fs';
import path from 'node:path';
import { chromium } from '@playwright/test';

/** Errors that come from the host's network stack, not from filex. */
const HOST_ERRORS = /ERR_NO_BUFFER_SPACE|ERR_ADDRESS_IN_USE|ERR_ADDRESS_INVALID|ERR_INSUFFICIENT_RESOURCES|ERR_NETWORK_ACCESS_DENIED/;

/**
 * @param {{ baseURL: string, loads: number, closeAfter: string, outDir: string, log: (m: string) => void }} o
 * `closeAfter`: milliseconds after the form showed, or `rand` (0.1 to 1.1 s,
 * which on an idle machine cuts the precache at every point of its pass).
 * @returns {Promise<number>} the exit code
 */
export async function bootStress({ baseURL, loads, closeAfter, outDir, log }) {
  fs.mkdirSync(outDir, { recursive: true });
  const browser = await chromium.launch();
  const t0 = Date.now();
  const at = () => Date.now() - t0;
  let hits = 0;
  let hostErrors = 0;
  let prevWait = null;
  try {
    for (let i = 1; i <= loads; i++) {
      // The suite's own storageState (playwright.config.ts), so every context
      // is set up the way a spec's is.
      const ctx = await browser.newContext({
        storageState: { cookies: [], origins: [{ origin: baseURL, localStorage: [{ name: 'filex.openTrigger', value: 'single' }] }] },
      });
      const page = await ctx.newPage();
      await page.addInitScript(() => {
        try {
          localStorage.setItem('filex.installPrompt.dismissed', '1');
        } catch {
          /* storage blocked: the banner is just present */
        }
      });
      const trail = [];
      page.on('request', (r) => trail.push(`${at()} REQ ${r.method()} ${r.url()}`));
      page.on('requestfinished', (r) => trail.push(`${at()} FIN ${r.url()}`));
      page.on('requestfailed', (r) => trail.push(`${at()} FAIL ${r.url()} ${r.failure()?.errorText ?? ''}`));
      page.on('console', (m) => trail.push(`${at()} CONSOLE ${m.type()} ${m.text()}`));
      page.on('pageerror', (e) => trail.push(`${at()} PAGEERROR ${e.message}`));
      let ok = true;
      try {
        await page.goto(`${baseURL}/admin/login`, { timeout: 15_000 });
        await page.getByLabel(/e-?mail|kullanıcı adı/i).waitFor({ timeout: 10_000 });
      } catch (err) {
        ok = false;
        hits++;
        const host = trail.some((l) => l.includes(' FAIL ') && HOST_ERRORS.test(l)) || HOST_ERRORS.test(String(err));
        if (host) hostErrors++;
        const entries = await page
          .evaluate(() => performance.getEntriesByType('resource').map((e) => `${Math.round(e.startTime)} ${e.name} ${Math.round(e.duration)}ms`))
          .catch((e) => [`(could not read: ${e.message})`]);
        fs.writeFileSync(
          path.join(outDir, `hit-${i}.txt`),
          [
            `load ${i} at ${at()} ms; the previous context closed ${prevWait} ms after its form`,
            `error: ${String(err).split('\n')[0]}`,
            host ? 'a request failed in the HOST network stack (see FAIL lines): the machine, not filex' : 'no host network error recorded',
            '--- requests, browser side',
            ...trail,
            '--- performance entries',
            ...entries,
          ].join('\n'),
        );
        log(`HIT load ${i}${host ? ' (host network error)' : ''} -> ${path.join(outDir, `hit-${i}.txt`)}`);
      }
      const wait = closeAfter === 'rand' ? 100 + Math.floor(Math.random() * 1000) : Number(closeAfter);
      if (ok) await page.waitForTimeout(wait);
      prevWait = ok ? wait : null;
      await ctx.close();
      if (i % 50 === 0) log(`${i} loads, ${hits} hits (${hostErrors} host), ${Math.round(at() / 1000)} s`);
    }
  } finally {
    await browser.close();
  }
  log(`boot stress: ${loads} loads, ${hits} hits, ${hostErrors} of them host network errors`);
  return hits ? 1 : 0;
}
