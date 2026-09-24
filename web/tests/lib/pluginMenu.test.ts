// The file menu's plugin block, as FileExplorer.selectionActionList splices
// it in: present for a selection an action accepts, absent in the trash,
// inside an encrypted folder and on a storage mount row.
import { describe, expect, it } from 'vitest';

import {
  pluginMenuRows,
  pluginActionKey,
  pluginActionNeed,
  pluginActionWrites,
  isPluginActionKey,
} from '@brftech/filex-core/src/lib/pluginMenu';
import { actionIconSvg } from '@brftech/filex-core/src/lib/actionIcons';
import type { PluginActionRow } from '@brftech/filex-core/src/types/Plugins';

const sign: PluginActionRow = {
  plugin: 'sign',
  id: 'sign',
  key: 'plugin:sign/sign',
  label: { en: 'Sign…', tr: 'İmzala…' },
  icon: 'sign',
  applies: { kind: 'file', ext: ['pdf'] },
};
const shred: PluginActionRow = {
  plugin: 'shred',
  id: 'wipe',
  label: { en: 'Shred' },
  icon: 'delete',
  danger: true,
  applies: { kind: 'file' },
};
const pdf = { type: 'file', extension: 'pdf', mime_type: 'application/pdf', basename: 'nda.pdf' };
const hasIcon = (name: string) => actionIconSvg(name) !== '';

/** The action rows only — the dividers are asserted on their own below. */
const actionsOnly = <T extends { divider?: boolean }>(rows: T[]) => rows.filter((r) => !r.divider);

describe('pluginMenuRows', () => {
  it('starts with the sep-plugins divider and names each row by its key', () => {
    const rows = pluginMenuRows([sign, shred], [pdf], { locale: 'tr', hasIcon });
    expect(rows[0].key).toBe('sep-plugins');
    expect(rows[0].divider).toBe(true);
    expect(actionsOnly(rows).map((r) => r.key)).toEqual(['plugin:sign/sign', 'plugin:shred/wipe']);
  });

  it('draws a line between EVERY app’s actions, not one block for all of them', () => {
    /* The owner, 2026-09-21: "dönüştür ve imzalama pluginlerinin menüleri —
       yani her plugin'in menüleri — arasına çizgi çekelim." Two apps' rows in
       one undivided run read as one app's vocabulary. */
    const sign2: PluginActionRow = { ...sign, id: 'request', key: 'plugin:sign/request', label: { en: 'Request signatures' } };
    const rows = pluginMenuRows([sign, sign2, shred], [pdf], { locale: 'en', hasIcon });
    expect(rows.map((r) => r.key)).toEqual([
      'sep-plugins',
      'plugin:sign/sign',
      'plugin:sign/request',
      'sep-plugin:shred',
      'plugin:shred/wipe',
    ]);
    expect(rows.filter((r) => r.divider)).toHaveLength(2);
  });

  it('keeps an app’s own rows together, in the order the server listed them', () => {
    /* Grouped by the app's FIRST row: an interleaved answer is pulled apart
       into groups, and inside each group the manifest's order stands. */
    const sign2: PluginActionRow = { ...sign, id: 'request', key: 'plugin:sign/request', label: { en: 'Request' } };
    const rows = pluginMenuRows([sign, shred, sign2], [pdf], { locale: 'en', hasIcon });
    expect(rows.map((r) => r.key)).toEqual([
      'sep-plugins',
      'plugin:sign/sign',
      'plugin:sign/request',
      'sep-plugin:shred',
      'plugin:shred/wipe',
    ]);
  });

  it('one app is one group: a single divider, as before', () => {
    const rows = pluginMenuRows([sign], [pdf], { locale: 'en', hasIcon });
    expect(rows.map((r) => r.key)).toEqual(['sep-plugins', 'plugin:sign/sign']);
  });

  it('labels in the viewer language, with the en fallback', () => {
    const rows = actionsOnly(pluginMenuRows([sign, shred], [pdf], { locale: 'tr', hasIcon }));
    expect(rows[0].label).toBe('İmzala…');
    expect(rows[1].label).toBe('Shred');
  });

  it('uses the manifest icon when this set draws it, the puzzle piece otherwise', () => {
    // ⚠ `sign` is drawn since 2026-09-21 (a tester: the Signatures app wore
    // the generic piece everywhere); an icon the set does not know still
    // falls back to the piece.
    const quill: PluginActionRow = { ...shred, plugin: 'quill', id: 'ink', icon: 'no-such-glyph', danger: false };
    const rows = actionsOnly(pluginMenuRows([sign, shred, quill], [pdf], { locale: 'en', hasIcon }));
    expect(rows[0].icon).toBe('sign');
    expect(rows[1].icon).toBe('delete');
    expect(rows[2].icon).toBe('plugin');
    expect(actionIconSvg('plugin')).toContain('<svg');
  });

  it('honours the danger flag', () => {
    const rows = actionsOnly(pluginMenuRows([sign, shred], [pdf], { locale: 'en', hasIcon }));
    expect(rows[0].danger).toBe(false);
    expect(rows[1].danger).toBe(true);
  });

  it('is empty when no action accepts the selection', () => {
    const txt = { type: 'file', extension: 'txt', mime_type: 'text/plain', basename: 'a.txt' };
    expect(pluginMenuRows([sign], [txt], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([sign], [], { locale: 'en' })).toEqual([]);
  });

  it('is empty in the trash and inside an encrypted folder', () => {
    expect(pluginMenuRows([sign], [pdf], { locale: 'en', trash: true })).toEqual([]);
    expect(pluginMenuRows([sign], [pdf], { locale: 'en', e2e: true })).toEqual([]);
  });

  it('on a read-only storage leaves out every action that WRITES its result', () => {
    /* QA, 2026-09-21: on a read-only drive "Dönüştür…", "İmzala…" and "İmza
       iste…" were all offered; the first answered with 409 read_only and a
       toast gone in 2.5 s. An action that can only be refused is not offered
       — the built-in verbs follow the same rule. */
    const convert: PluginActionRow = { plugin: 'convert', id: 'convert', label: { en: 'Convert…' }, applies: { kind: 'file' }, output_mode: 'sibling' };
    const fill: PluginActionRow = { plugin: 'sign', id: 'fill', label: { en: 'Fill' }, applies: { kind: 'file' }, output_mode: 'version' };
    const verify: PluginActionRow = { plugin: 'sign', id: 'verify', label: { en: 'Verify' }, applies: { kind: 'file' }, output_mode: 'none' };
    const all = [convert, fill, verify];
    expect(actionsOnly(pluginMenuRows(all, [pdf], { locale: 'en' })).map((r) => r.key)).toEqual([
      'plugin:convert/convert',
      'plugin:sign/fill',
      'plugin:sign/verify',
    ]);
    const ro = pluginMenuRows(all, [pdf], { locale: 'en', readOnly: true });
    // Only the reader is left — and the convert app's group went with its
    // only row, divider and all.
    expect(ro.map((r) => r.key)).toEqual(['sep-plugins', 'plugin:sign/verify']);
    expect(pluginMenuRows([convert], [pdf], { locale: 'en', readOnly: true })).toEqual([]);
  });

  it("leaves out what the caller's level on the file cannot run", () => {
    /* v0.43.0 wave 2 (2026-09-22): Ayşe, granted viewer on an RBAC storage,
       was offered "Dönüştür…", "İmzala…" and "İmza iste…" there, and each
       answered with 403 "insufficient permission". The server's rule is
       pluginACLNeed; the menu mirrors it. */
    const convert: PluginActionRow = { plugin: 'convert', id: 'convert', label: { en: 'Convert…' }, applies: { kind: 'file' }, output_mode: 'sibling' };
    const request: PluginActionRow = { plugin: 'sign', id: 'request', label: { en: 'Request signatures…' }, applies: { kind: 'file' }, output_mode: 'none', min_role: 'editor' };
    const verify: PluginActionRow = { plugin: 'sign', id: 'verify', label: { en: 'Verify' }, applies: { kind: 'file' }, output_mode: 'none', min_role: 'viewer' };
    const purge: PluginActionRow = { plugin: 'vault', id: 'purge', label: { en: 'Purge' }, applies: { kind: 'file' }, min_role: 'owner' };
    const all = [convert, request, verify, purge];
    const keys = (perm: string | undefined) =>
      actionsOnly(pluginMenuRows(all, [pdf], { locale: 'en', permOf: () => perm })).map((r) => r.key);

    expect(keys('viewer')).toEqual(['plugin:sign/verify']);
    expect(keys('editor')).toEqual(['plugin:convert/convert', 'plugin:sign/request', 'plugin:sign/verify']);
    expect(keys('owner')).toEqual(['plugin:convert/convert', 'plugin:sign/request', 'plugin:sign/verify', 'plugin:vault/purge']);
    expect(keys('none')).toEqual([]);
    // No level (no ACL on that storage) and an unknown answer stay ungated:
    // the server enforces, the menu only shapes.
    expect(keys(undefined)).toHaveLength(4);
    expect(keys('')).toHaveLength(4);

    // A multi-selection is as weak as its weakest row.
    const two = pluginMenuRows([{ ...convert, applies: { kind: 'file', multi: true } }], [pdf, { ...pdf, basename: 'b.pdf' }], {
      locale: 'en',
      permOf: (n) => (n.basename === 'b.pdf' ? 'viewer' : 'owner'),
    });
    expect(two).toEqual([]);
  });

  it('does not hold an app to the level its OWN lock put on the file', () => {
    /* A locked file is listed at `viewer` for everyone (the lock speaking,
       not the grant) — "Sign / Fill" on a document the signing app froze
       must stay in its signers' menu; another app's writer must not. */
    const fill: PluginActionRow = { plugin: 'sign', id: 'fill', label: { en: 'Sign / Fill' }, applies: { kind: 'file' }, output_mode: 'version', min_role: 'editor' };
    const convert: PluginActionRow = { plugin: 'convert', id: 'convert', label: { en: 'Convert…' }, applies: { kind: 'file' }, output_mode: 'sibling' };
    const frozen = { ...pdf, lock: { plugin: 'sign' } };
    const keys = actionsOnly(pluginMenuRows([fill, convert], [frozen], { locale: 'en', permOf: () => 'viewer' })).map((r) => r.key);
    expect(keys).toEqual(['plugin:sign/fill']);
  });

  it('needs = the server pluginACLNeed: viewer to read, editor to write back, the min_role floor', () => {
    expect(pluginActionNeed({ output_mode: 'none' })).toBe('viewer');
    expect(pluginActionNeed({})).toBe('viewer');
    expect(pluginActionNeed({ output_mode: 'sibling' })).toBe('editor');
    expect(pluginActionNeed({ output_mode: 'version' })).toBe('editor');
    expect(pluginActionNeed({ output_mode: 'none', min_role: 'editor' })).toBe('editor');
    expect(pluginActionNeed({ output_mode: 'sibling', min_role: 'viewer' })).toBe('editor');
    expect(pluginActionNeed({ output_mode: 'none', min_role: 'owner' })).toBe('owner');
  });

  it('a flow that ends in a write (applies.writable) is not offered on a read-only storage', () => {
    /* "İmza iste…" writes nothing now, but the signed document at the end:
       on a read-only storage its first screen could only refuse. */
    const request: PluginActionRow = { plugin: 'sign', id: 'request', label: { en: 'Request signatures…' }, applies: { kind: 'file', writable: true }, output_mode: 'none', min_role: 'editor' };
    const verify: PluginActionRow = { plugin: 'sign', id: 'verify', label: { en: 'Verify' }, applies: { kind: 'file' }, output_mode: 'none' };
    expect(actionsOnly(pluginMenuRows([request, verify], [pdf], { locale: 'en', readOnly: true })).map((r) => r.key)).toEqual([
      'plugin:sign/verify',
    ]);
    expect(actionsOnly(pluginMenuRows([request, verify], [pdf], { locale: 'en' }))).toHaveLength(2);
    expect(pluginActionNeed(request)).toBe('editor');
  });

  it('an action whose result may go elsewhere IS offered on a read-only storage', () => {
    /* Burak, 2026-09-22: "Dönüştür…" on a read-only storage — the wizard asks
       where the result should go. */
    const convert: PluginActionRow = { plugin: 'convert', id: 'convert', label: { en: 'Convert…' }, applies: { kind: 'file' }, output_mode: 'sibling', output_elsewhere: true };
    expect(actionsOnly(pluginMenuRows([convert], [pdf], { locale: 'en', readOnly: true })).map((r) => r.key)).toEqual([
      'plugin:convert/convert',
    ]);
    const without = { ...convert, output_elsewhere: false };
    expect(pluginMenuRows([without], [pdf], { locale: 'en', readOnly: true })).toEqual([]);
  });

  it('draws an action the server lacks a piece for as a greyed row saying what is missing', () => {
    /* The owner's rule: not configured → disabled with the reason for
       administrators, hidden for everybody else. The server sends `gated`
       to administrators only; the menu draws it only when told how to say
       the reason. */
    const sign: PluginActionRow = {
      plugin: 'sign',
      id: 'sign',
      label: { en: 'Sign…' },
      applies: { kind: 'file', ext: ['pdf'] },
      output_mode: 'sibling',
      gated: [{ ext: ['docx', 'odt'], needs: { kind: 'engine', id: 'libreoffice', name: 'LibreOffice' } }],
    };
    const docx = { type: 'file', extension: 'docx', basename: 'letter.docx' };
    const needWords = (n: { name: string }) => `${n.name} is not installed`;
    const rows = actionsOnly(pluginMenuRows([sign], [docx], { locale: 'en', needWords }));
    expect(rows).toEqual([
      expect.objectContaining({ key: 'plugin:sign/sign', disabled: true, title: 'LibreOffice is not installed' }),
    ]);
    // A PDF is simply offered; a type no gate names is not drawn at all.
    expect(actionsOnly(pluginMenuRows([sign], [pdf], { locale: 'en', needWords }))[0].disabled).toBeUndefined();
    expect(pluginMenuRows([sign], [{ type: 'file', extension: 'zip', basename: 'a.zip' }], { locale: 'en', needWords })).toEqual([]);
    // Nobody who is not told how to say it (not an administrator) sees it.
    expect(pluginMenuRows([sign], [docx], { locale: 'en' })).toEqual([]);
    // The level and read-only rules still apply to a greyed row.
    expect(pluginMenuRows([sign], [docx], { locale: 'en', needWords, readOnly: true })).toEqual([]);
  });

  it('writes = the output the server refuses on a read-only storage, nothing else', () => {
    // handlers/app_plugins.go `authorise` → `jobOutputMode`: sibling and
    // version write, none and an unknown/absent mode do not.
    expect(pluginActionWrites({ output_mode: 'sibling' })).toBe(true);
    expect(pluginActionWrites({ output_mode: 'version' })).toBe(true);
    expect(pluginActionWrites({ output_mode: 'none' })).toBe(false);
    expect(pluginActionWrites({})).toBe(false);
    expect(pluginActionWrites({ output_mode: 'none', applies: { writable: true } })).toBe(true);
  });

  it('is empty when the selection holds an encrypted folder or a storage row', () => {
    const encDir = { type: 'dir', basename: 'vault', e2e: true };
    const storage = { type: 'dir', basename: 'docs', mime_type: 'inode/storage' };
    const anyDir: PluginActionRow = { plugin: 'zip', id: 'dir', label: { en: 'Zip' }, applies: { kind: 'any', multi: true } };
    expect(pluginMenuRows([anyDir], [encDir], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([anyDir], [storage], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([anyDir], [{ type: 'dir', basename: 'plain' }], { locale: 'en' })).toHaveLength(2);
  });
});

describe('plugin action keys', () => {
  it('prefers the server key and builds plugin:<plugin>/<id> otherwise', () => {
    expect(pluginActionKey(sign)).toBe('plugin:sign/sign');
    expect(pluginActionKey(shred)).toBe('plugin:shred/wipe');
  });
  it('recognises the prefix', () => {
    expect(isPluginActionKey('plugin:sign/sign')).toBe(true);
    expect(isPluginActionKey('rename')).toBe(false);
  });
});
