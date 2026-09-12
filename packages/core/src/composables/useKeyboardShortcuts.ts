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
  onToggleHidden?: () => void; // Ctrl+Shift+. — dot-file visibility
  /* yildiz:s1 */
  onStar?: () => void; // S — star / unstar the selection
  onQuickLook?: () => void; // Space (wiring:c2 quick-look overlay)
  /* wiring:d1 — tab strip actions */
  onTabNew?: () => void; // Ctrl+T
  onTabClose?: () => void; // Ctrl+W
  onTabNext?: () => void; // Ctrl+Tab
  onTabPrev?: () => void; // Ctrl+Shift+Tab
  /* /wiring:d1 */
  /* tus:t1 — the verbs that were only ever reachable from the menus. Every
   * one of them is remappable like the rest; the ones that ship unbound are
   * there so the user can give them a key, not because they do less. */
  onNewFolder?: () => void; // Shift+N
  onUpload?: () => void; // U
  onRefresh?: () => void; // R
  onDownload?: () => void; // D
  onPreview?: () => void; // P
  onShare?: () => void; // Shift+S
  onTags?: () => void; // T
  onConvert?: () => void; // (unbound)
  onOpenTab?: () => void; // (unbound)
  onCopyPath?: () => void; // (unbound)
  onCopyId?: () => void; // (unbound)
  onRestore?: () => void; // (unbound)
  /* /tus:t1 */
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

/**
 * Combos the BROWSER takes before the page sees them. `preventDefault()`
 * cannot stop these in an ordinary tab — Chrome and Firefox handle them at
 * the window level — so an action bound to one of them only ever fires in
 * the desktop app, an installed PWA or a kiosk window.
 *
 * They are not forbidden as DEFAULTS (the tab actions have shipped on
 * Ctrl+T/W/Tab since wiring:d1 and work in the desktop app), but the
 * settings modal refuses to let a user assign one: choosing a key that
 * silently does nothing where you are standing is not a choice, it is a
 * trap. `isReservedCombo` is the one place that list lives.
 */
const RESERVED_COMBOS = new Set([
  'Ctrl+N', 'Ctrl+Shift+N',
  'Ctrl+T', 'Ctrl+Shift+T',
  'Ctrl+W', 'Ctrl+Shift+W',
  'Ctrl+Tab', 'Ctrl+Shift+Tab',
  'Ctrl+Q',
  'Ctrl+Shift+I', 'Ctrl+Shift+J', 'Ctrl+Shift+C',
  'F11', 'F12',
]);

/** Does the browser eat this combo before the page can see it? */
export function isReservedCombo(combo: string): boolean {
  return RESERVED_COMBOS.has(combo);
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
  { id: 'toggle-hidden', defaultCombo: 'Ctrl+Shift+.', labelKey: 'shortcuts.toggle_hidden', groupKey: 'shortcuts.group.nav' },
  /* yildiz:s1 — starring is an action, so it belongs where the other verbs
   * are. A bare letter like the inspector's `I`; the handler ignores events
   * from form controls, so it never eats a keystroke meant for a filename. */
  { id: 'star', defaultCombo: 'S', labelKey: 'shortcuts.star', groupKey: 'shortcuts.group.file' },
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
  /* tus:t1 — the rest of the menu verbs. Until now these were reachable only
   * by right-clicking, and the right-click menu named no keys at all, so 16 of
   * the registry's actions were invisible unless somebody opened the `?` sheet
   * on their own. The ones with no default are deliberate: they are rare
   * enough that taking a key from the user by default is the wrong trade, and
   * they are in the registry so the user can take one. */
  { id: 'new-folder', defaultCombo: 'Shift+N', labelKey: 'shortcuts.new_folder', groupKey: 'shortcuts.group.file' },
  { id: 'upload', defaultCombo: 'U', labelKey: 'shortcuts.upload', groupKey: 'shortcuts.group.file' },
  { id: 'refresh', defaultCombo: 'R', labelKey: 'shortcuts.refresh', groupKey: 'shortcuts.group.nav' },
  { id: 'download', defaultCombo: 'D', labelKey: 'shortcuts.download', groupKey: 'shortcuts.group.file' },
  { id: 'preview', defaultCombo: 'P', labelKey: 'shortcuts.preview', groupKey: 'shortcuts.group.nav' },
  { id: 'share', defaultCombo: 'Shift+S', labelKey: 'shortcuts.share', groupKey: 'shortcuts.group.file' },
  { id: 'tags', defaultCombo: 'T', labelKey: 'shortcuts.tags', groupKey: 'shortcuts.group.file' },
  { id: 'convert', defaultCombo: '', labelKey: 'shortcuts.convert', groupKey: 'shortcuts.group.file' },
  { id: 'open-tab', defaultCombo: '', labelKey: 'shortcuts.open_tab', groupKey: 'shortcuts.group.tabs' },
  { id: 'copy-path', defaultCombo: '', labelKey: 'shortcuts.copy_path', groupKey: 'shortcuts.group.file' },
  { id: 'copy-id', defaultCombo: '', labelKey: 'shortcuts.copy_id', groupKey: 'shortcuts.group.file' },
  { id: 'restore', defaultCombo: '', labelKey: 'shortcuts.restore', groupKey: 'shortcuts.group.file' },
  /* /tus:t1 */
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
  'toggle-hidden': 'onToggleHidden',
  star: 'onStar' /* yildiz:s1 */,
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
  /* tus:t1 */
  'new-folder': 'onNewFolder',
  upload: 'onUpload',
  refresh: 'onRefresh',
  download: 'onDownload',
  preview: 'onPreview',
  share: 'onShare',
  tags: 'onTags',
  convert: 'onConvert',
  'open-tab': 'onOpenTab',
  'copy-path': 'onCopyPath',
  'copy-id': 'onCopyId',
  restore: 'onRestore',
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

// --------------------------------------------------------------------
// Hint surfaces
// --------------------------------------------------------------------

/**
 * Is this a Mac-style keyboard? Only affects how a combo is PRINTED —
 * the canonical form folds Meta into Ctrl, so one saved combo works on
 * every platform and only the label differs.
 */
export function isMacLike(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    /mac|iphone|ipad|ipod/i.test(navigator.platform || navigator.userAgent || '')
  );
}

/** Canonical combo → what a human should see. '' stays ''. */
export function comboLabel(combo: string): string {
  if (!combo) return '';
  return isMacLike() ? combo.replace(/\bCtrl\b/g, '⌘') : combo;
}

/**
 * Menu action key → registry action id.
 *
 * The right-click menu, the toolbar and the keyboard all name the same verbs,
 * but the menu's `key` and the registry's `id` are separate vocabularies and
 * two of them genuinely differ (`access` is the share dialog, `details` is the
 * inspector). One map, in one place: a second copy is a second chance to
 * forget, and the symptom would be a menu row that silently stops naming its
 * key while every other row keeps working.
 */
export const MENU_ACTION_SHORTCUTS: Record<string, string> = {
  open: 'open',
  'open-tab': 'open-tab',
  preview: 'preview',
  download: 'download',
  convert: 'convert',
  access: 'share',
  details: 'inspector',
  'copy-id': 'copy-id',
  'copy-path': 'copy-path',
  rename: 'rename',
  cut: 'cut',
  copy: 'copy',
  paste: 'paste',
  star: 'star',
  tags: 'tags',
  delete: 'delete',
  restore: 'restore',
  'new-folder': 'new-folder',
  'toggle-hidden': 'toggle-hidden',
  upload: 'upload',
  refresh: 'refresh',
};

/**
 * What a menu row or a toolbar button should print beside its label: the
 * current combo for the verb it triggers, or '' when it has none (unbound, or
 * not a registry action at all — `keep-local`, the E2E entries and so on).
 */
export function menuShortcutHint(menuKey: string): string {
  /* Fall back to the key itself: the toolbar's own buttons (go-up, upload,
   * refresh…) pass a registry id directly, and an id nothing knows returns ''
   * rather than throwing. */
  const id = MENU_ACTION_SHORTCUTS[menuKey] ?? menuKey;
  return SHORTCUT_ACTIONS.some((a) => a.id === id) ? shortcutHint(id) : '';
}

/**
 * The one way a hint may name a key.
 *
 * ⚠ Never write a key into a hint by hand — not in a template, not in a
 * locale string. Every combo in this registry is remappable, so a
 * hardcoded "Ctrl+K" is true only until someone opens the shortcut
 * settings, and then it is a label that tells the user to press a key
 * that does nothing. Call this instead and interpolate the result;
 * `web/tests/ui/shortcutHints.test.ts` fails the build on a literal.
 *
 * Returns '' when the action is unbound, so a caller can drop the whole
 * segment rather than print an empty key cap.
 */
export function shortcutHint(id: string): string {
  return comboLabel(effectiveCombo(id));
}

/**
 * Does this event fire the given registry action? For overlays that
 * handle their own keys (the quick-look peek) rather than going through
 * the global binder — without this they keep answering to the DEFAULT
 * key after the user has remapped the action.
 */
export function eventMatchesShortcut(e: KeyboardEvent, id: string): boolean {
  const combo = effectiveCombo(id);
  if (!combo) return false;
  const fired = comboFromEvent(e);
  if (fired === combo) return true;
  return defOf(id)?.fixedCombos?.includes(fired ?? '') ?? false;
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

    /* ui-fix — while a context menu is open, global shortcuts stay quiet
     * (except Esc, which the menu scopes itself). Otherwise e.g. Delete
     * opens a modal UNDER the menu backdrop and the UI wedges: the
     * backdrop intercepts every click on the new dialog. */
    if (e.key !== 'Escape' && document.querySelector('.fe-ctx-backdrop')) return;

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
