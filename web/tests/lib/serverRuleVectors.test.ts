// The clients' half of the rule-mirror drift test (filex #211, audit B20).
//
// Some rules cannot travel as a value: the client applies them while a person
// types or picks (a theme's slug, an accent colour, a ZIP password, which
// app action a selection may run, which field a form shows). Each is written
// on both sides, and nothing used to hold the two together - a rule changed
// in one place drifted silently from the other. The cases live in ONE file,
// backend/internal/api/handlers/testdata/rule-mirrors.json; the Go tests
// check the server against it (rule_vectors_internal_test.go, and onlyoffice's
// for the formats saved beside a file), and this file checks the clients.
//
// The values the server CAN send are not here: they come from
// /api/files/capabilities (`edit_kinds`, `limits`, lib/serverRules), and the
// copies that held them are gone.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { THEME_KEY_RE } from '@/lib/themeTokens';
import { appliesMatches, type AppliesItem } from '@brftech/filex-core/src/lib/pluginApplies';
import { conditionMet } from '@brftech/filex-core/src/lib/surfaceConditions';
import { pluginActionNeed } from '@brftech/filex-core/src/lib/pluginMenu';
import { UI_SAVE_CHUNK } from '@brftech/filex-core/src/composables/useFileApi';
import { isOfficeExt, isTextEditable, serverLimit } from '@brftech/filex-core/src/lib/serverRules';
import type { PluginApplies, PluginCondition } from '@brftech/filex-core/src/types/Plugins';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8');

interface RegexVectors {
  pattern: string;
  ok: string[];
  bad: string[];
}
interface Vectors {
  edit_kinds: { office: string[]; text: string[]; text_names: string[] };
  limits: Record<string, number>;
  theme_key: RegexVectors;
  accent_hex: RegexVectors;
  store_token: RegexVectors;
  zip_password: { printable_from: number; printable_to: number; ok: string[]; bad: string[] };
  beside_formats: string[];
  ui_save_chunk_bytes: number;
  applies: Array<{ rule: PluginApplies; items: AppliesItem[]; match: boolean }>;
  action_need: Array<{ min_role: string; output_mode: string; need: string }>;
  conditions: Array<{ cond: PluginCondition | null; values: Record<string, unknown>; met: boolean }>;
}

const V = JSON.parse(read('backend/internal/api/handlers/testdata/rule-mirrors.json')) as Vectors;

describe('patterns the server holds a value to', () => {
  it('a theme’s key (web lib/themeTokens ↔ handlers/themes.go themeKeyRe)', () => {
    expect(THEME_KEY_RE.source).toBe(V.theme_key.pattern);
    for (const s of V.theme_key.ok) expect(THEME_KEY_RE.test(s), s).toBe(true);
    for (const s of V.theme_key.bad) expect(THEME_KEY_RE.test(s), s).toBe(false);
  });

  it('the accent colour (Branding.vue ↔ handlers/branding.go brandingAccentRe)', () => {
    const src = read('web/src/views/Branding.vue');
    expect(src, 'Branding.vue no longer checks the accent with the server’s pattern').toContain(`/${V.accent_hex.pattern}/`);
    const re = new RegExp(V.accent_hex.pattern);
    for (const s of V.accent_hex.ok) expect(re.test(s), s).toBe(true);
    for (const s of V.accent_hex.bad) expect(re.test(s), s).toBe(false);
  });

  it('a store install token (web lib/storeLink ↔ appstore/client.go tokenRe)', () => {
    const src = read('web/src/lib/storeLink.ts');
    expect(src).toContain(`const TOKEN_RE = /${V.store_token.pattern}/;`);
  });

  it('a ZIP password: printable ASCII (ArchiveCreateModal ↔ archivecli.CheckPassword)', () => {
    const bs = String.fromCharCode(92);
    const hex = (n: number) => n.toString(16).padStart(2, '0');
    const range = `[${bs}x${hex(V.zip_password.printable_from)}-${bs}x${hex(V.zip_password.printable_to)}]`;
    const src = read('packages/core/src/modals/ArchiveCreateModal.vue');
    expect(src, `ArchiveCreateModal no longer holds a ZIP password to ${range}`).toContain(range);
    const re = new RegExp(`^${range}*$`);
    for (const s of V.zip_password.ok) expect(re.test(s), s).toBe(true);
    for (const s of V.zip_password.bad) expect(re.test(s), s).toBe(false);
  });
});

describe('what an app action and a surface field decide', () => {
  it('applies: the selection an action is offered on (lib/pluginApplies ↔ wasmplugin.Matches)', () => {
    V.applies.forEach((c, i) => expect(appliesMatches(c.rule, c.items), `applies case ${i}`).toBe(c.match));
  });

  it('the level an action needs (lib/pluginMenu pluginActionNeed ↔ handlers pluginACLNeed)', () => {
    V.action_need.forEach((c, i) =>
      expect(pluginActionNeed({ min_role: c.min_role || undefined, output_mode: c.output_mode }), `action case ${i}`).toBe(
        c.need,
      ),
    );
  });

  it('a field’s condition (lib/surfaceConditions conditionMet ↔ handlers surfaceConditionMet)', () => {
    V.conditions.forEach((c, i) => expect(conditionMet(c.cond, c.values), `condition case ${i}`).toBe(c.met));
  });
});

describe('numbers and lists kept for a server that does not send them', () => {
  it('the chunk an interface’s save is sent in (useFileApi UI_SAVE_CHUNK ↔ app_ui_chunks.go uiChunkMax)', () => {
    expect(UI_SAVE_CHUNK).toBe(V.ui_save_chunk_bytes);
    expect(V.limits.app_ui_save_chunk_bytes).toBe(V.ui_save_chunk_bytes);
  });

  it('the formats the desktop app finds saved beside a file (openwith.ts BESIDE_FORMATS ↔ onlyoffice besideTypes)', () => {
    const src = read('desktop/src/openwith.ts');
    const m = src.match(/export const BESIDE_FORMATS: ReadonlySet<string> = new Set\(\[([^\]]*)\]\)/);
    expect(m, 'desktop/src/openwith.ts no longer declares BESIDE_FORMATS as a literal set').toBeTruthy();
    const list = [...m![1].matchAll(/'([a-z0-9]+)'/g)].map((x) => x[1]).sort();
    expect(list).toEqual([...V.beside_formats].sort());
  });

  // The desktop app registers itself with the OPERATING SYSTEM for these
  // types at install time (electron-builder.yml, the installer, the Store
  // manifest), so it cannot ask the server for them at run time - the one
  // copy the client keeps on purpose. Each must be one the server opens in
  // the document server: an office document by its rule, or `.csv`, which the
  // server saves as text and ONLYOFFICE edits as a spreadsheet (#151).
  it('the desktop’s “Open with filex” types are ones the server opens in ONLYOFFICE', () => {
    const src = read('desktop/src/openwith.ts');
    const m = src.match(/export const OFFICE_EXTENSIONS = \[([^\]]*)\] as const;/);
    expect(m, 'desktop/src/openwith.ts no longer declares OFFICE_EXTENSIONS as a literal list').toBeTruthy();
    const list = [...m![1].matchAll(/'([a-z0-9]+)'/g)].map((x) => x[1]);
    expect(list.length).toBeGreaterThan(0);
    for (const ext of list) {
      const opens = V.edit_kinds.office.includes(ext) || (ext === 'csv' && V.edit_kinds.text.includes('csv'));
      expect(opens, `.${ext} is not one the server opens in the document server`).toBe(true);
    }
  });

  // Every format saved beside a file is an office document by the server's
  // own rule: the two lists cannot disagree about what ONLYOFFICE writes.
  it('every format saved beside a file is an office document to the server', () => {
    for (const ext of V.beside_formats) expect(V.edit_kinds.office, ext).toContain(ext);
  });
});

describe('the seeded rules are the server’s (web/tests/setup.ts)', () => {
  it('reads the published lists and limits', () => {
    expect(isOfficeExt('docm')).toBe(true);
    expect(isTextEditable({ basename: 'Makefile', extension: '' })).toBe(true);
    expect(serverLimit('tag_max_runes')).toBe(V.limits.tag_max_runes);
  });
});
