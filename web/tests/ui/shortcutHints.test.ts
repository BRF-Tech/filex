/**
 * Every key a hint names must be the key that is actually bound.
 *
 * filex lets people remap shortcuts (`filex.shortcuts` in localStorage, edited
 * from the shortcut settings modal). A hint that spells a key out by hand is
 * therefore true only until someone changes that key — and then the product
 * tells the user, in its own voice, to press something that does nothing. That
 * is worse than showing no hint at all, and nothing else in the UI contradicts
 * it, so the user has no way to tell it is wrong.
 *
 * Found on 2026-09-12 while fixing #22 (the quick-look legend). The legend's
 * keys were hardcoded AND the overlay's own key handler compared against the
 * default key, so remapping quick-look onto `Q` gave you: Q opens the peek,
 * Space still closes it, and the pill says "Space". Three answers to one
 * question. The drive shell's search chip and two steps of the onboarding tour
 * had the same literal `Ctrl+K`.
 *
 * Two halves, both of which have to stay true every release:
 *   1. Behaviour — remap an action, and the surface that names it changes.
 *   2. Source    — no new hint is written with a key typed into it by hand.
 *
 * Keys that are genuinely NOT remappable may be named literally, but they have
 * to be declared in FIXED_KEY_HINTS below with the binding that proves it.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { nextTick } from 'vue';

import QuickLook from '@brftech/filex-core/src/components/QuickLook.vue';
import Toolbar from '@brftech/filex-core/src/components/Toolbar.vue';
import {
  effectiveCombo,
  resetAllShortcuts,
  setShortcutOverride,
  shortcutHint,
  SHORTCUT_ACTIONS,
} from '@brftech/filex-core/src/composables/useKeyboardShortcuts';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');

beforeEach(() => resetAllShortcuts());
afterEach(() => {
  resetAllShortcuts();
  document.body.innerHTML = '';
});

// ---------------------------------------------------------------------------
// 1. Behaviour: the surface follows the binding
// ---------------------------------------------------------------------------

/** Where the hint text of a mounted surface lives. */
function bodyText(): string {
  return document.body.textContent ?? '';
}

describe('quick-look legend', () => {
  const props = {
    open: true,
    locale: 'en' as const,
    file: { basename: 'a.txt', path: 'demo://a.txt', type: 'file', size: 1 } as never,
    previewUrl: (p: string) => p,
    downloadUrl: (p: string) => p,
  };

  it('names the bound keys, not the default ones', async () => {
    const wrapper = mount(QuickLook, { props, global: { stubs: { PreviewModal: true } } });
    await nextTick();
    const hint = document.body.querySelector('.fe-ql-hint');
    expect(hint, 'the hint did not render under <body>').not.toBeNull();
    expect(hint!.textContent).toContain(effectiveCombo('quicklook')); // Space
    expect(hint!.textContent).toContain(effectiveCombo('open')); // Enter

    setShortcutOverride('quicklook', 'Q');
    await nextTick();
    const after = document.body.querySelector('.fe-ql-hint')!.textContent ?? '';
    expect(after, 'the legend still names the default key after a remap').toContain('Q');
    expect(after, 'the legend kept the old key').not.toContain('Space');
    wrapper.unmount();
  });

  it('drops a segment whose action the user unbound', async () => {
    setShortcutOverride('quicklook', '');
    const wrapper = mount(QuickLook, { props, global: { stubs: { PreviewModal: true } } });
    await nextTick();
    const hint = document.body.querySelector('.fe-ql-hint')!;
    // No empty key cap where the unbound action used to be.
    expect([...hint.querySelectorAll('kbd')].map((k) => k.textContent)).not.toContain('');
    wrapper.unmount();
  });
});

describe('drive shell search chip', () => {
  const props = {
    viewMode: 'list' as const,
    searchQuery: '',
    trashActive: false,
    actions: [],
    locale: 'en' as const,
    shell: 'drive' as const,
  };

  it('names the bound palette key, not a hardcoded one', async () => {
    const wrapper = mount(Toolbar, { props, attachTo: document.body });
    await nextTick();
    expect(wrapper.text()).toContain(effectiveCombo('palette')); // Ctrl+K

    setShortcutOverride('palette', 'Ctrl+J');
    await nextTick();
    expect(wrapper.text(), 'the chip still names the default key after a remap').toContain(
      'Ctrl+J',
    );
    wrapper.unmount();
  });
});

describe('shortcutHint()', () => {
  it('is empty for an unbound action, so a caller can drop the segment', () => {
    setShortcutOverride('star', '');
    expect(shortcutHint('star')).toBe('');
  });
});

// ---------------------------------------------------------------------------
// 2. Source: no hint may spell a key out by hand
// ---------------------------------------------------------------------------

/**
 * Locale strings allowed to name a key literally, each with the binding that
 * makes it permanent. Adding a line here is a claim — check the handler.
 */
const FIXED_KEY_HINTS: Record<string, string> = {
  // PreviewModal wires Ctrl/Cmd+S in both editors: monaco.addCommand(CtrlCmd|KeyS)
  // for code, and @keydown.ctrl.s / @keydown.meta.s on the markdown textarea.
  // Neither goes through the registry, so neither can be remapped.
  'viewer.save': 'Ctrl+S — PreviewModal, not a registry action',
  // The palette's own keys, live only while the palette is open and handled by
  // CommandPalette.vue itself. `Esc` IS a registry action but is declared
  // `customizable: false`.
  'palette.hint': '↑↓ / Enter / Esc — CommandPalette.vue, fixed',
  // Not one of ours at all: the interrupt of whatever terminal the reader is
  // standing in, in the `filex mount` guide.
  'conn.guide.mount.win.stop': 'Ctrl-C — the OS terminal, not filex',
};

/** Combo-shaped literals: `Ctrl+K`, `⌘K`, `Alt+↑`, `F2`, a standalone `Esc`. */
const COMBO_RE = /(?:(?:Ctrl|⌘|Cmd|Alt|Shift)\s*[+\-]\s*\S)|(?:\bF(?:[1-9]|1[0-2])\b)|(?:\bEsc\b)/;

function scanCatalogue(name: string, table: Record<string, string>): string[] {
  const offenders: string[] = [];
  for (const [key, value] of Object.entries(table)) {
    if (typeof value !== 'string') continue;
    // The shortcut sheet's own labels are action NAMES, never key names.
    if (key.startsWith('shortcuts.')) continue;
    if (!COMBO_RE.test(value)) continue;
    if (key in FIXED_KEY_HINTS) continue;
    offenders.push(`${name}: ${key} = ${value.slice(0, 80)}`);
  }
  return offenders;
}

describe('no hint spells a key out by hand', () => {
  it('locale catalogues interpolate the combo instead', () => {
    const offenders = [...scanCatalogue('en', en), ...scanCatalogue('tr', tr)];
    expect(
      offenders,
      'a hint names a key literally — interpolate shortcutHint(<action>) instead, ' +
        'or declare it in FIXED_KEY_HINTS with the handler that makes it fixed',
    ).toEqual([]);
  });

  it('every <kbd> renders a value, never a typed-in key', () => {
    const walk = (dir: string): string[] =>
      readdirSync(dir).flatMap((n) => {
        const p = path.join(dir, n);
        return statSync(p).isDirectory() ? walk(p) : [p];
      });
    const offenders: string[] = [];
    for (const file of walk(CORE_SRC).filter((f) => f.endsWith('.vue'))) {
      const src = readFileSync(file, 'utf8');
      for (const m of src.matchAll(/<kbd[^>]*>([\s\S]*?)<\/kbd>/g)) {
        const inner = m[1].trim();
        // `{{ … }}` is a value from the registry — that is the whole point.
        if (inner.startsWith('{{')) continue;
        offenders.push(`${path.relative(CORE_SRC, file)}: <kbd>${inner}</kbd>`);
      }
    }
    expect(
      offenders,
      'a key cap carries a literal — render shortcutHint(<action>) so a remap reaches it',
    ).toEqual([]);
  });

  it('the registry is the only place a default combo is written', () => {
    // Sanity: the ids the hint surfaces above ask for still exist. A renamed
    // action would otherwise make every hint quietly empty.
    for (const id of ['quicklook', 'open', 'palette', 'help', 'search']) {
      expect(
        SHORTCUT_ACTIONS.some((a) => a.id === id),
        `hint surfaces reference the action "${id}", which is no longer in the registry`,
      ).toBe(true);
    }
  });
});
