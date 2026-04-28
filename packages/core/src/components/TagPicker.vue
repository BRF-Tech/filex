<script setup lang="ts">
/**
 * TagPicker — small chip list with add/remove for a node.
 *
 * Reads `GET /api/files/manager/tags?node_id=…` on mount, writes the
 * full new array via `POST /api/files/manager/tags`.
 *
 * Tag colors are deterministic: hash(name) → palette index. Consumers
 * can override with the `palette` prop.
 */
import { ref, watch, onMounted, computed } from 'vue';

const props = defineProps<{
  nodeId: number;
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  /** Optional list of palette swatches; one is picked per tag deterministically. */
  palette?: string[];
}>();

const emit = defineEmits<{
  (e: 'change', tags: string[]): void;
  (e: 'error', message: string): void;
}>();

const tags = ref<string[]>([]);
const loading = ref(false);
const adding = ref(false);
const newTag = ref('');

const palette = computed(() => props.palette ?? [
  '#ef4444', '#f59e0b', '#10b981', '#3b82f6',
  '#8b5cf6', '#ec4899', '#14b8a6', '#f97316',
]);

function colorFor(tag: string): string {
  let h = 0;
  for (let i = 0; i < tag.length; i++) h = (h * 31 + tag.charCodeAt(i)) | 0;
  const arr = palette.value;
  return arr[Math.abs(h) % arr.length];
}

async function buildHeaders(extra: Record<string, string> = {}): Promise<Record<string, string>> {
  return { ...(await (props.authHeaders ?? (() => ({})))()), ...extra };
}

async function load() {
  loading.value = true;
  try {
    const base = props.apiBase ?? '';
    const res = await fetch(`${base}/api/files/manager/tags?node_id=${props.nodeId}`, {
      headers: await buildHeaders(),
      credentials: 'include',
    });
    if (res.ok) {
      const body = await res.json();
      tags.value = Array.isArray(body.tags) ? body.tags : [];
    }
  } catch (err) {
    emit('error', err instanceof Error ? err.message : String(err));
  } finally {
    loading.value = false;
  }
}

async function persist(next: string[]) {
  const previous = [...tags.value];
  tags.value = next;
  try {
    const base = props.apiBase ?? '';
    const res = await fetch(`${base}/api/files/manager/tags`, {
      method: 'POST',
      headers: await buildHeaders({ 'Content-Type': 'application/json' }),
      credentials: 'include',
      body: JSON.stringify({ node_id: props.nodeId, tags: next }),
    });
    if (!res.ok) throw new Error(`tag save failed: ${res.status}`);
    emit('change', next);
  } catch (err) {
    tags.value = previous;
    emit('error', err instanceof Error ? err.message : String(err));
  }
}

function add() {
  const v = newTag.value.trim();
  if (!v || tags.value.includes(v)) {
    newTag.value = '';
    adding.value = false;
    return;
  }
  persist([...tags.value, v]);
  newTag.value = '';
  adding.value = false;
}

function remove(tag: string) {
  persist(tags.value.filter((t) => t !== tag));
}

onMounted(load);
watch(() => props.nodeId, load);
</script>

<template>
  <div class="filex-tag-picker" :class="{ 'is-loading': loading }">
    <span
      v-for="tag in tags"
      :key="tag"
      class="filex-tag"
      :style="{ '--filex-tag-color': colorFor(tag) }"
    >
      {{ tag }}
      <button class="filex-tag-x" type="button" @click="remove(tag)" aria-label="Remove tag">×</button>
    </span>

    <form v-if="adding" class="filex-tag-add" @submit.prevent="add">
      <input
        v-model="newTag"
        autofocus
        placeholder="tag name"
        @blur="add"
        @keydown.escape="adding = false; newTag = ''"
      />
    </form>
    <button v-else class="filex-tag-add-btn" type="button" @click="adding = true">+ Add tag</button>
  </div>
</template>

<style scoped>
.filex-tag-picker {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.filex-tag-picker.is-loading {
  opacity: 0.6;
  pointer-events: none;
}
.filex-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border-radius: 999px;
  padding: 2px 10px;
  font-size: 12px;
  font-weight: 500;
  color: #fff;
  background: var(--filex-tag-color);
}
.filex-tag-x {
  background: transparent;
  border: none;
  color: rgba(255, 255, 255, 0.85);
  cursor: pointer;
  padding: 0 2px;
  font-size: 14px;
  line-height: 1;
}
.filex-tag-x:hover {
  color: #fff;
}
.filex-tag-add input {
  border: 1px solid var(--filex-border, #d1d5db);
  border-radius: 12px;
  padding: 2px 10px;
  font-size: 12px;
  width: 110px;
}
.filex-tag-add-btn {
  border: 1px dashed var(--filex-border, #d1d5db);
  background: transparent;
  border-radius: 999px;
  padding: 2px 10px;
  font-size: 12px;
  color: var(--filex-text-muted, #6b7280);
  cursor: pointer;
}
.filex-tag-add-btn:hover {
  border-color: var(--filex-accent, #6366f1);
  color: var(--filex-accent, #6366f1);
}
</style>
