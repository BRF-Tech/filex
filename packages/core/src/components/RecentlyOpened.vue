<script setup lang="ts">
/**
 * RecentlyOpened — drop-down/sidebar tray of the user's recently-opened
 * files. Backed by `GET /api/files/manager/recent?limit=20`.
 */
import { ref, onMounted, watch } from 'vue';

interface RecentNode {
  id: number;
  storage_id: number;
  path: string;
  name: string;
  mime?: string;
  last_opened?: string;
}

const props = defineProps<{
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  limit?: number;
  /** Optional refresh trigger — incrementing this re-fetches. */
  refreshKey?: number | string;
}>();

const emit = defineEmits<{
  (e: 'open', node: RecentNode): void;
  (e: 'error', message: string): void;
}>();

const items = ref<RecentNode[]>([]);
const loading = ref(false);

async function load() {
  loading.value = true;
  try {
    const headers = await (props.authHeaders ?? (() => ({})))();
    const base = props.apiBase ?? '';
    const limit = props.limit ?? 20;
    const res = await fetch(`${base}/api/files/manager/recent?limit=${limit}`, {
      headers,
      credentials: 'include',
    });
    if (res.ok) {
      const body = await res.json();
      items.value = Array.isArray(body.entries) ? body.entries : (Array.isArray(body) ? body : []);
    }
  } catch (err) {
    emit('error', err instanceof Error ? err.message : String(err));
  } finally {
    loading.value = false;
  }
}

function fmtTime(s?: string): string {
  if (!s) return '';
  const t = new Date(s).getTime();
  if (Number.isNaN(t)) return '';
  const diff = Date.now() - t;
  if (diff < 60_000) return 'just now';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}

onMounted(load);
watch(() => props.refreshKey, load);
</script>

<template>
  <div class="filex-recent">
    <header>
      <h3>Recently opened</h3>
      <button class="filex-recent-refresh" type="button" @click="load" :disabled="loading">
        ↻
      </button>
    </header>

    <ul v-if="items.length">
      <li v-for="n in items" :key="n.id">
        <button type="button" @click="emit('open', n)" class="filex-recent-item">
          <span class="filex-recent-name">{{ n.name }}</span>
          <span class="filex-recent-meta">{{ fmtTime(n.last_opened) }}</span>
        </button>
      </li>
    </ul>
    <p v-else-if="!loading" class="filex-recent-empty">Nothing here yet.</p>
    <p v-else class="filex-recent-empty">Loading…</p>
  </div>
</template>

<style scoped>
.filex-recent {
  background: var(--fe-bg-elev, var(--filex-bg-card, #ffffff));
  border: 1px solid var(--fe-border, var(--filex-border, #e5e7eb));
  border-radius: 8px;
  padding: 12px;
  font-size: 13px;
}
.filex-recent header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}
.filex-recent h3 {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--fe-text, var(--filex-text, #111827));
}
.filex-recent-refresh {
  background: transparent;
  border: none;
  color: var(--fe-text-muted, var(--filex-text-muted, #9ca3af));
  cursor: pointer;
  font-size: 14px;
  padding: 2px 6px;
}
.filex-recent ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.filex-recent-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  background: transparent;
  border: none;
  padding: 6px 8px;
  cursor: pointer;
  border-radius: 4px;
  text-align: left;
}
.filex-recent-item:hover {
  background: var(--fe-bg-hover, var(--filex-bg-soft, #f3f4f6));
}
.filex-recent-name {
  color: var(--fe-text, var(--filex-text, #111827));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.filex-recent-meta {
  color: var(--fe-text-muted, var(--filex-text-muted, #9ca3af));
  font-size: 11px;
  margin-left: 8px;
  flex-shrink: 0;
}
.filex-recent-empty {
  color: var(--fe-text-muted, var(--filex-text-muted, #9ca3af));
  margin: 4px 0 0;
}
</style>
