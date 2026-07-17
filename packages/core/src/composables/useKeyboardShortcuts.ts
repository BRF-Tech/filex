/**
 * useKeyboardShortcuts — binds FileExplorer's keyboard affordances.
 *
 * wiring:c2 — rewritten as a REGISTRY: every action is declared once
 * (id + default combo + i18n label + category) and the user may remap
 * the customizable ones. Overrides persist in localStorage under
 * `filex.shortcuts` as `{ "<actionId>": "<combo>" }` — only deviations
 * from the defaults are stored; an empty string means "unbound".
 *
 * Canonical combo format (also the display format): modifiers in
 * `Ctrl+Alt+Shift` order followed by a single key token, joined with
 * `+`. Key tokens: uppercase letters/digits, symbols as typed (`/`,
 * `?`), `F1`–`F12`, `Space`, `Enter`, `Esc`, `Del`, `Backspace`, `Tab`,
 * `Home`, `End`, `PgUp`, `PgDn` and arrows as `↑ ↓ ← →`. Meta (Cmd) is
 * folded into `Ctrl` so one saved combo works on macOS too. For
 * printable symbols Shift is omitted (`?` already encodes it).
 *
 * Activated whenever the explorer is mounted (only one instance per
 * page is expected). Skips events that originate inside form controls
 * so the user can type filenames in the search box, modals, etc.
 */

import { computed, onMounted, onBeforeUnmount, ref, type ComputedRef, type Ref } from 'vue';

export interface ShortcutHandlers {
  onDelete?: () => void;
  onRename?: () => void; // F2
  onSelectAll?: () => void; // Ctrl+A
  onCut?: () => void; // Ctrl+X
  onCopy?: () => void; // Ctrl+C
  onPaste?: () => void; // Ctrl+V
  onOpen?: () => void; // Enter
  onClose?: () => void; // Escape
  onFocusSearch?: () => void; // /
  onDuplicate?: () => void; // (unwired — kept for API compat)
  onPathJump?: () => void; // Cmd+K / Ctrl+K
  onGoUp?: () => void; // Alt+Up / Backspace (when nothing selected)
  onShowHelp?: () => void; // ? (Shift+/ on most layouts)
  onToggleInspector?: () => void; // i (koru:k1 details panel)
  onQuickLook?: () => void; // Space (wiring:c2 quick-look overlay)
  /* wiring:d1 — tab strip actions */
  onTabNew?: () => void; // Ctrl+T
  onTabClose?: () => void; // Ctrl+W
  onTabNext?: () => void; // Ctrl+Tab
  onTabPrev?: () => void; // Ctrl+Shift+Tab
  /* /wiring:d1 */
  hasSelection?: () => boolean; // disambiguates Backspace
}

// --------------------------------------------------------------------
// Registry
// --------------------------------------------------------------------

/** One remappable action in the shortcut registry. */
export interface ShortcutActionDef {
  /** Stable id — also the localStorage override key. */
  id: string;
  /** Default combo in canonical form. */
  defaultCombo: string;
  /**
   * Extra built-in combos that also trigger the action but are NOT
   * remappable (e.g. Backspace on go-up, which carries the
   * selection-dependent dual behaviour). Shown in the help sheet and
   * reserved in conflict detection.
   */
  fixedCombos?: string[];
  labelKey: string;
  groupKey: string;
  /** false → the combo can't be remapped (Esc). Default true. */
  customizable?: boolean;
  /** false → don't preventDefault on match (Enter / Esc). Default true. */
  prevent?: boolean;
  /** true → fires even when focus sits in a form control (Esc). */
  inForms?: boolean;
}

/** Registry order = help/settings display order. */
export const SHORTCUT_ACTIONS: ShortcutActionDef[] = [
  // Navigation
  { id: 'palette', defaultCombo: 'Ctrl+K', labelKey: 'shortcuts.palette', groupKey: 'shortcuts.group.nav' },
  { id: 'search', defaultCombo: '/', labelKey: 'shortcuts.search', groupKey: 'shortcuts.group.nav' },
  { id: 'go-up', defaultCombo: 'Alt+↑', fixedCombos: ['Backspace'], labelKey: 'shortcuts.go_up', groupKey: 'shortcuts.group.nav' },
  { id: 'open', defaultCombo: 'Enter', prevent: false, labelKey: 'shortcuts.open', groupKey: 'shortcuts.group.nav' },
  { id: 'quicklook', defaultCombo: 'Space', labelKey: 'shortcuts.quicklook', groupKey: 'shortcuts.group.nav' },
  { id: 'close', defaultCombo: 'Esc', customizable: false, prevent: false, inForms: true, labelKey: 'shortcuts.close', groupKey: 'shortcuts.group.nav' },
  { id: 'inspector', defaultCombo: 'I', labelKey: 'shortcuts.inspector', groupKey: 'shortcuts.group.nav' } /* koru:k1 */,
  { id: 'help', defaultCombo: '?', labelKey: 'shortcuts.help', groupKey: 'shortcuts.group.nav' },
  // Selection
  { id: 'select-all', defaultCombo: 'Ctrl+A', labelKey: 'shortcuts.select_all', groupKey: 'shortcuts.group.selection' },
  // File operations
  { id: 'rename', defaultCombo: 'F2', labelKey: 'shortcuts.rename', groupKey: 'shortcuts.group.file' },
  { id: 'delete', defaultCombo: 'Del', labelKey: 'shortcuts.delete', groupKey: 'shortcuts.group.file' },
  { id: 'cut', defaultCombo: 'Ctrl+X', labelKey: 'shortcuts.cut', groupKey: 'shortcuts.group.file' },
  { id: 'copy', defaultCombo: 'Ctrl+C', labelKey: 'shortcuts.copy', groupKey: 'shortcuts.group.file' },
  { id: 'paste', defaultCombo: 'Ctrl+V', labelKey: 'shortcuts.paste', groupKey: 'shortcuts.group.file' },
  /* wiring:d1 — tabs. Note: browsers reserve Ctrl+T/W/Tab in normal pages
   * (preventDefault can't stop them there); they work in webcomponent/PWA/
   * kiosk contexts and stay remappable through the settings modal. */
  { id: 'tab-new', defaultCombo: 'Ctrl+T', labelKey: 'shortcuts.tab_new', groupKey: 'shortcuts.group.tabs' },
  { id: 'tab-close', defaultCombo: 'Ctrl+W', labelKey: 'shortcuts.tab_close', groupKey: 'shortcuts.group.tabs' },
  { id: 'tab-next', defaultCombo: 'Ctrl+Tab', labelKey: 'shortcuts.tab_next', groupKey: 'shortcuts.group.tabs' },
  { id: 'tab-prev', defaultCombo: 'Ctrl+Shift+Tab', labelKey: 'shortcuts.tab_prev', groupKey: 'shortcuts.group.tabs' },
  /* /wiring:d1 */
];

/** action id → ShortcutHandlers callback name. */
const HANDLER_KEY: Record<string, keyof ShortcutHandlers> = {
  palette: 'onPathJump',
  search: 'onFocusSearch',
  'go-up': 'onGoUp',
  open: 'onOpen',
  quicklook: 'onQuickLook',
  close: 'onClose',
  inspector: 'onToggleInspector',
  help: 'onShowHelp',
  'select-all': 'onSelectAll',
  rename: 'onRename',
  delete: 'onDelete',
  cut: 'onCut',
  copy: 'onCopy',
  paste: 'onPaste',
  /* wiring:d1 */
  'tab-new': 'onTabNew',
  'tab-close': 'onTabClose',
  'tab-next': 'onTabNext',
  'tab-prev': 'onTabPrev',
};

// --------------------------------------------------------------------
// Overrides (localStorage `filex.shortcuts`)
// --------------------------------------------------------------------

const SHORTCUTS_LS_KEY = 'filex.shortcuts';

function readOverrides(): Record<string, string> {
  try {
    const raw = localStorage.getItem(SHORTCUTS_LS_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== 'object') return {};
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof v === 'string' && SHORTCUT_ACTIONS.some((a) => a.id === k)) out[k] = v;
    }
    return out;
  } catch {
    return {};
  }
}

// Module-level so the key handler, the "?" help sheet and the settings
// modal all observe the same reactive mapping.
const overrides = ref<Record<string, string>>(readOverrides());

function writeOverrides(): void {
  try {
    if (Object.keys(overrides.value).length === 0) localStorage.removeItem(SHORTCUTS_LS_KEY);
    else localStorage.setItem(SHORTCUTS_LS_KEY, JSON.stringify(overrides.value));
  } catch {
    /* private mode / quota */
  }
}

function defOf(id: string): ShortcutActionDef | undefined {
  return SHORTCUT_ACTIONS.find((a) => a.id === id);
}

/** Current combo for an action ('' = unbound). */
export function effectiveCombo(id: string): string {
  const o = overrides.value[id];
  if (o !== undefined) return o;
  return defOf(id)?.defaultCombo ?? '';
}

/** Set (or clear back to default) a user override. '' unbinds. */
export function setShortcutOverride(id: string, combo: string): void {
  const def = defOf(id);
  if (!def || def.customizable === false) return;
  const next = { ...overrides.value };
  if (combo === def.defaultCombo) delete next[id];
  else next[id] = combo;
  overrides.value = next;
  writeOverrides();
}

export function resetShortcut(id: string): void {
  if (!(id in overrides.value)) return;
  const next = { ...overrides.value };
  delete next[id];
  overrides.value = next;
  writeOverrides();
}

export function resetAllShortcuts(): void {
  overrides.value = {};
  writeOverrides();
}

export interface ShortcutConflict {
  id: string;
  /** true → the clash is with a fixed (non-remappable) combo. */
  fixed: boolean;
}

/**
 * Does `combo` already trigger another action? Checks both effective
 * combos and the reserved fixed combos (Backspace, Esc). Returns null
 * when free.
 */
export function findShortcutConflict(combo: string, excludeId: string): ShortcutConflict | null {
  if (!combo) return null;
  for (const def of SHORTCUT_ACTIONS) {
    if (def.id === excludeId) continue;
    if (def.fixedCombos?.includes(combo)) return { id: def.id, fixed: true };
    if (effectiveCombo(def.id) === combo) return { id: def.id, fixed: def.customizable === false };
  }
  return null;
}

// --------------------------------------------------------------------
// Event → canonical combo
// --------------------------------------------------------------------

const KEY_TOKEN_MAP: Record<string, string> = {
  ' ': 'Space',
  Spacebar: 'Space',
  Escape: 'Esc',
  Delete: 'Del',
  ArrowUp: '↑',
  ArrowDown: '↓',
  ArrowLeft: '←',
  ArrowRight: '→',
  PageUp: 'PgUp',
  PageDown: 'PgDn',
};

const MODIFIER_KEYS = new Set(['Control', 'Alt', 'Shift', 'Meta', 'AltGraph', 'CapsLock', 'NumLock', 'ScrollLock', 'Fn', 'Dead']);

function keyToken(e: KeyboardEvent): string | null {
  const k = e.key;
  if (!k || MODIFIER_KEYS.has(k)) return null;
  const mapped = KEY_TOKEN_MAP[k];
  if (mapped) return mapped;
  if (k.length === 1) {
    const upper = k.toUpperCase();
    // Letters normalize to uppercase; symbols pass through as typed.
    return upper;
  }
  return k; // 'Enter', 'F2', 'Tab', 'Home', 'End', 'Backspace', …
}

/**
 * Canonical combo for a keyboard event, or null for a bare modifier
 * press. Meta folds into Ctrl; Shift is dropped for printable symbols
 * because the produced character already encodes it (`?` not `Shift+?`).
 */
export function comboFromEvent(e: KeyboardEvent): string | null {
  const key = keyToken(e);
  if (!key) return null;
  const parts: string[] = [];
  if (e.ctrlKey || e.metaKey) parts.push('Ctrl');
  if (e.altKey) parts.push('Alt');
  const printableSymbol = key.length === 1 && key.toUpperCase() === key.toLowerCase();
  if (e.shiftKey && !printableSymbol) parts.push('Shift');
  parts.push(key);
  return parts.join('+');
}

// --------------------------------------------------------------------
// Reactive views (help sheet + settings modal)
// --------------------------------------------------------------------

export interface ShortcutView {
  id: string;
  /** Current (possibly remapped) combo, '' = unbound. */
  combo: string;
  /** Non-remappable extra combos (display + reservation only). */
  fixedCombos: string[];
  /** Display combos: [combo, ...fixedCombos] minus empties. */
  keys: string[];
  labelKey: string;
  groupKey: string;
  customizable: boolean;
  overridden: boolean;
}

/** Reactive, override-aware view of the registry (registry order). */
export function useShortcutList(): ComputedRef<ShortcutView[]> {
  return computed(() =>
    SHORTCUT_ACTIONS.map((def) => {
      const combo = effectiveCombo(def.id);
      const fixed = def.fixedCombos ?? [];
      return {
        id: def.id,
        combo,
        fixedCombos: fixed,
        keys: [combo, ...fixed].filter((c) => !!c),
        labelKey: def.labelKey,
        groupKey: def.groupKey,
        customizable: def.customizable !== false,
        overridden: overrides.value[def.id] !== undefined,
      };
    }),
  );
}

/**
 * @deprecated Legacy static cheat-sheet shape (pre-registry). Kept for
 * API compatibility; use `useShortcutList()` for the live, remap-aware
 * list.
 */
export interface ShortcutDef {
  keys: string[];
  labelKey: string;
  groupKey: string;
}

/** @deprecated See {@link useShortcutList}. Defaults only. */
export const SHORTCUTS: ShortcutDef[] = SHORTCUT_ACTIONS.map((def) => ({
  keys: [def.defaultCombo, ...(def.fixedCombos ?? [])].filter((c) => !!c),
  labelKey: def.labelKey,
  groupKey: def.groupKey,
}));

// --------------------------------------------------------------------
// Binding
// --------------------------------------------------------------------

export function useKeyboardShortcuts(rootEl: Ref<HTMLElement | null>, handlers: ShortcutHandlers) {
  // combo → action def, rebuilt when overrides change.
  const comboMap = computed(() => {
    const m = new Map<string, ShortcutActionDef>();
    for (const def of SHORTCUT_ACTIONS) {
      const c = effectiveCombo(def.id);
      if (c) m.set(c, def);
    }
    return m;
  });

  function onKey(e: KeyboardEvent) {
    const root = rootEl.value;
    if (!root) return;

    // Skip when the event originates inside a form control — don't
    // want `Delete` while editing a filename, `/` while typing in the
    // search box, etc. Escape always goes through so modals can close.
    const target = e.target as HTMLElement | null;
    const inForm = !!(
      target &&
      (target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.tagName === 'SELECT' ||
        target.isContentEditable)
    );

    const combo = comboFromEvent(e);
    if (!combo) return;

    // Backspace keeps its FIXED dual behaviour: delete when something is
    // selected (file-manager convention), parent-dir navigation otherwise.
    // Not remappable — the selection-dependent branch doesn't fit the
    // one-combo-one-action registry model.
    if (combo === 'Backspace') {
      if (inForm) return;
      if (handlers.hasSelection?.() && handlers.onDelete) {
        e.preventDefault();
        handlers.onDelete();
      } else if (handlers.onGoUp) {
        e.preventDefault();
        handlers.onGoUp();
      }
      return;
    }

    const def = comboMap.value.get(combo);
    if (!def) return;
    if (inForm && !def.inForms) return;
    // Space on a focused button/link must keep its native activation
    // (quick-look would otherwise fire alongside the click).
    if (
      combo === 'Space' &&
      target &&
      typeof target.closest === 'function' &&
      target.closest('button, a, select, summary, [role="button"], [role="menuitem"]')
    ) {
      return;
    }
    const fn = handlers[HANDLER_KEY[def.id]] as (() => void) | undefined;
    if (!fn) return;
    if (def.prevent !== false) e.preventDefault();
    fn();
  }

  onMounted(() => window.addEventListener('keydown', onKey));
  onBeforeUnmount(() => window.removeEventListener('keydown', onKey));
}
