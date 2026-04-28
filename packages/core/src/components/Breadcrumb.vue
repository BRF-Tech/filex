<script setup lang="ts">
/**
 * Breadcrumb — adapter-aware path crumbs.
 *
 * Dirname arrives as `brf://fileman/foo/bar`. We strip the adapter and
 * root so the user sees `foo › bar`; clicking any crumb emits the full
 * adapter-qualified path back so the caller can `index()` that folder.
 *
 * The ✏ button swaps the crumbs for a free-form input; Enter navigates,
 * Escape cancels.
 */
import { computed, nextTick, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  dirname: string;
  adapter: string;
  rootLabel: string;
  locale: LocaleCode;
}>();

const { t } = useLocale(() => props.locale);

const emit = defineEmits<{
  (e: 'navigate', adapterPath: string): void;
  (e: 'copy-path', adapterPath: string): void;
  (e: 'crumb-context', payload: { x: number; y: number; adapterPath: string; label: string }): void;
  (e: 'crumb-drop', adapterPath: string, ev: DragEvent): void;
}>();

interface Crumb {
  label: string;
  adapterPath: string;
}

const crumbs = computed<Crumb[]>(() => {
  const adapterPrefix = `${props.adapter}://`;
  const raw = props.dirname.startsWith(adapterPrefix)
    ? props.dirname.slice(adapterPrefix.length)
    : props.dirname;
  const parts = raw.split('/').filter(Boolean);

  if (parts.length === 0) return [];

  const out: Crumb[] = [];
  let acc = '';
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part;
    let label: string;
    if (out.length === 0) {
      label = props.rootLabel;
    } else if (part === '.trash') {
      label = t('node.trash');
    } else {
      label = part;
    }
    out.push({ label, adapterPath: `${adapterPrefix}${acc}` });
  }
  return out;
});

function onClick(crumb: Crumb) {
  emit('navigate', crumb.adapterPath);
}

function onContext(ev: MouseEvent, crumb: Crumb) {
  ev.preventDefault();
  ev.stopPropagation();
  emit('crumb-context', {
    x: ev.clientX,
    y: ev.clientY,
    adapterPath: crumb.adapterPath,
    label: crumb.label,
  });
}

function onCrumbDragOver(ev: DragEvent) {
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
}

function onCrumbDrop(ev: DragEvent, crumb: Crumb) {
  if (!ev.dataTransfer?.types.includes('application/x-brf-files')) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('crumb-drop', crumb.adapterPath, ev);
}

const editing = ref(false);
const pathDraft = ref('');
const pathInput = ref<HTMLInputElement | null>(null);

function startEdit() {
  const adapterPrefix = `${props.adapter}://`;
  pathDraft.value = props.dirname.startsWith(adapterPrefix)
    ? props.dirname.slice(adapterPrefix.length)
    : props.dirname;
  editing.value = true;
  void nextTick(() => {
    pathInput.value?.focus();
    pathInput.value?.select();
  });
}

function submitPath() {
  const v = pathDraft.value.trim().replace(/^\/+|\/+$/g, '');
  editing.value = false;
  if (!v) return;
  emit('navigate', `${props.adapter}://${v}`);
}

function cancelEdit() {
  editing.value = false;
}
</script>

<template>
  <nav class="fe-breadcrumb" aria-label="Breadcrumb">
    <template v-if="!editing">
      <button
        v-for="(c, i) in crumbs"
        :key="c.adapterPath"
        class="fe-breadcrumb__crumb"
        :class="{ 'is-last': i === crumbs.length - 1 }"
        type="button"
        :aria-current="i === crumbs.length - 1 ? 'page' : undefined"
        @click="onClick(c)"
        @contextmenu="onContext($event, c)"
        @dragover="onCrumbDragOver"
        @drop="onCrumbDrop($event, c)"
      >
        <span>{{ c.label }}</span>
        <span v-if="i < crumbs.length - 1" class="fe-breadcrumb__sep" aria-hidden="true">›</span>
      </button>
      <button
        type="button"
        class="fe-breadcrumb__edit"
        :title="t('breadcrumb.go_to_path')"
        :aria-label="t('breadcrumb.go_to_path')"
        @click="startEdit"
      >✏</button>
    </template>
    <template v-else>
      <input
        ref="pathInput"
        v-model="pathDraft"
        type="text"
        class="fe-breadcrumb__input"
        :placeholder="t('breadcrumb.path_placeholder')"
        spellcheck="false"
        autocomplete="off"
        @keydown.enter.prevent="submitPath"
        @keydown.escape.prevent="cancelEdit"
        @blur="cancelEdit"
      />
    </template>
  </nav>
</template>
