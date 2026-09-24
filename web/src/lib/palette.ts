/**
 * THE PALETTE, FOR THE WHOLE PANEL — not just for routes with an explorer on
 * them.
 *
 * ⚠⚠ The bug this closes, and why nobody could see it from the code: a
 * palette is a map of `--fe-*` values that core's `lib/themes` publishes in
 * TWO ways — inline on the explorer's root element, and as a singleton
 * `<style data-filex-theme>` that mirrors `variables.css`' selector cascade
 * (`:root,.fe { … }`). The second one is what reaches the admin chrome. It was
 * written by `FileExplorer.vue` in its own `onMounted`, so it existed on
 * /explore and on nothing else. Users, Storages, Settings, Webhooks, the
 * dashboard — every page without a file explorer on it stayed on the stock
 * blue whatever palette the person had chosen, and there was no error and
 * nothing to notice beyond "the panel ignores my theme" (owner, 2026-09-20:
 * "admin panel seçili renk paletinden etkilenmiyor, etkilenmeli").
 *
 * So the app owns the call now. It is idempotent and it is the same singleton
 * element the explorer writes, so an explorer mounting later finds its work
 * already done rather than fighting it.
 *
 * ⚠ The light/dark MODE is a different question and stays with `lib/theme`:
 * the generated CSS carries both variants behind the `.dark` selectors that
 * module toggles, so a mode flip needs no JavaScript here at all.
 *
 * ⚠ `filex.palette` is written from two places that both live outside this
 * module (the settings modal via core's `setTheme`, and another tab), which is
 * why the watcher and the `storage` listener are both here rather than in the
 * component that happens to draw the picker.
 */
import { watch } from 'vue';
import { syncThemeStyle, useThemeState } from '@brftech/filex-core';

let started = false;

/**
 * Publish the stored palette for the whole document, and keep publishing it.
 *
 * Call once, before the mount: the `<style>` goes in on the first frame, so
 * the window does not paint stock blue and then repaint.
 */
export function applyPalette(): void {
  if (started || typeof document === 'undefined') return;
  started = true;

  const { themeId } = useThemeState();

  const apply = () => {
    try {
      syncThemeStyle(themeId.value);
    } catch {
      /* A palette that cannot be applied must not take the app down with it:
         the stock tokens in variables.css are a complete, readable answer. */
    }
  };

  apply();
  // `themeId` is core's own ref and is already cross-tab synced by that
  // module, so watching it covers the picker AND a second tab.
  watch(themeId, apply);
}
