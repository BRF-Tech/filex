// 0.50 - "Choose an app…" (core modals/OpenWithDialog) and the explorer's
// file menu: the handlers the administrator left on, the one that opens it
// now, and "Always use this app for .<ext> files" kept on the account.
import { describe, expect, it } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import OpenWithDialog from '@brftech/filex-core/src/modals/OpenWithDialog.vue';
import type { OpenHandler } from '@brftech/filex-core/src/lib/appViewer';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';

const sketch = {
  plugin: 'sketch',
  id: 'editor',
  placement: 'viewer',
  label: { en: 'Sketch', tr: 'Eskiz' },
} as unknown as PluginViewRow;

const HANDLERS: OpenHandler[] = [
  { id: 'app:sketch/editor', view: sketch },
  { id: 'builtin', view: null },
];

function open(props: Record<string, unknown> = {}) {
  return mount(OpenWithDialog, {
    props: {
      open: true,
      locale: 'en',
      name: 'plan.sketch',
      kind: 'sketch',
      handlers: HANDLERS,
      current: 'app:sketch/editor',
      remembered: null,
      ...props,
    },
    attachTo: document.body,
  });
}

// setup.ts unmounts every page and then empties <body> after each test
// (helpers/teardown.ts): emptying it here first would leave the dialog
// mounted over a missing DOM.

describe('OpenWithDialog', () => {
  it('lists the handlers that are on, marks the one that opens it now', async () => {
    open();
    await flushPromises();
    const dialog = document.querySelector('[data-testid="open-with-dialog"]');
    expect(dialog?.textContent).toContain('Which app should open plan.sketch?');
    expect(document.querySelector('[data-testid="open-with-choice-app:sketch/editor"]')?.textContent).toContain('Sketch');
    expect(document.querySelector('[data-testid="open-with-choice-builtin"]')?.textContent).toContain('filex viewer (built-in)');
    expect(document.querySelector('[data-testid="open-with-choice-app:sketch/editor"]')?.textContent).toContain('Opens it now');
    expect(document.body.textContent).toContain('Always use this app for .sketch files');
  });

  it('answers the pick, and "always" only when it was ticked', async () => {
    const w = open();
    await flushPromises();
    (document.querySelector('[data-testid="open-with-choice-builtin"]') as HTMLButtonElement).click();
    await flushPromises();
    (document.querySelector('[data-testid="open-with-open"]') as HTMLButtonElement).click();
    expect(w.emitted('open')).toEqual([['builtin', false]]);

    (document.querySelector('[data-testid="open-with-always"]') as HTMLInputElement).click();
    await flushPromises();
    (document.querySelector('[data-testid="open-with-open"]') as HTMLButtonElement).click();
    expect(w.emitted('open')?.[1]).toEqual(['builtin', true]);
  });

  it('starts ticked when the person already chose this one for the kind', async () => {
    open({ remembered: 'app:sketch/editor' });
    await flushPromises();
    expect((document.querySelector('[data-testid="open-with-always"]') as HTMLInputElement).checked).toBe(true);
  });

  it('speaks Turkish', async () => {
    open({ locale: 'tr' });
    await flushPromises();
    expect(document.body.textContent).toContain('plan.sketch hangi uygulamayla açılsın?');
    expect(document.body.textContent).toContain('.sketch dosyalarını her zaman bu uygulamayla aç');
    expect(document.body.textContent).toContain('Eskiz');
  });
});

describe('the explorer’s file menu', () => {
  // FileExplorer is not mounted in unit tests; the wiring is read off its
  // source, like the other explorer wiring tests.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

  it('offers the handlers that are on, and "Choose an app…", only when there is more than one', () => {
    const rows = src.slice(src.indexOf('function openWithRows'), src.indexOf('/* ── "Choose an app…"'));
    expect(rows).toMatch(/const \{ on \} = openHandlersOf\(sel\[0\]\);/);
    // 0.51: an administrator also sees "Open with ONLYOFFICE" greyed for a
    // .csv while ONLYOFFICE is not there (csvInOffice.test.ts).
    expect(rows).toMatch(/if \(on\.length < 2 && missing\.length === 0\) return \[\];/);
    expect(rows).toMatch(/key: 'open-with-choose'/);
  });

  it('opens a file with the person’s choice, the administrator’s order and the Open with pick - one rule', () => {
    expect(src).toMatch(
      /pickOpenHandler\(\s*pluginViewList\.value,\s*previewTarget\.value,\s*previewAppChoice\.value,\s*pluginOpenRules\.value,\s*personalOpenChoice\(previewTarget\.value\),\s*openOpts\.value,\s*\)/,
    );
    expect(src).toMatch(/<OpenWithDialog[\s\S]*?@open="openWithChosen"/);
    const chosen = src.slice(src.indexOf('function openWithChosen'), src.indexOf('const stopFollowingOpenWith'));
    // The kind is the FILE's, read before the dialog's target is cleared: the
    // computed kind of a cleared target is '' and "Always" was dropped (the
    // browser run of e2e 185 found it).
    expect(chosen).toMatch(/const kind = openKindOf\(n\);\s*openWithTarget\.value = null;\s*if \(always && kind\) setOpenWithChoice\(kind, id\);/);
    expect(chosen).not.toMatch(/openWithKind\.value/);
  });

  it('inside an encrypted folder only filex’s own viewer opens anything', () => {
    const of = src.slice(src.indexOf('function openHandlersOf'), src.indexOf('function personalOpenChoice'));
    expect(of).toMatch(/if \(e2eActive\.value\) return/);
  });
});
