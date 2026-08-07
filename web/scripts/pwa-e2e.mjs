// Standalone Playwright E2E for the PWA install + update surface (Dilim 1).
//
// Why standalone (not @playwright/test, not Cypress): Cypress's bundled Electron
// renderer crashes at startup on this memory/GPU-constrained shared host (every
// run reports "Duration: 0s" before the spec loads). Playwright's chromium with
// --no-sandbox --disable-dev-shm-usage runs where that Electron won't. This
// drives the REAL Vue app served by the Vite dev server (PWA plugin enabled),
// reachable at localhost:5173 from this container.
//
// Usage: node scripts/pwa-e2e.mjs   (needs the dev server up on :5173)
// playwright-core lives under the pnpm store, not linked into web/node_modules;
// resolve it from the monorepo root at runtime so this works regardless of the
// pinned version.
import { readdirSync } from 'node:fs';
const PNPM = '/home/coder/filemanager/node_modules/.pnpm';
const pwDir = readdirSync(PNPM).find((d) => d.startsWith('playwright-core@'));
const { chromium } = await import(`${PNPM}/${pwDir}/node_modules/playwright-core/index.mjs`);

const BASE = process.env.PWA_BASE || 'http://localhost:5173';
const LOGIN = `${BASE}/admin/login`;

const results = [];
function check(name, cond, detail = '') {
  results.push({ name, ok: !!cond, detail });
  // eslint-disable-next-line no-console
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`);
}

// Injected before any app script runs: pin a non-standalone, non-iOS desktop
// environment (or override per-scenario). Mirrors real Chrome desktop.
const desktopInit = `
  (() => {
    const real = window.matchMedia && window.matchMedia.bind(window);
    window.matchMedia = (q) => ({ matches:false, media:q, onchange:null,
      addEventListener(){}, removeEventListener(){}, addListener(){}, removeListener(){}, dispatchEvent(){return false;} });
    window.__realMM = real;
  })();
`;

// Fire a faithful synthetic beforeinstallprompt with a working prompt()/userChoice.
const fireBip = (outcome) => `
  (() => {
    const e = new Event('beforeinstallprompt');
    e.platforms = ['web'];
    window.__promptCalled = false;
    e.prompt = () => { window.__promptCalled = true; return Promise.resolve(); };
    e.userChoice = Promise.resolve({ outcome: '${outcome}', platform: 'web' });
    window.dispatchEvent(e);
  })();
`;

async function newPage(browser, initScript) {
  const ctx = await browser.newContext();
  await ctx.addInitScript(initScript);
  const page = await ctx.newPage();
  return { ctx, page };
}

async function main() {
  const browser = await chromium.launch({
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu'],
  });

  // ── Scenario 1: manifest linked + valid ──────────────────────────────────
  {
    const { ctx, page } = await newPage(browser, desktopInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    const href = await page.getAttribute('link[rel="manifest"]', 'href');
    check('manifest <link> present', href && /manifest/.test(href), href || 'missing');
    const manifest = await page.evaluate(async () => {
      const r = await fetch('/admin/manifest.webmanifest');
      return { type: r.headers.get('content-type'), body: await r.json() };
    });
    check('manifest content-type', /manifest/.test(manifest.type), manifest.type);
    check('manifest scope=/admin/', manifest.body.scope === '/admin/', manifest.body.scope);
    check('manifest start_url=/admin/', manifest.body.start_url === '/admin/');
    check('manifest display=standalone', manifest.body.display === 'standalone');
    check(
      'manifest has maskable icon',
      manifest.body.icons?.some((i) => i.purpose === 'maskable'),
    );
    await ctx.close();
  }

  // ── Scenario 2: install banner appears after beforeinstallprompt ──────────
  {
    const { ctx, page } = await newPage(browser, desktopInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    const before = await page.locator('[data-testid="pwa-install-banner"]').count();
    check('banner hidden before prompt', before === 0);
    await page.evaluate(fireBip('accepted'));
    await page.waitForSelector('[data-testid="pwa-install-banner"]', { timeout: 5000 });
    check('banner visible after prompt', await page.locator('[data-testid="pwa-install-banner"]').isVisible());
    check('install button visible', await page.locator('[data-testid="pwa-install-button"]').isVisible());
    check(
      'no iOS instructions on desktop',
      (await page.locator('[data-testid="pwa-ios-instructions"]').count()) === 0,
    );
    await ctx.close();
  }

  // ── Scenario 3: clicking Install triggers native prompt + clears offer ────
  {
    const { ctx, page } = await newPage(browser, desktopInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    await page.evaluate(fireBip('accepted'));
    await page.waitForSelector('[data-testid="pwa-install-button"]');
    await page.click('[data-testid="pwa-install-button"]');
    const called = await page.evaluate(() => window.__promptCalled);
    check('native prompt() called', called === true);
    await page.waitForSelector('[data-testid="pwa-install-button"]', { state: 'detached', timeout: 5000 });
    check('offer cleared after accept', (await page.locator('[data-testid="pwa-install-button"]').count()) === 0);
    await ctx.close();
  }

  // ── Scenario 4: dismiss hides + persists across reload ────────────────────
  {
    const { ctx, page } = await newPage(browser, desktopInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    await page.evaluate(fireBip('accepted'));
    await page.waitForSelector('[data-testid="pwa-install-dismiss"]');
    await page.click('[data-testid="pwa-install-dismiss"]');
    await page.waitForSelector('[data-testid="pwa-install-banner"]', { state: 'detached', timeout: 5000 });
    const persisted = await page.evaluate(() => localStorage.getItem('filex.installPrompt.dismissed'));
    check('dismiss persisted to localStorage', persisted === '1', String(persisted));
    // Reload (same context keeps localStorage) → offer stays hidden.
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.evaluate(fireBip('accepted'));
    await page.waitForTimeout(300);
    check(
      'offer stays hidden after reload',
      (await page.locator('[data-testid="pwa-install-banner"]').count()) === 0,
    );
    await ctx.close();
  }

  // ── Scenario 5: iOS Safari → manual instructions, no button ───────────────
  {
    const iosInit =
      desktopInit +
      `Object.defineProperty(navigator, 'userAgent', { get: () => 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1' });`;
    const { ctx, page } = await newPage(browser, iosInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('[data-testid="pwa-ios-instructions"]', { timeout: 5000 });
    check('iOS instructions visible', await page.locator('[data-testid="pwa-ios-instructions"]').isVisible());
    check(
      'iOS shows no install button',
      (await page.locator('[data-testid="pwa-install-button"]').count()) === 0,
    );
    await ctx.close();
  }

  // ── Scenario 6: already installed (standalone) → nothing offered ──────────
  {
    const standaloneInit = `
      window.matchMedia = (q) => ({ matches: /standalone/.test(q), media:q, onchange:null,
        addEventListener(){}, removeEventListener(){}, addListener(){}, removeListener(){}, dispatchEvent(){return false;} });
    `;
    const { ctx, page } = await newPage(browser, standaloneInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    await page.evaluate(fireBip('accepted'));
    await page.waitForTimeout(300);
    check(
      'standalone suppresses install banner',
      (await page.locator('[data-testid="pwa-install-banner"]').count()) === 0,
    );
    await ctx.close();
  }

  // ── Scenario 7: service worker registers under /admin/ ────────────────────
  {
    const { ctx, page } = await newPage(browser, desktopInit);
    await page.goto(LOGIN, { waitUntil: 'domcontentloaded' });
    const swScope = await page.evaluate(async () => {
      if (!('serviceWorker' in navigator)) return null;
      // Give the dev SW a moment to register via useRegisterSW.
      for (let i = 0; i < 20; i++) {
        const reg = await navigator.serviceWorker.getRegistration('/admin/');
        if (reg) return reg.scope;
        await new Promise((r) => setTimeout(r, 250));
      }
      return 'none';
    });
    check('service worker registered under /admin/', swScope && swScope.includes('/admin/'), String(swScope));
    await ctx.close();
  }

  await browser.close();

  const failed = results.filter((r) => !r.ok);
  console.log(`\n==== ${results.length - failed.length}/${results.length} checks passed ====`);
  if (failed.length) {
    console.log('FAILED: ' + failed.map((f) => f.name).join(' | '));
    process.exit(1);
  }
}

main().catch((e) => {
  console.error('HARNESS ERROR:', e);
  process.exit(2);
});
