<script setup lang="ts">
/**
 * StarButton — toggleable star indicator for a node.
 *
 * Backed by the `node_meta key="starred"` server-side row (see
 * `POST /api/files/manager/star`). Optimistic update — flips the local
 * state immediately and rolls back on API error.
 */
import { ref, watch } from 'vue';

const props = defineProps<{
  starred: boolean;
  nodeId: number;
  apiBase?: string;
  /** Auth header builder injected by the parent file explorer. */
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  /** Compact mode for grid view (no label, just the icon). */
  compact?: boolean;
}>();

const emit = defineEmits<{
  (e: 'change', value: boolean): void;
  (e: 'error', message: string): void;
}>();

const local = ref(props.starred);
watch(() => props.starred, (v) => { local.value = v; });

async function toggle() {
  const next = !local.value;
  local.value = next; // optimistic
  try {
    const headers = {
      'Content-Type': 'application/json',
      ...(await (props.authHeaders ?? (() => ({})))()),
    };
    const base = props.apiBase ?? '';
    const res = await fetch(`${base}/api/files/manager/star`, {
      method: 'POST',
      headers,
      credentials: 'include',
      body: JSON.stringify({ node_id: props.nodeId, starred: next }),
    });
    if (!res.ok) throw new Error(`star toggle failed: ${res.status}`);
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
    :class="{ 'is-starred': local, 'is-compact': compact }"
    :aria-pressed="local"
    :title="local ? 'Unstar' : 'Star'"
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
    <span v-if="!compact" class="filex-star-label">{{ local ? 'Starred' : 'Star' }}</span>
  </button>
</template>

<style scoped>
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
