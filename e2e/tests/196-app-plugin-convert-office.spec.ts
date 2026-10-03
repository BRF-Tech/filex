/**
 * 196-app-plugin-convert-office - an office document converts through the
 * connected ONLYOFFICE (filex 0.50).
 *
 * LibreOffice left the images; the apps' office engine (`engines:office`,
 * the old `engines:libreoffice` being the same engine) is the ONLYOFFICE
 * Document Server filex is connected to. The walk, through the real
 * `filex-convert` module:
 *
 *   - CONNECTED (the server under test has a Document Server configured,
 *     FILEX_ONLYOFFICE_URL + FILEX_ONLYOFFICE_JWT or External services): a
 *     .docx converted to .odt - a target only the office engine makes, the
 *     sandbox's pure-Go writers have no ODT - lands beside it as a real
 *     OpenDocument text (a zip whose first entry is its mimetype).
 *   - NOT CONNECTED: .odt is not a button; it is in the grey list, which
 *     names the engine it needs - and an administrator is never told to
 *     "install" anything for it.
 *
 * Red before 0.50 on an image without LibreOffice: the office engine was a
 * binary, absent, and the grey row named LibreOffice; with a Document Server
 * connected nothing changed (the engine did not use it).
 *
 * The module lives in a sibling checkout; without it the spec skips, unless
 * FILEX_REQUIRE_WASM_FIXTURE=1 (CI) makes that a failure.
 */
import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, waitForOp } from '../helpers/seed';
import { guardFixture, installThroughWizard, resolveApp } from '../helpers/appPlugin';
import {
  autoFill,
  fieldsOf,
  openView,
  removeApp,
  surfaceEvent,
  textOf,
  walkNodes,
  type FieldText,
  type Node,
  type Surface,
} from '../helpers/surface';

const APP = resolveApp('convert');
const FIXTURE = resolve(dirname(fileURLToPath(import.meta.url)), '../fixtures/file-types/letter.docx');

const STORAGE = `e2e-convert-office-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DOCX = 'letter.docx';
const QUALIFIED = `${STORAGE}://${DOCX}`;
const TARGET_GROUP = /^target_[a-z]+(__[a-z0-9.]+)?$/;

function targetChoices(s: Surface): { value: string; group: string }[] {
  return fieldsOf(s)
    .filter((f) => TARGET_GROUP.test(f.key))
    .flatMap((f) => (f.options ?? []).map((o) => ({ value: o.value, group: f.key })));
}

function greyRows(s: Surface): { id: string; cells: Record<string, FieldText> }[] {
  const lists: Node[] = [];
  walkNodes(s.nodes, (_w, n) => {
    if (n.type === 'list') lists.push(n);
  });
  return lists.flatMap((l) => (l.props?.rows as { id: string; cells: Record<string, FieldText> }[]) ?? []);
}

function formValues(s: Surface): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  walkNodes(s.nodes, (_w, n) => {
    if (n.type === 'form') Object.assign(out, (n.props?.values as Record<string, unknown>) ?? {});
  });
  return out;
}

test.describe('App plugin: convert - office documents through ONLYOFFICE', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': {
          name: DOCX,
          mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          buffer: readFileSync(FIXTURE),
        },
      },
    });
    if (!up.ok()) throw new Error(`upload ${DOCX} failed: ${up.status()} ${await up.text()}`);
    await removeApp(request, 'convert');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'convert');
    await dropStorageByName(request, STORAGE);
  });

  test('install the converter', async ({ page }) => {
    await loginAs(page);
    await installThroughWizard(page, APP);
    const perms = APP.manifest?.permissions ?? [];
    expect(
      perms.some((p) => p === 'engines:office' || p === 'engines:libreoffice'),
      'the converter asks for the office engine, under either of its names',
    ).toBe(true);
  });

  test('a .docx becomes an .odt through the office engine, or says what it needs', async ({ request }) => {
    test.setTimeout(180_000);
    await apiLogin(request);
    const runtime = await request.get('/api/admin/app-plugins');
    expect(runtime.ok()).toBe(true);
    const engines = ((await runtime.json()).runtime?.engines ?? {}) as Record<string, boolean>;
    expect(Object.keys(engines), 'the office engine is listed once, as `office`').toContain('office');
    expect(Object.keys(engines)).not.toContain('libreoffice');
    const connected = engines.office === true;

    const opened = await openView(request, 'convert', 'options', QUALIFIED);
    const odt = targetChoices(opened).find((c) => c.value === 'odt');

    if (!connected) {
      test.info().annotations.push({ type: 'note', description: 'no ONLYOFFICE connected: measuring the grey list' });
      expect(odt, 'with no document server, ODT is not a button').toBeFalsy();
      const row = greyRows(opened).find((r) => r.id === 'odt');
      expect(row, 'ODT is in the grey list').toBeTruthy();
      const needs = textOf(row!.cells?.needs).trim();
      expect(needs, 'the grey row names what it needs').not.toBe('');
      if ((APP.manifest?.permissions ?? []).includes('engines:office')) {
        expect(needs).toContain('ONLYOFFICE');
      }
      return;
    }

    expect(odt, 'with ONLYOFFICE connected, ODT is offered').toBeTruthy();
    const chosen = await surfaceEvent(request, 'convert', 'options', {
      path: QUALIFIED,
      event: 'change',
      state: opened.state ?? {},
      data: { values: { [odt!.group]: 'odt' } },
    });
    let surface = chosen.surface!;
    let primary = (surface.actions ?? []).find((a) => a.primary);
    let res = await surfaceEvent(request, 'convert', 'options', {
      path: QUALIFIED,
      event: 'submit',
      action_id: primary!.id,
      state: surface.state ?? {},
      data: { values: formValues(surface) },
    });
    for (let step = 0; step < 6 && !res.op; step++) {
      surface = res.surface!;
      expect(surface, `step ${step + 2} answered neither a screen nor a job`).toBeTruthy();
      primary = (surface.actions ?? []).find((a) => a.primary);
      res = await surfaceEvent(request, 'convert', 'options', {
        path: QUALIFIED,
        event: 'submit',
        action_id: primary!.id,
        state: surface.state ?? {},
        data: { values: autoFill(surface) },
      });
    }
    expect(res.op?.id, 'the wizard queued a job').toBeTruthy();
    const done = await waitForOp(request, res.op!.id, 150_000);
    expect(done.status, `the conversion finished: ${JSON.stringify(done)}`).toBe('ok');

    const raw = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://letter.odt`)}`,
    );
    expect(raw.ok(), 'letter.odt landed beside the .docx').toBe(true);
    const body = await raw.body();
    expect(body.subarray(0, 2).toString('latin1'), 'a zip').toBe('PK');
    expect(body.subarray(0, 120).toString('latin1'), 'an OpenDocument text').toContain(
      'application/vnd.oasis.opendocument.text',
    );
  });
});
