<script setup lang="ts">
/**
 * RecentlyOpened — drop-down/sidebar tray of the user's recently-opened
 * files. Backed by `GET /api/files/manager/recent?limit=20`.
 */
import { ref, onMounted, watch } from 'vue';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */

interface RecentNode {
  id: number;
  storage_id: number;
  path: string;
  name: string;
  mime?: string;
  last_opened?: string;
  /** The raw row carries more (`storage`, `perm`, `read_only`, `type`,
   *  the mtimes…) — the explorer turns it into a FileNode for the context
   *  menu with the same converter Recent / Starred / a tag view use. */
  [k: string]: unknown;
}

const props = defineProps<{
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  /** Credentials mode, from the explorer's auth kind. ⚠ Defaults to
   *  'same-origin': a credentialed cross-origin request cannot be answered
   *  with ACAO:* , so hardcoding 'include' broke this call in every embed
   *  served from a different origin to the API. */
  authCredentials?: RequestCredentials;
  limit?: number;
  /** Optional refresh trigger — incrementing this re-fetches. */
  refreshKey?: number | string;
}>();

const emit = defineEmits<{
  (e: 'open', node: RecentNode): void;
  /** Right-click on a row. The tray does not build a menu of its own — the
   *  explorer opens the ONE menu every listing row gets (owner: "CONTEXT
   *  MENÜ HER YERDE AYNI OLMALI"), so a file here offers exactly what the
   *  same file offers in its folder. */
  (e: 'context', node: RecentNode, ev: MouseEvent): void;
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
      credentials: props.authCredentials ?? 'same-origin',
    });
    if (res.ok) {
      const body = await res.json();
      // ⚠ `nodes` is what the endpoint actually answers with; this read `entries`
      // only, so the tray was empty on every server that ever served it.
      items.value = Array.isArray(body.nodes)
        ? body.nodes
        : Array.isArray(body.entries)
          ? body.entries
          : Array.isArray(body)
            ? body
            : [];
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
      <button
        class="filex-recent-refresh"
        type="button"
        title="Refresh"
        aria-label="Refresh"
        @click="load"
        :disabled="loading"
      >
        <!-- ikon:emoji — ↻ was a bare arrow glyph with no label at all, so
             the only thing naming this button was its shape. Same mark as the
             toolbar's Refresh now, and it says what it is. -->
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span aria-hidden="true" v-html="actionIconSvg('refresh')"></span>
      </button>
    </header>

    <ul v-if="items.length">
      <li v-for="n in items" :key="n.id" data-testid="recent-tray-item" :data-fe-path="n.path">
        <button
          type="button"
          class="filex-recent-item"
          @click="emit('open', n)"
          @contextmenu.prevent="emit('context', n, $event)"
        >
          <span class="filex-recent-name">{{ n.name }}</span>
          <span class="filex-recent-meta">{{ fmtTime(n.last_opened) }}</span>
        </button>
      </li>
    </ul>
    <p v-else-if="!loading" class="filex-recent-empty">Nothing here yet.</p>
    <p v-else class="filex-recent-empty">Loading…</p>
  </div>
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
  display: inline-flex;
  align-items: center;
  justify-content: center;
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
