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
  hasSelection?: () => boolean; // disambiguates Backspace
}

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
