<script setup lang="ts">
/**
 * StarButton — toggleable star indicator for a node.
 *
 * Backed by the `node_meta key="starred"` server-side row (see
 * `POST /api/files/manager/star`). Optimistic update — flips the local
 * state immediately and rolls back on API error.
 */
import { ref, watch, computed } from 'vue';
import { setNodeStarred } from '../lib/star';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';
import { resolveLocale } from '../locales/resolve';

const props = defineProps<{
  starred: boolean;
  nodeId: number;
  apiBase?: string;
  /** Auth header builder injected by the parent file explorer. */
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  /** Credentials mode, from the explorer's auth kind. ⚠ Defaults to
   *  'same-origin': a credentialed cross-origin request cannot be answered
   *  with ACAO:* , so hardcoding 'include' broke this call in every embed
   *  served from a different origin to the API. */
  authCredentials?: RequestCredentials;
  /** Compact mode for grid view (no label, just the icon). */
  compact?: boolean;
  /**
   * Card mode — the same button sitting ON a grid/gallery tile instead of in
   * a list cell: a round translucent chip in the tile's corner. It is the SAME
   * component, deliberately: a card star written separately is a second
   * starring path, and the two drift the first time one of them is fixed.
   */
  card?: boolean;
  /** Locale for the title/label. ⚠ The strings used to be hardcoded English
   *  ("Star"/"Unstar"), which is a Turkish user's only untranslated control in
   *  the row. */
  locale?: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'change', value: boolean): void;
  (e: 'error', message: string): void;
}>();

const local = ref(props.starred);
watch(() => props.starred, (v) => { local.value = v; });

const { t } = useLocale(() => resolveLocale(props.locale));
const label = computed(() => t(local.value ? 'ctx.unstar' : 'ctx.star'));

async function toggle() {
  const next = !local.value;
  local.value = next; // optimistic
  try {
    await setNodeStarred(props.nodeId, next, {
      apiBase: props.apiBase,
      authHeaders: props.authHeaders,
      authCredentials: props.authCredentials,
    });
    emit('change', next);
  } catch (err) {
    local.value = !next; // rollback
    emit('error', err instanceof Error ? err.message : String(err));
  }
}
</script>

<template>
  <button
    type="button"
    class="filex-star-btn"
    :class="{ 'is-starred': local, 'is-compact': compact, 'is-card': card }"
    :aria-pressed="local"
    :title="label"
    :aria-label="label"
    data-testid="star-toggle"
    @click.stop="toggle"
  >
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path
        :fill="local ? 'currentColor' : 'none'"
        stroke="currentColor"
        stroke-width="2"
        stroke-linejoin="round"
        d="M12 2.5l3.09 6.26 6.91 1-5 4.87 1.18 6.87L12 18.27l-6.18 3.23L7 14.63 2 9.76l6.91-1z"
      />
    </svg>
    <span v-if="!compact" class="filex-star-label">{{ label }}</span>
  </button>
</template>

<style>
/* ⚠ NOT `scoped`, deliberately. Vue's scoped styles compile to
   `.cls[data-v-HASH]`, and in the web-component build the hash baked into
   this CSS does not match the one Vue stamps onto the DOM — so every rule
   here silently stopped applying. Measured in the desktop app: the share
   dialog had `position: static`, no background and no radius, i.e. raw
   unstyled HTML, in EVERY embedded surface.
   Safe to drop: every selector below is prefixed (fx-/fe-/filex-), so
   there is nothing here that can leak into a host page. */
.filex-star-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  padding: 4px 8px;
  color: var(--filex-text-muted, #9ca3af);
  cursor: pointer;
  transition: color 120ms, background 120ms;
}
.filex-star-btn:hover {
  color: var(--filex-accent-amber, #f59e0b);
  background: var(--filex-bg-soft, rgba(0, 0, 0, 0.04));
}
.filex-star-btn.is-starred {
  color: var(--filex-accent-amber, #f59e0b);
}
.filex-star-btn.is-compact {
  padding: 4px;
}
.filex-star-label {
  font-size: 13px;
}
</style>
