/**
 * 154-update-policy-badge — Ops → Updates says what this install DOES by
 * itself, not the policy it cannot carry out (#72, 2026-09-25).
 *
 * A filex that Homebrew, winget, Snap or a distribution package installed
 * never replaces its own binary, and neither does a container. The page still
 * said "Politika: yamaları kur" there — a promise the install cannot keep. The
 * server now works out the effective behaviour (update.EffectiveOf) and the
 * page words it: "Yalnızca duyurur", the saved policy named as having no
 * effect here, and who upgrades instead, with the command.
 *
 *   1. Whatever install this server is, the badge is what the server says:
 *      the saved policy when it is in force, the behaviour when it is not.
 *      Measured in a Turkish and an English session.
 *   2. With FILEX_INSTALL_MODE=homebrew FILEX_UPDATE_POLICY=patch (a run of
 *      its own — the hermetic suite's server is a plain binary, and this test
 *      skips there): "Yalnızca duyurur" / "Announces only", Homebrew and
 *      `brew upgrade --cask filex` beside it, the saved policy kept.
 *      With E2E_UPDATE_CHECK=1 the spec serves a manifest with the next patch
 *      on E2E_UPDATE_MANIFEST_PORT (the server's FILEX_UPDATE_MANIFEST_URL
 *      must point there), and "Check now" shows that release with the brew
 *      command and no "Upgrade now" button.
 *
 * With E2E_SHOTS_DIR set, the Turkish and English pages are written there.
 *
 *   FILEX_INSTALL_MODE=homebrew FILEX_UPDATE_POLICY=patch \
 *   FILEX_UPDATE_MANIFEST_URL=http://127.0.0.1:5381/stable.json \
 *   E2E_UPDATE_CHECK=1 E2E_UPDATE_MANIFEST_PORT=5381 \
 *     node e2e/run.mjs local --port 5380 --grep policy-badge-72
 */
import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import http from 'node:http';
import { apiLogin, dismissInstallBanner, loginAs } from '../helpers/auth';

const STAMP = Date.now();
const TR_ADMIN = `policy-badge-${STAMP}@example.com`;
const TR_PW = 'Policy-badge-2026!';
const SHOTS = process.env.E2E_SHOTS_DIR;
const CHECK = process.env.E2E_UPDATE_CHECK === '1';
const MANIFEST_PORT = Number(process.env.E2E_UPDATE_MANIFEST_PORT ?? 0);

interface Status {
  mode: string;
  policy: string;
  behavior?: string;
  policy_limit?: string;
  package_manager?: string;
  package_manager_name?: string;
  upgrade_command?: string;
  current: string;
}

// The catalogue's words (web/src/locales), as a person reads them.
const WORDS = {
  tr: {
    policyIs: 'Politika: ',
    policyName: { off: 'kapalı', manual: 'yalnızca duyur', patch: 'yamaları kur', minor: 'ara sürümleri kur' },
    behavior: { off: 'Kontrol kapalı', announce: 'Yalnızca duyurur', patch: 'Yamaları kurar', minor: 'Ara sürümleri kurar' },
    mode: 'Homebrew kurulumu',
    check: 'Şimdi kontrol et',
    apply: 'Şimdi yükselt',
  },
  en: {
    policyIs: 'Policy: ',
    policyName: { off: 'off', manual: 'announce only', patch: 'install patches', minor: 'install minor releases' },
    behavior: { off: 'Checking off', announce: 'Announces only', patch: 'Installs patches', minor: 'Installs minor releases' },
    mode: 'Homebrew install',
    check: 'Check now',
    apply: 'Upgrade now',
  },
} as const;
type Lang = keyof typeof WORDS;

async function signInTurkish(page: Page) {
  await dismissInstallBanner(page);
  await page.addInitScript(() => {
    try {
      localStorage.setItem('filex.tourDone', '1');
    } catch {
      /* private window */
    }
  });
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(TR_ADMIN);
  await page.getByLabel(/password|parola/i).first().fill(TR_PW);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/);
}

async function signIn(page: Page, lang: Lang) {
  if (lang === 'tr') await signInTurkish(page);
  else await loginAs(page);
}

/** A release manifest whose newest release is the running version's next
 *  patch, clean and auto_ok: the release a "patch" policy would take by
 *  itself on a plain binary. */
async function serveNextPatch(current: string, port: number): Promise<http.Server> {
  const m = /^v?(\d+)\.(\d+)\.(\d+)/.exec(current);
  if (!m) throw new Error(`the running version ${current} is not semantic; build with -X …version.Version=vX.Y.Z`);
  const next = `v${m[1]}.${m[2]}.${Number(m[3]) + 1}`;
  const body = JSON.stringify({
    channel: 'stable',
    releases: [
      { version: `v${m[1]}.${m[2]}.${m[3]}`, auto_ok: true, migrations: false },
      { version: next, date: '2026-09-25', auto_ok: true, migrations: false, notes: 'A patch release' },
    ],
  });
  const srv = http.createServer((req, res) => {
    const found = req.url === '/stable.json';
    res.writeHead(found ? 200 : 404, { 'Content-Type': 'application/json' });
    res.end(found ? body : '{}');
  });
  await new Promise<void>((resolve) => srv.listen(port, '127.0.0.1', resolve));
  return srv;
}

async function readStatus(request: APIRequestContext): Promise<Status> {
  await apiLogin(request);
  const res = await request.get('/api/admin/update');
  expect(res.ok(), await res.text()).toBe(true);
  return (await res.json()) as Status;
}

test.describe('Updates: the policy badge says what the install does (policy-badge-72)', () => {
  let status: Status;
  let manifest: http.Server | null = null;

  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    const made = await request.post('/api/admin/users', {
      data: { email: TR_ADMIN, password: TR_PW, role: 'admin', locale: 'tr', display_name: 'Sürüm' },
    });
    expect(made.ok(), await made.text()).toBe(true);
    status = await readStatus(request);
    if (CHECK && MANIFEST_PORT) manifest = await serveNextPatch(status.current, MANIFEST_PORT);
  });

  test.afterAll(async () => {
    await new Promise<void>((resolve) => (manifest ? manifest.close(() => resolve()) : resolve()));
  });

  for (const lang of ['tr', 'en'] as Lang[]) {
    test(`${lang}: the badge is the server's word for this install`, async ({ page }) => {
      const w = WORDS[lang];
      expect(status.behavior, 'the server reports the effective behaviour').toBeTruthy();
      await signIn(page, lang);
      await page.goto('/admin/updates');
      const badge = page.getByTestId('updates-policy');
      if (status.policy_limit) {
        await expect(badge).toHaveText(w.behavior[status.behavior as keyof typeof w.behavior]);
        await expect(page.getByTestId('updates-policy-note')).toContainText(
          w.policyName[status.policy as keyof typeof w.policyName],
        );
      } else {
        await expect(badge).toHaveText(w.policyIs + w.policyName[status.policy as keyof typeof w.policyName]);
        await expect(page.getByTestId('updates-policy-note')).toHaveCount(0);
      }
      if (SHOTS) await page.screenshot({ path: `${SHOTS}/updates-${status.mode}-${status.policy}-${lang}.png`, fullPage: true });
    });
  }

  for (const lang of ['tr', 'en'] as Lang[]) {
    test(`${lang}: a Homebrew install set to "patch" announces only and names brew`, async ({ page }) => {
      test.skip(
        status.mode !== 'package' || status.package_manager !== 'homebrew' || status.policy !== 'patch',
        'needs FILEX_INSTALL_MODE=homebrew FILEX_UPDATE_POLICY=patch on the server',
      );
      const w = WORDS[lang];
      await signIn(page, lang);
      await page.goto('/admin/updates');
      await expect(page.getByTestId('updates-mode')).toHaveText(w.mode);
      // What a person reads first; the wire fields after it.
      await expect(page.getByTestId('updates-policy')).toHaveText(w.behavior.announce);
      expect(status.behavior).toBe('announce');
      expect(status.policy_limit).toBe('package');
      const note = page.getByTestId('updates-policy-note');
      await expect(note).toContainText(w.policyName.patch);
      await expect(note).toContainText('Homebrew');
      await expect(page.getByTestId('updates-policy-command')).toHaveText('brew upgrade --cask filex');
      await expect(page.locator('main')).not.toContainText(w.policyIs + w.policyName.patch);

      if (CHECK) {
        await page.getByRole('button', { name: w.check }).click();
        const howto = page.getByTestId('updates-howto');
        await expect(howto.locator('pre')).toContainText('brew upgrade --cask filex');
        await expect(howto).not.toContainText('self-update');
        await expect(page.getByRole('button', { name: w.apply })).toHaveCount(0);
        // The saved policy is still what was saved.
        const after = await page.request.get('/api/admin/update');
        expect((await after.json()).policy).toBe('patch');
      }
      if (SHOTS) await page.screenshot({ path: `${SHOTS}/updates-homebrew-${lang}.png`, fullPage: true });
    });
  }
});
