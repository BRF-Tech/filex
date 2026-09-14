<script setup lang="ts">
/**
 * TagPicker — small chip list with add/remove for a node.
 *
 * Reads `GET /api/files/manager/tags?node_id=…` on mount, writes the
 * full new array via `POST /api/files/manager/tags`.
 *
 * Tag colors are deterministic: hash(name) → palette index. Consumers
 * can override with the `palette` prop.
 *
 * === etiket:t1 — THE CHIP IS A DOOR ==================================
 * ⚠⚠ Owner, 2026-09-13: "taglediğim dosya klasör tag'ine gitmiyor." The tag
 * VIEW existed and the panel listed every tag, but from a file there was no way
 * into the one it carries: the chip was a `<span>`, so the only route from
 * "this file is tagged invoices" to "what else is tagged invoices" was to read
 * the word, find it again in the panel and click it there.
 *
 * ⚠ The name is the button and the × stays its own. Two targets in one chip,
 * neither of which may fire the other's action — a mis-hit here either loses a
 * tag or navigates away from the file you were reading about.
 *
 * ⚠ It navigates from BOTH mounts, the details panel's and the modal the
 * right-click menu opens. One component, one behaviour: a chip that led
 * somewhere in one place and was inert three inches away in another would be
 * the surface-specific split this package exists to avoid. The host closes the
 * modal on the way (it opened it).
 */
import { ref, watch, onMounted, computed } from 'vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';

const props = defineProps<{
  nodeId: number;
  /**
   * ⚠ Added 2026-09-13 with the chip's new "open this tag" affordance, and it
   * pulls three OLDER strings out of English with it: this component printed
   * "+ Add tag", "tag name" and "Remove tag" literally, so a Turkish user
   * reading "Etiketler" as the section heading got three English controls under
   * it. Optional, defaulting to the catalogue's own default, so an embedder
   * that never passed one is unaffected.
   */
  locale?: LocaleCode;
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  /** Credentials mode, from the explorer's auth kind. ⚠ Defaults to
   *  'same-origin': a credentialed cross-origin request cannot be answered
   *  with ACAO:* , so hardcoding 'include' broke this call in every embed
   *  served from a different origin to the API. */
  authCredentials?: RequestCredentials;
  /** Optional list of palette swatches; one is picked per tag deterministically. */
  palette?: string[];
}>();

const emit = defineEmits<{
  (e: 'change', tags: string[]): void;
  (e: 'error', message: string): void;
  /** etiket:t1 — "show me everything tagged this". The host owns navigation. */
  (e: 'open', tag: string): void;
}>();

const { t } = useLocale(() => props.locale ?? 'en');

/** "Open tag: invoices" — the chip's title and its accessible name, one value
 *  so the two cannot say different things. */
function openTitle(tag: string): string {
  return t('tags.open', { tag });
}

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
      credentials: props.authCredentials ?? 'same-origin',
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
      credentials: props.authCredentials ?? 'same-origin',
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
      <button
        class="filex-tag-open"
        type="button"
        :title="openTitle(tag)"
        :aria-label="openTitle(tag)"
        :data-testid="`tag-open-${tag}`"
        @click="emit('open', tag)"
      >{{ tag }}</button>
      <button class="filex-tag-x" type="button" @click="remove(tag)" :aria-label="t('tags.remove')">×</button>
    </span>

    <form v-if="adding" class="filex-tag-add" @submit.prevent="add">
      <input
        v-model="newTag"
        autofocus
        :placeholder="t('tags.name')"
        :aria-label="t('tags.name')"
        @blur="add"
        @keydown.escape="adding = false; newTag = ''"
      />
    </form>
    <button v-else class="filex-tag-add-btn" type="button" @click="adding = true">
      {{ t('tags.add') }}
    </button>
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
/* etiket:t1 — the tag's NAME is the door to its view. A bare button: it
   inherits the chip's own colour and type so the chip still reads as one
   object, and the underline appears only under the pointer, where the chip has
   already said it is interactive by changing the cursor. */
.filex-tag-open {
  background: transparent;
  border: 0;
  padding: 0;
  margin: 0;
  color: inherit;
  font: inherit;
  line-height: inherit;
  cursor: pointer;
}
.filex-tag-open:hover,
.filex-tag-open:focus-visible {
  text-decoration: underline;
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
/* gorunum:v1 — a host override still wins, but the fallback chain now ends at
   the product blue instead of the old indigo, and it asks the palette first:
   in a themed context this hover follows --fe-primary (and so goes light blue
   in dark mode), and only a context with no stylesheet at all — the very case
   where a fallback is the only thing painting — lands on the literal. */
.filex-tag-add-btn:hover {
  border-color: var(--filex-accent, var(--fe-primary, #2f6ceb));
  color: var(--filex-accent, var(--fe-primary, #2f6ceb));
}
</style>
