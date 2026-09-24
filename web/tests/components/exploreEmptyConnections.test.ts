// baglan:b1 — the way IN to "How to connect" for a person with NO storage.
//
// The guide itself is the package's (`ConnectionsPanel`) and is measured in
// e2e/tests/25-connections.spec.ts; this file is about the door, and about the
// one screen that had none.
//
// `sidenav-connect` is the only remaining door to that guide, and it lives in
// the explorer's navigation panel — which is not mounted at all when
// `roots.length === 0`. So a brand-new account, and one whose grant was
// revoked, landed on the bare "nothing has been shared with you" screen, were
// told to ask an administrator, and could not even READ how to connect. That
// is not a missing page: FTPS, WebDAV and `filex mount` each instruct the
// reader to sign in with an API token, and the only surface that mints one is
// the panel behind that door. The gap was closed for the has-storage case on
// 2026-08-17 (see the ⚠⚠ block in that spec) and re-opened underneath it for
// the zero-storage one.
//
// `web/tests/components/sideNavMyShares.test.ts` measures the SAME mechanism
// for "Paylaştıklarım" — its last describe is the model this file follows —
// and the fix is deliberately the same one rather than a variant: a second row
// spliced into `emptyStateActions`, acted on by the one `runAccountAction`.
//
// What has to stay true:
//
//   · the empty screen's menu — and ONLY that menu — offers the row;
//   · the row is built from `headerActions` rather than hand-written, so the
//     two lists cannot start disagreeing about Sign out;
//   · it is named in the viewer's language, from the string the screen it
//     opens already uses, in BOTH catalogues and with real Turkish letters;
//   · its glyph exists in the shared icon set and is NOT the chain "My shares"
//     draws one row above it;
//   · pressing it reaches the guide. ⚠ And that last one is the assertion with
//     teeth: the obvious implementation — `router.push({ name: 'connections' })`,
//     exactly what the My shares row does — is WRONG here, because that route
//     lives inside the AdminLayout block (`meta.requiresAdmin`) and the guard
//     bounces a non-admin straight back to Home. The person this row exists for
//     is a non-admin. So the row raises a flag and this page mounts the
//     package's own panel — the same one the explorer's own door opens.
//
// ⚠ Source scans, for the same reason sideNavMyShares.test.ts gives: mounting
// Explore.vue means mounting FileExplorer, the storage discovery it runs on
// mount and the whole router, and the thing under test is which of two lists
// one `<AccountMenu>` is handed. The menu itself IS mounted below, fed the row
// parsed out of the page — so a deleted row, a wrong i18n key or an icon
// nothing draws all turn this file red rather than only the e2e.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { readFileSync } from 'node:fs';
import path from 'node:path';

// ⚠ `/src/…`, not the package root: the root resolves to `dist/`, which is a
// build artefact that may predate this change by minutes. The contract under
// test is "the shared set draws this key", and that lives in the source.
import { actionIconSvg } from '@brftech/filex-core/src/lib/actionIcons';

import AccountMenu, { type AccountAction } from '@/components/AccountMenu.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const SRC = path.resolve(__dirname, '../../src');
const explore = readFileSync(path.join(SRC, 'views/Explore.vue'), 'utf8');

/** The declaration of the empty screen's list, cut at the handler below it. */
function emptyStateDecl(): string {
  const from = explore.indexOf('const emptyStateActions');
  const to = explore.indexOf('function runAccountAction');
  expect(from, 'emptyStateActions is gone from Explore.vue').toBeGreaterThan(-1);
  expect(to).toBeGreaterThan(from);
  return explore.slice(from, to);
}

/**
 * The `connections` row exactly as the page declares it.
 *
 * ⚠ Parsed rather than re-typed: a row this file spelled out itself would keep
 * passing after somebody deleted the real one, which is the failure mode this
 * whole file exists to avoid (a previous pass here shipped a test that was
 * green with and without the fix).
 */
function connectionsRow(): { key: string; labelKey: string; icon: string } {
  const m = emptyStateDecl().match(
    /\{\s*key:\s*'(connections)',\s*label:\s*t\('([\w.]+)'\),\s*icon:\s*'([\w-]+)'\s*\}/,
  );
  expect(
    m,
    'no `connections` row in emptyStateActions — the zero-storage screen has no door to the guide',
  ).not.toBeNull();
  return { key: m![1], labelKey: m![2], icon: m![3] };
}

/** Resolve a dotted i18n key against a catalogue, or `undefined`. */
function lookup(cat: Record<string, unknown>, dotted: string): string | undefined {
  const v = dotted.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown>)?.[k], cat);
  return typeof v === 'string' ? v : undefined;
}

describe('Explore — the empty screen can reach the connections guide', () => {
  it('offers the row, and only on the screen with no navigation panel', () => {
    const decl = emptyStateDecl();
    // Built FROM the shared definition, like its neighbour: a hand-written
    // second list is how the two copies start to disagree about Sign out.
    expect(decl).toMatch(/\[\.\.\.headerActions\.value\]/);
    expect(decl).toContain("key: 'connections'");

    // ⚠ And NOT in the header's own list. The explorer draws `sidenav-connect`
    // four pixels away; two doors a glyph apart in one header is the duplicate
    // gorunum:v2-topbar removed, and re-adding it there would be the relapse.
    const header = explore.slice(
      explore.indexOf('const headerActions'),
      explore.indexOf('const emptyStateActions'),
    );
    expect(header).not.toContain("key: 'connections'");
  });

  it('is named in the viewer’s language, in both catalogues, in real Turkish', () => {
    const { labelKey } = connectionsRow();
    const enLabel = lookup(en as Record<string, unknown>, labelKey);
    const trLabel = lookup(tr as Record<string, unknown>, labelKey);
    expect(enLabel, `${labelKey} missing from web/src/locales/en.json`).toBeTruthy();
    expect(trLabel, `${labelKey} missing from web/src/locales/tr.json`).toBeTruthy();
    // ⚠ ASCII-folded Turkish is a defect in this repo, not a cosmetic choice:
    // "Baglantilar" is not a word. The label is the screen's own name, so if
    // the folded spelling ever lands it lands on every surface at once.
    expect(trLabel).not.toMatch(/Baglant/i);
    expect(trLabel).toMatch(/[ıİşŞğĞüÜöÖçÇ]/);
  });

  it('wears a glyph of its own, not the chain "My shares" draws above it', () => {
    const { icon } = connectionsRow();
    // The shared set really draws it — an unknown key is contractually '' and
    // renders as an empty box, i.e. a row that looks unfinished.
    expect(actionIconSvg(icon), `actionIcons draws nothing for '${icon}'`).not.toBe('');
    // Two adjacent rows wearing one mark is the misreading the icon set exists
    // to stop; `link` belongs to the row above, where what leaves IS a URL.
    expect(icon).not.toBe('link');
  });

  it('reaches the guide instead of an admin-only route', () => {
    const handler = explore.slice(
      explore.indexOf('function runAccountAction'),
      explore.indexOf('const remountKey'),
    );
    expect(handler).toMatch(/key === 'connections'/);
    // ⚠⚠ The trap, written down: `{ name: 'connections' }` is a child of the
    // AdminLayout record, whose `meta.requiresAdmin` the router guard enforces
    // by sending a non-admin to Home. Pushing it from this row would do
    // nothing visible for the exact person the row is for.
    expect(
      handler,
      "the connections row must not push the admin-only 'connections' route",
    ).not.toMatch(/key === 'connections'\)[^;]*router\.push/);
    expect(handler).toMatch(/key === 'connections'\) showConnections\.value = true/);
  });

  it('mounts the package’s own panel, not a copy of it', () => {
    // The overlay is drawn at page level, guarded on the flag the row sets.
    expect(explore).toMatch(/v-if="showConnections"/);
    // ⚠ `lastIndexOf`: the file NAMES the component in prose near the top
    // (the baglan:b1 note), and `indexOf` walks straight into that comment and
    // then slices to the first `/>` a thousand lines away.
    const from = explore.lastIndexOf('<ConnectionsPanel');
    expect(from, 'the empty screen draws no ConnectionsPanel').toBeGreaterThan(-1);
    const tag = explore.slice(from, explore.indexOf('/>', from));
    // ⚠ Same props the explorer's own door passes (FileExplorer.vue), so both
    // ways in open the same screen.
    expect(tag).toContain('closable');
    // ⚠⚠ And NOT `initial-tab`: the panel lost its Storages tab in v0.43.0, so
    // there is no half to choose. A prop left behind here would be read as a
    // working choice by the next person to touch this page.
    expect(tag).not.toContain('initial-tab');
    expect(tag).not.toContain('@changed');
    // Fed the page's shared config — not a second object holding the same
    // endpoint, which is how one of them keeps the old one after a move.
    expect(tag).toContain(':config="panelConfig"');
    expect(explore).toMatch(/const panelConfig = computed/);
    expect(explore).toMatch(/\.\.\.panelConfig\.value/);
    // And imported from the package, never re-implemented here.
    expect(explore).toMatch(/import \{[^}]*ConnectionsPanel[^}]*\} from '@brftech\/filex-core'/);
  });

  it('draws as a real row, with the testid the e2e clicks', async () => {
    setActivePinia(createPinia());
    const { key, labelKey, icon } = connectionsRow();
    const label = lookup(tr as Record<string, unknown>, labelKey) as string;
    const actions: AccountAction[] = [
      { key: 'settings', label: 'Ayarlar', icon: 'account' },
      { key, label, icon },
    ];
    const w = mount(AccountMenu, {
      attachTo: document.body,
      props: { actions, locale: 'tr', fallbackLabel: 'Hesap' },
    });
    await w.find('[data-testid="explore-account"]').trigger('click');
    await w.vm.$nextTick();
    // ⚠ Queried off <body>: the panel is TELEPORTED there (the explorer's root
    // carries overflow:hidden), so a wrapper-scoped find would report the row
    // missing even when it is on screen.
    const row = document.body.querySelector(`[data-testid="explore-${key}"]`);
    expect(row, 'the connections row is not drawn in the account menu').not.toBeNull();
    expect(row!.textContent?.trim()).toContain(label);
    w.unmount();
  });
});
