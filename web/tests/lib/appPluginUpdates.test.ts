// What the Apps list says about an app's updates and its `filex` range.
//
// ⚠ The rows are the SERVER's own bytes (backend/internal/api/handlers/
// testdata/wire/app-plugin-update-check.json, written by
// app_plugins_wire_test.go from wasmplugin.Status): an update finding typed
// the client's way here could only agree with the client.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { AppPlugin } from '@/api/appPlugins';
import { updateRank, updateView } from '@/lib/appPluginUpdates';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
const check = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-update-check.json'), 'utf8'));
const [sign, pack] = check.plugins as AppPlugin[];

function tOf(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return i18n.global.t as unknown as (k: string, v?: Record<string, unknown>) => string;
}

describe('updateView', () => {
  it('a newer version that asks for more: needs approval, the jump and what it adds, reviewable', () => {
    const v = updateView(sign, tOf('en'), 'en');
    expect(v.badges.map((b) => b.label)).toEqual([en.appPlugins.update.needsApproval]);
    expect(v.badges[0].tone).toBe('amber');
    expect(v.lines).toEqual(['1.2.0 → 1.3.0 · new: mail:send']);
    expect(v.reviewable).toBe(true);

    // What it adds is the server's line (update_said), said in the reader's
    // language when the list was read; the list lays it after the jump.
    expect(sign.update_said).toBe('new: mail:send');
    const trv = updateView({ ...sign, update_said: 'yeni: mail:send' }, tOf('tr'), 'tr');
    expect(trv.badges[0].label).toBe('Onay bekliyor');
    expect(trv.lines[0]).toBe('1.2.0 → 1.3.0 · yeni: mail:send');
  });

  it('an app outside its own range for this filex is marked — and still says what waits, and what is kept', () => {
    const v = updateView(pack, tOf('en'), 'en');
    expect(v.badges.map((b) => [b.label, b.tone])).toEqual([
      [en.appPlugins.compat.bad, 'rose'],
      [en.appPlugins.update.available, 'sky'],
    ]);
    // The range and the kept version are the server's lines (compat.message,
    // previous.message - on the reader's clock), in English in the fixture.
    expect(v.lines).toEqual([
      'Works with filex >=0.45.0 <0.47.0; this is 0.47.0. It keeps running.',
      '1.0.0 → 1.1.0',
      'Version 0.9.0 is kept to go back to (replaced 2026-09-24 08:00 UTC)',
    ]);
    expect(v.badges[0].title).toBe(pack.compat!.message);
    expect(v.reviewable).toBe(true);
    expect(updateRank(pack)).toBeLessThan(updateRank(sign));
  });

  // RED before 0.55: the list built these lines from the row's fields with
  // copies of its own (appPlugins.update.needsNewerFilex, failedDetail,
  // newPermissions, addsModule, noSource, noManifestAddress, previous,
  // appPlugins.compat.needs) and decided which one a row got.
  it('builds no line of its own: every sentence is the server’s, and the locales keep no copy', () => {
    const src = readFileSync(path.resolve(__dirname, '../../src/lib/appPluginUpdates.ts'), 'utf8');
    expect(src).not.toMatch(/appPlugins\.update\.(needsNewerFilex|failedDetail|newPermissions|addsModule|noSource|noManifestAddress|previous)\b/);
    expect(src).not.toMatch(/appPlugins\.compat\.needs/);
    for (const cat of [en, tr] as Array<{ appPlugins: { update: Record<string, unknown>; compat: Record<string, unknown> } }>) {
      for (const k of ['needsNewerFilex', 'failedDetail', 'newPermissions', 'addsModule', 'noSource', 'noManifestAddress', 'previous']) {
        expect(cat.appPlugins.update[k], k).toBeUndefined();
      }
      expect(cat.appPlugins.compat.needs).toBeUndefined();
    }
    expect(updateView(pack, tOf('tr'), 'tr').lines.at(-1)).toBe(pack.previous!.message);
  });

  it('⚠⚠ nothing updates itself: no "updated automatically", no switch said to be off', () => {
    const old = { ...pack, auto_update: false, update: { status: 'current', auto: { from: '0.9.0', to: '1.0.0', at: '2026-09-26T09:30:00Z' } } } as unknown as AppPlugin;
    const v = updateView(old, tOf('en'), 'en');
    expect(v.badges.map((b) => b.tone)).not.toContain('emerald');
    expect(v.lines.join(' ')).not.toMatch(/automatic/i);
    expect(v.lines).toContain(en.appPlugins.update.upToDate);
  });

  it('an app with no source says so in the server’s line, and is never offered an update', () => {
    // Which line (from a file, or from an address filex did not keep) is the
    // server's decision too (handlers appUpdateSaid).
    const said = 'Dosyadan kuruldu: güncellemelerin denetleneceği bir kaynak yok';
    const upload = { ...sign, source: 'upload', update_source: undefined, update: undefined, update_said: said } as AppPlugin;
    const v = updateView(upload, tOf('tr'), 'tr');
    expect(v.badges).toEqual([]);
    expect(v.lines).toEqual([said]);
    expect(v.reviewable).toBe(false);
  });

  // The server says the stored refusal in the reader's language when the list
  // is read (handlers sayStatus, 0.55): `message` is that sentence and the
  // English is `detail`. RED before: the list built the sentence itself from
  // the code (lib/appPluginRefusal.ts) and the server's English was dropped.
  it('a failed automatic update (a row filex 0.47 wrote) says the server’s sentence', () => {
    const said = 'Modül kendini manifestten farklı tarif ediyor - derleme ile manifest birbirine ait değil.';
    const failed = {
      ...sign,
      update: {
        status: 'failed',
        version: '1.3.0',
        refusal: { error: 'describe_mismatch', message: said, detail: 'describe: version 1.2.0 != manifest 1.3.0' },
      },
      update_said: '1.3.0 denendi ve geri alındı; 1.2.0 çalışmaya devam ediyor',
    } as AppPlugin;
    const v = updateView(failed, tOf('tr'), 'tr');
    expect(v.badges[0].label).toBe(tr.appPlugins.update.failed);
    expect(v.badges[0].title).toBe(said);
    expect(v.lines).toEqual(['1.3.0 denendi ve geri alındı; 1.2.0 çalışmaya devam ediyor']);
    expect(v.reviewable).toBe(true);
  });

  it('a source that could not be read says why in the server’s sentence, never its English detail', () => {
    const said = 'The server could not reach BRF-Tech/filex-sign. Check that it can reach the internet (or that host), then try again.';
    const unread = {
      ...sign,
      update: {
        status: 'check_failed',
        refusal: { error: 'fetch_failed', reason: 'unreachable', where: 'BRF-Tech/filex-sign', message: said, detail: 'dial tcp: i/o timeout' },
      },
      update_said: said,
    } as AppPlugin;
    const v = updateView(unread, tOf('en'), 'en');
    expect(v.badges[0].label).toBe(en.appPlugins.update.checkFailed);
    expect(v.lines[0]).toBe(said);
    expect(v.lines.join(' ')).not.toContain('i/o timeout');
    expect(v.reviewable).toBe(false);
  });

  it('newer versions that need a newer filex are said, and nothing is offered', () => {
    const ahead = {
      ...sign,
      update: { status: 'incompatible', version: '2.0.0', requires: '>=0.48.0' },
      update_said: '2.0.0 needs filex >=0.48.0',
    } as AppPlugin;
    const v = updateView(ahead, tOf('en'), 'en');
    expect(v.badges).toEqual([]);
    expect(v.lines).toEqual(['2.0.0 needs filex >=0.48.0']);
    expect(v.reviewable).toBe(false);
  });

  it('a newer version that asks for nothing more still waits for the administrator, and is offered', () => {
    const waiting = { ...sign, update: { status: 'available', version: '1.2.1' } } as AppPlugin;
    const v = updateView(waiting, tOf('en'), 'en');
    expect(v.badges.map((b) => [b.label, b.tone])).toEqual([[en.appPlugins.update.available, 'sky']]);
    expect(v.lines).toEqual(['1.2.0 → 1.2.1']);
    expect(v.reviewable).toBe(true);
  });
});
