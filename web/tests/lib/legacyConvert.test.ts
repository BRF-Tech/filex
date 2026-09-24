// One "Convert", never two: the legacy iframe converter against the Convert
// app (lib/serviceGate `legacyConvertGate`, lib/pluginMenu `convertAppOffered`).
//
// ⚠ QA, 2026-09-21: an administrator's menu on a .docx read "Dönüştür" (the
// legacy service, greyed "not set up") right above "Dönüştür…" (the app).
// The release plan's rule: the legacy entry appears only when the app is not
// available AND the legacy service is configured, with a note to the admin
// that it is being retired.
import { describe, expect, it } from 'vitest';

import { legacyConvertGate } from '@brftech/filex-core/src/lib/serviceGate';
import { CONVERT_APP, convertAppOffered } from '@brftech/filex-core/src/lib/pluginMenu';

const base = {
  unhealthyReason: 'not answering',
  adminNote: 'being retired',
};

describe('legacyConvertGate', () => {
  it('never offers the legacy converter beside the Convert app — in any state, to anybody', () => {
    for (const configured of [true, false]) {
      for (const healthy of [true, false]) {
        for (const callerAdmin of [true, false]) {
          expect(
            legacyConvertGate({ ...base, appOffered: true, configured, healthy, callerAdmin }),
            `configured=${configured} healthy=${healthy} admin=${callerAdmin}`,
          ).toEqual({ hidden: true });
        }
      }
    }
  });

  it('not configured: hidden even for an administrator — its replacement is the app', () => {
    expect(legacyConvertGate({ ...base, appOffered: false, configured: false, healthy: false, callerAdmin: true })).toEqual({
      hidden: true,
    });
  });

  it('configured but not answering: greyed with the reason for an admin, hidden for everybody else', () => {
    expect(legacyConvertGate({ ...base, appOffered: false, configured: true, healthy: false, callerAdmin: true })).toEqual({
      disabled: true,
      title: 'not answering',
    });
    expect(legacyConvertGate({ ...base, appOffered: false, configured: true, healthy: false, callerAdmin: false })).toEqual({
      hidden: true,
    });
  });

  it('configured and answering: offered, and an administrator reads that it is being retired', () => {
    expect(legacyConvertGate({ ...base, appOffered: false, configured: true, healthy: true, callerAdmin: true })).toEqual({
      title: 'being retired',
    });
    expect(legacyConvertGate({ ...base, appOffered: false, configured: true, healthy: true, callerAdmin: false })).toEqual({});
  });
});

describe('convertAppOffered', () => {
  it('is the Convert app’s own name, not any app that converts', () => {
    expect(CONVERT_APP).toBe('convert');
    expect(convertAppOffered([{ plugin: 'sign' }, { plugin: 'convert' }])).toBe(true);
    expect(convertAppOffered([{ plugin: 'sign' }])).toBe(false);
    expect(convertAppOffered([])).toBe(false);
  });
});

describe('every door to the legacy dialog reads the rule', () => {
  // FileExplorer.vue is too large to mount here; its wiring is read from the
  // source, and the shape of each door is pinned (not just a name — a name
  // survives in a type annotation or a comment while the door is open).
  it('the menu entry, the dialog and the shortcut all go through legacyConvert', async () => {
    const { readFileSync } = await import('node:fs');
    const path = await import('node:path');
    const src = readFileSync(
      path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'),
      'utf8',
    );
    expect(src).toMatch(/const convertGate = legacyConvert\.value;/);
    expect(src).toMatch(/v-if="showConvert && convertTarget && legacyConvertUrl"/);
    expect(src).toMatch(/function openConvert\(n: FileNode\) \{\s*\/\/[^\n]*\n\s*if \(!legacyConvertUrl\.value\) return;/);
    // effectiveConvertUrl is the SERVICE's health, read only by the rule.
    const uses = src.split('\n').filter((l) => /effectiveConvertUrl\b/.test(l) && !/^\s*(\/\/|\*|\/\*)/.test(l));
    expect(uses.map((l) => l.trim())).toEqual([
      'const effectiveConvertUrl = computed<string | null>(() => {',
      'healthy: !!effectiveConvertUrl.value,',
      'legacyConvert.value.hidden || legacyConvert.value.disabled ? null : effectiveConvertUrl.value,',
    ]);
  });
});
