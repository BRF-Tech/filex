/**
 * useKeyboardShortcuts — binds FileExplorer's keyboard affordances.
 *
 * Activated whenever the explorer is mounted (only one instance per
 * page is expected). Skips events that originate inside form controls
 * so the user can type filenames in the search box, modals, etc.
 */

import { onMounted, onBeforeUnmount, type Ref } from 'vue';

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
  onDuplicate?: () => void; // Ctrl+D
  onPathJump?: () => void; // Cmd+K / Ctrl+K
  onGoUp?: () => void; // Alt+Up / Backspace (when nothing selected)
  onShowHelp?: () => void; // ? (Shift+/ on most layouts)
  onToggleInspector?: () => void; // i (koru:k1 details panel)
  hasSelection?: () => boolean; // disambiguates Backspace
}

/**
 * SHORTCUTS — the single source of truth for the shortcut cheat-sheet
 * (ShortcutsHelp.vue). Only shortcuts actually wired by FileExplorer are
 * listed; labels/groups resolve through the locale catalogue so the help
 * dialog stays tr/en aware. `keys` entries are display combos — each one
 * renders as its own <kbd>, alternatives joined visually with "/".
 */
export interface ShortcutDef {
  keys: string[];
  labelKey: string;
  groupKey: string;
}

export const SHORTCUTS: ShortcutDef[] = [
  // Navigation
  { keys: ['Ctrl+K'], labelKey: 'shortcuts.palette', groupKey: 'shortcuts.group.nav' },
  { keys: ['/'], labelKey: 'shortcuts.search', groupKey: 'shortcuts.group.nav' },
  { keys: ['Alt+↑', 'Backspace'], labelKey: 'shortcuts.go_up', groupKey: 'shortcuts.group.nav' },
  { keys: ['Enter'], labelKey: 'shortcuts.open', groupKey: 'shortcuts.group.nav' },
  { keys: ['Esc'], labelKey: 'shortcuts.close', groupKey: 'shortcuts.group.nav' },
  { keys: ['I'], labelKey: 'shortcuts.inspector', groupKey: 'shortcuts.group.nav' } /* koru:k1 */,
  { keys: ['?'], labelKey: 'shortcuts.help', groupKey: 'shortcuts.group.nav' },
  // Selection
  { keys: ['Ctrl+A'], labelKey: 'shortcuts.select_all', groupKey: 'shortcuts.group.selection' },
  // File operations
  { keys: ['F2'], labelKey: 'shortcuts.rename', groupKey: 'shortcuts.group.file' },
  { keys: ['Del'], labelKey: 'shortcuts.delete', groupKey: 'shortcuts.group.file' },
  { keys: ['Ctrl+X'], labelKey: 'shortcuts.cut', groupKey: 'shortcuts.group.file' },
  { keys: ['Ctrl+C'], labelKey: 'shortcuts.copy', groupKey: 'shortcuts.group.file' },
  { keys: ['Ctrl+V'], labelKey: 'shortcuts.paste', groupKey: 'shortcuts.group.file' },
];

export function useKeyboardShortcuts(rootEl: Ref<HTMLElement | null>, handlers: ShortcutHandlers) {
  function onKey(e: KeyboardEvent) {
    const root = rootEl.value;
    if (!root) return;

    // Skip when the event originates inside a form control — don't
    // want `Delete` while editing a filename, `/` while typing in the
    // search box, etc. Escape always goes through so modals can close.
    const target = e.target as HTMLElement | null;
    if (
      target &&
      (target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.isContentEditable)
    ) {
      if (e.key !== 'Escape') return;
    }

    const ctrl = e.ctrlKey || e.metaKey;

    switch (e.key) {
      case 'Delete':
        if (handlers.onDelete) {
          e.preventDefault();
          handlers.onDelete();
        }
        break;
      case 'Backspace':
        // Backspace = delete when something is selected (file-manager
        // convention), parent-dir navigation otherwise. Without this
        // disambiguation Backspace was firing onDelete with an empty
        // selection and nothing happened, leaving users wondering why
        // the obvious 'go back' key did nothing.
        if (handlers.hasSelection?.() && handlers.onDelete) {
          e.preventDefault();
          handlers.onDelete();
        } else if (handlers.onGoUp) {
          e.preventDefault();
          handlers.onGoUp();
        }
        break;
      case 'F2':
        if (handlers.onRename) {
          e.preventDefault();
          handlers.onRename();
        }
        break;
      case 'Enter':
        if (handlers.onOpen) {
          handlers.onOpen();
        }
        break;
      case 'Escape':
        if (handlers.onClose) {
          handlers.onClose();
        }
        break;
      case '/':
        if (handlers.onFocusSearch && !ctrl) {
          e.preventDefault();
          handlers.onFocusSearch();
        }
        break;
      case '?':
        if (handlers.onShowHelp && !ctrl && !e.altKey) {
          e.preventDefault();
          handlers.onShowHelp();
        }
        break;
      case 'a':
      case 'A':
        if (ctrl && handlers.onSelectAll) {
          e.preventDefault();
          handlers.onSelectAll();
        }
        break;
      case 'x':
      case 'X':
        if (ctrl && handlers.onCut) {
          e.preventDefault();
          handlers.onCut();
        }
        break;
      case 'c':
      case 'C':
        if (ctrl && handlers.onCopy) {
          e.preventDefault();
          handlers.onCopy();
        }
        break;
      case 'v':
      case 'V':
        if (ctrl && handlers.onPaste) {
          e.preventDefault();
          handlers.onPaste();
        }
        break;
      case 'd':
      case 'D':
        if (ctrl && handlers.onDuplicate) {
          e.preventDefault();
          handlers.onDuplicate();
        }
        break;
      /* koru:k1 — plain `i` toggles the inspector (details) panel. Ctrl+I /
         Alt+I stay untouched so browser/OS combos keep working. */
      case 'i':
      case 'I':
        if (!ctrl && !e.altKey && handlers.onToggleInspector) {
          e.preventDefault();
          handlers.onToggleInspector();
        }
        break;
      case 'k':
      case 'K':
        if (ctrl && handlers.onPathJump) {
          e.preventDefault();
          handlers.onPathJump();
        }
        break;
      case 'ArrowUp':
        // Alt+Up = parent dir (matches Finder / Files / Explorer).
        if (e.altKey && handlers.onGoUp) {
          e.preventDefault();
          handlers.onGoUp();
        }
        break;
    }
  }

  onMounted(() => window.addEventListener('keydown', onKey));
  onBeforeUnmount(() => window.removeEventListener('keydown', onKey));
}
