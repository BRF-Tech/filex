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
import { AUTO_UPDATED_SHOWN_MS, updateRank, updateView } from '@/lib/appPluginUpdates';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
const check = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-update-check.json'), 'utf8'));
const [sign, pack] = check.plugins as AppPlugin[];

function tOf(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return i18n.global.t as unknown as (k: string, v?: Record<string, unknown>) => string;
}

/** The moment the fixture's last check ran — "now" for the week-long badge. */
const CHECKED = Date.parse('2026-09-26T09:30:00Z');

describe('updateView', () => {
  it('a newer version that asks for more: needs approval, the jump and what it adds, reviewable', () => {
    const v = updateView(sign, tOf('en'), 'en', CHECKED);
    expect(v.badges.map((b) => b.label)).toEqual([en.appPlugins.update.needsApproval]);
    expect(v.badges[0].tone).toBe('amber');
    expect(v.lines).toEqual(['1.2.0 → 1.3.0 · new: mail:send']);
    expect(v.reviewable).toBe(true);

    const trv = updateView(sign, tOf('tr'), 'tr', CHECKED);
    expect(trv.badges[0].label).toBe('Onay bekliyor');
    expect(trv.lines[0]).toContain('yeni: mail:send');
  });

  it('an app outside its own range for this filex is marked — and still says what its updates did', () => {
    const v = updateView(pack, tOf('en'), 'en', CHECKED);
    expect(v.badges.map((b) => [b.label, b.tone])).toEqual([
      [en.appPlugins.compat.bad, 'rose'],
      [en.appPlugins.update.updated, 'emerald'],
    ]);
    expect(v.lines[0]).toBe('Works with filex >=0.45.0 <0.47.0; this is 0.47.0. It keeps running.');
    expect(v.lines[1]).toMatch(/^from 0\.9\.0, /);
    expect(v.reviewable).toBe(false);
    expect(updateRank(pack)).toBeLessThan(updateRank(sign));
  });

  it('"Updated automatically" is a week-long note, not a label for ever', () => {
    const later = updateView(pack, tOf('en'), 'en', CHECKED + AUTO_UPDATED_SHOWN_MS + 1000);
    expect(later.badges.map((b) => b.label)).not.toContain(en.appPlugins.update.updated);
    expect(later.lines).toContain(en.appPlugins.update.upToDate);
  });

  it('an app with no source says so, and is never offered an update', () => {
    const upload = { ...sign, source: 'upload', update_source: undefined, update: undefined } as AppPlugin;
    const v = updateView(upload, tOf('tr'), 'tr');
    expect(v.badges).toEqual([]);
    expect(v.lines).toEqual([tr.appPlugins.update.noSource]);
    expect(v.reviewable).toBe(false);
  });

  it('an app installed from an address before its manifest address was kept says that, not "from a file"', () => {
    const old = { ...sign, source: 'url', update_source: undefined, update: undefined } as AppPlugin;
    expect(updateView(old, tOf('en'), 'en').lines).toEqual([en.appPlugins.update.noManifestAddress]);
  });

  it('a failed automatic update says the refusal in the reader’s words, not the server’s English', () => {
    const failed = {
      ...sign,
      update: {
        status: 'failed',
        version: '1.3.0',
        refusal: { error: 'describe_mismatch', message: 'describe: version 1.2.0 != manifest 1.3.0' },
      },
    } as AppPlugin;
    const v = updateView(failed, tOf('tr'), 'tr');
    expect(v.badges[0].label).toBe(tr.appPlugins.update.failed);
    expect(v.badges[0].title).toBe(tr.appPlugins.wizard.errors.describe_mismatch);
    expect(v.lines).toEqual(['1.3.0 denendi ve geri alındı; 1.2.0 çalışmaya devam ediyor']);
    expect(v.reviewable).toBe(true);
  });

  it('a source that could not be read says why with the wizard’s own sentence', () => {
    const unread = {
      ...sign,
      update: { status: 'check_failed', refusal: { error: 'fetch_failed', reason: 'unreachable', where: 'BRF-Tech/filex-sign' } },
    } as AppPlugin;
    const v = updateView(unread, tOf('en'), 'en');
    expect(v.badges[0].label).toBe(en.appPlugins.update.checkFailed);
    expect(v.lines[0]).toBe(en.appPlugins.wizard.errors.fetch.unreachable.replace('{where}', 'BRF-Tech/filex-sign'));
    expect(v.reviewable).toBe(false);
  });

  it('newer versions that need a newer filex are said, and nothing is offered', () => {
    const ahead = { ...sign, update: { status: 'incompatible', version: '2.0.0', requires: '>=0.48.0' } } as AppPlugin;
    const v = updateView(ahead, tOf('en'), 'en');
    expect(v.badges).toEqual([]);
    expect(v.lines).toEqual(['2.0.0 needs filex >=0.48.0']);
    expect(v.reviewable).toBe(false);
  });

  it('a version that only waits because automatic updates are off is offered', () => {
    const waiting = { ...sign, auto_update: false, update: { status: 'available', version: '1.2.1' } } as AppPlugin;
    const v = updateView(waiting, tOf('en'), 'en');
    expect(v.badges.map((b) => [b.label, b.tone])).toEqual([[en.appPlugins.update.available, 'sky']]);
    expect(v.lines, 'and it says why it waits').toEqual(['1.2.0 → 1.2.1', en.appPlugins.update.autoOff]);
    expect(v.reviewable).toBe(true);
  });
});
