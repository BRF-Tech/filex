/**
 * Finding and installing a real app plugin for the e2e suite.
 *
 * The two apps filex ships alongside itself — `sign` and `convert` — live in
 * their own repositories, so their `plugin.wasm` is not in this tree. A spec
 * that needs one asks for it here:
 *
 *   const app = resolveApp('convert');
 *   test.skip(!app.present, app.skipReason);
 *
 * With `FILEX_REQUIRE_WASM_FIXTURE=1` a missing module is a FAILURE instead
 * of a skip — that is what CI sets, so "the suite was green" can never mean
 * "the suite skipped everything".
 */
import { expect, test, type Page } from '@playwright/test';
import { APP_LOCATIONS, locateApp } from './app-locations.mjs';

export interface AppManifest {
  name: string;
  version: string;
  label?: Record<string, string>;
  permissions: string[];
  languages?: string[];
  actions?: { id: string; label?: Record<string, string>; view?: string; hidden?: boolean }[];
  views?: { id: string; placement: string }[];
  public_pages?: { id: string }[];
}

export interface AppFixture {
  name: string;
  present: boolean;
  wasm: string;
  manifestPath: string;
  manifest: AppManifest | undefined;
  /** The languages the manifest promises; [] when it declares none. */
  languages: string[];
  skipReason: string;
}

export function requireFixtures(): boolean {
  const v = process.env.FILEX_REQUIRE_WASM_FIXTURE;
  return !!v && v !== '0' && v !== 'false';
}

/**
 * Locate an app's built module and its manifest.
 *
 * Where to look is `app-locations.mjs` — one table, shared with the
 * screenshot scripts (`e2e/shots/scene.mjs`), so the specs and the pictures
 * can never be looking at two different builds. ⚠ An explicit
 * `FILEX_*_APP_DIR` is AUTHORITATIVE there: when it is set, that directory is
 * the only one looked in.
 */
export function resolveApp(name: keyof typeof APP_LOCATIONS | string): AppFixture {
  const found = locateApp(name);
  if (found.present) {
    const manifest = found.manifest as AppManifest;
    return {
      name,
      present: true,
      wasm: found.wasm,
      manifestPath: found.manifestPath,
      manifest,
      languages: manifest.languages ?? [],
      skipReason: '',
    };
  }
  const env = APP_LOCATIONS[name]?.env;
  return {
    name,
    present: false,
    wasm: '',
    manifestPath: '',
    manifest: undefined,
    languages: [],
    skipReason: `the ${name} app's plugin.wasm is not built: ${found.how}. Set ${env ?? 'the app dir'} to point at it, or FILEX_REQUIRE_WASM_FIXTURE=1 to make this a failure.`,
  };
}

/**
 * Guard for a spec that needs a module: skip locally, fail on CI.
 *
 * Playwright's own skip, not a bespoke flag — a skipped test is reported as
 * skipped, and `--forbid-only`-style strictness stays with the runner.
 */
export function guardFixture(app: AppFixture, testSkip: (condition: boolean, reason: string) => void) {
  if (!app.present && requireFixtures()) {
    throw new Error(`FILEX_REQUIRE_WASM_FIXTURE is set and ${app.skipReason}`);
  }
  testSkip(!app.present, app.skipReason);
}

/**
 * ⚠⚠ How long ONE app install may take in a spec — the one allowance every
 * app spec gets, so no spec carries its own copy of the number.
 *
 * filex compiles the module before it answers the install. Measured: the
 * 14 MiB converter takes ~23 s on an idle machine and the 17-20 MB signing
 * module 29 s on a busy one; with a build running beside the suite both pass
 * 30 s — Playwright's default for a WHOLE test. Spec 96 then failed three
 * runs in a row at 30 s on a machine at 100 % CPU while the same install
 * over the API answered 201 "running" in 23 s; spec 97 had been given 90 s
 * for the same reason (2026-09-21). How fast a busy machine compiles is not
 * what these specs test, so the install gets this much on top of the test's
 * own budget.
 *
 * ⚠ The admin page's own install request must wait as long. It waits up to
 * 180 s since feat/043-signing (`INSTALL_TIMEOUT_MS`, web/src/api/appPlugins.ts);
 * before that it gave up at the API client's 30 s default and the wizard
 * showed an error — which the wait below reports as the wizard's sentence.
 */
export const APP_INSTALL_ALLOWANCE_MS = 90_000;

/**
 * Install the app the way an administrator does: the wizard, the permission
 * review, the "I understand" tick. It is the only supported install path, so
 * the specs use it rather than a back door.
 *
 * It extends the running test's timeout by `APP_INSTALL_ALLOWANCE_MS`, so a
 * spec calls it and sets no timeout of its own for the install.
 */
export async function installThroughWizard(page: Page, app: AppFixture) {
  test.info().setTimeout(test.info().timeout + APP_INSTALL_ALLOWANCE_MS);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await page.goto('/admin/plugins');
  await page.getByTestId('plugins-tab-apps').click();
  await expect(page.getByTestId('app-plugins')).toBeVisible();
  await page.getByTestId('app-plugin-add').click();
  await expect(page.getByTestId('app-plugin-wizard')).toBeVisible();
  await page.getByTestId('app-plugin-source-file').click();
  await page.getByTestId('app-plugin-wasm').setInputFiles(app.wasm);
  await page.getByTestId('app-plugin-manifest').setInputFiles(app.manifestPath);
  await page.getByTestId('app-plugin-review').click();

  // Nothing is granted without being read first.
  const rows = page.getByTestId('app-plugin-permissions');
  await expect(rows).toBeVisible();
  for (const perm of app.manifest?.permissions ?? []) {
    await expect(rows, `permission ${perm} is reviewed before it is granted`).toContainText(perm);
  }
  const install = page.getByTestId('app-plugin-install');
  await expect(install).toBeDisabled();
  await page.getByLabel(/I understand|Anladım/).check();
  await expect(install).toBeEnabled();
  await install.click();

  /**
   * ⚠⚠ The wizard's OWN error is read before the wait runs out.
   *
   * Waiting only for the installed row turns every refusal into the same
   * "element(s) not found" at the deadline — which says nothing about WHY.
   * Measured 2026-09-20: `97-app-plugin-sign` failed exactly like that while
   * the wizard was plainly saying "the build and the manifest do not belong
   * together", because the sibling checkout's `dist/filex-app.json` was
   * several versions older than the `dist/plugin.wasm` beside it (only
   * `build.sh --stamp` refreshes that copy). A red that names the cause is
   * the difference between a five-minute fix and an afternoon.
   */
  const installed = page.getByTestId(`app-plugin-${app.name}`);
  const failure = page.getByTestId('app-plugin-wizard-error');
  const deadline = Date.now() + APP_INSTALL_ALLOWANCE_MS;
  for (;;) {
    if (await installed.isVisible().catch(() => false)) break;
    const said = (await failure.textContent().catch(() => null))?.trim();
    if (said) throw new Error(`the install wizard refused the ${app.name} module: ${said}`);
    if (Date.now() > deadline) break;
    await page.waitForTimeout(250);
  }
  await expect(installed, `the ${app.name} app is installed and listed`).toBeVisible();
  await expect(page.getByTestId('app-plugins')).toContainText(/running|çalışıyor/i);
}

/** A 1×1 PNG, small enough to inline and real enough to decode. */
export const PNG_1PX = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
);

/**
 * The smallest PDF that is a PDF: one empty A4 page, with the xref table a
 * reader needs. Written by hand so the suite does not depend on a producer.
 */
export function minimalPDF(text = 'e2e'): Buffer {
  const objects = [
    '1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n',
    '2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n',
    '3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n',
    '4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n',
  ];
  const stream = `BT /F1 24 Tf 72 760 Td (${text.replace(/[()\\]/g, '')}) Tj ET`;
  objects.push(`5 0 obj\n<< /Length ${stream.length} >>\nstream\n${stream}\nendstream\nendobj\n`);

  let body = '%PDF-1.4\n';
  const offsets: number[] = [];
  for (const o of objects) {
    offsets.push(body.length);
    body += o;
  }
  const xref = body.length;
  body += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const off of offsets) body += `${String(off).padStart(10, '0')} 00000 n \n`;
  body += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(body, 'latin1');
}
