<script setup lang="ts">
/**
 * Breadcrumb — adapter-aware path crumbs.
 *
 * Dirname arrives as `<adapter>://<rel>` (e.g. `s3-test://aa/bb`). The
 * first crumb is ALWAYS the storage root — its label is the adapter
 * name itself (`s3-test`) and clicking it lands you at `<adapter>://`,
 * NOT at the first sub-folder you happened to be inside. The remaining
 * crumbs walk down the path one segment at a time.
 *
 * Examples:
 *   `s3-test://`           → [s3-test]
 *   `s3-test://aa`         → [s3-test] › [aa]
 *   `s3-test://aa/bb`      → [s3-test] › [aa] › [bb]
 *
 * `rootLabel` overrides the adapter name when the embedder wants a
 * branded label ('Files', 'My Drive', etc). Defaults to the adapter.
 *
 * The ✏ button swaps the crumbs for a free-form input; Enter
 * navigates, Escape cancels.
 */
import { computed, nextTick, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  dirname: string;
  adapter: string;
  rootLabel: string;
  locale: LocaleCode;
  /**
   * Multi-storage mode — when true the breadcrumb prepends a global
   * "/" crumb so the user can pop all the way back to the storage
   * picker. Storage name then occupies crumb #1 instead of #0.
   */
  multiStorageRoot?: boolean;
  /**
   * Root confinement (qualified `<adapter>://<rel>`). When set the breadcrumb
   * treats this folder as `/`: crumbs above it are hidden, the root crumb is
   * the confined folder's own name, and the ✏ path editor shows/accepts paths
   * relative to it (`/`, `/sub`). Pairs with FileExplorer's rootPath clamp.
   */
  rootPath?: string;
}>();

// Confined root parsed into its adapter-relative form ("projeler/acme") and
// the trailing folder name used as the "/" crumb label.
const confinedRel = computed(() => {
  if (!props.rootPath) return '';
  const r = props.rootPath;
  const i = r.indexOf('://');
  return (i >= 0 ? r.slice(i + 3) : r).replace(/^\/+|\/+$/g, '');
});
const floorLabel = computed(() => {
  const rel = confinedRel.value;
  if (!rel) return props.rootLabel || props.adapter;
  const parts = rel.split('/');
  return parts[parts.length - 1] || (props.rootLabel || props.adapter);
});

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

  const out: Crumb[] = [];

  // Confined: the root folder is the top crumb (its own name), and only the
  // path BELOW it is walked — segments above the confinement are never shown.
  if (props.rootPath) {
    const floor = confinedRel.value;
    const cur = raw.replace(/^\/+|\/+$/g, '');
    const below = floor && (cur === floor || cur.startsWith(floor + '/'))
      ? cur.slice(floor.length).replace(/^\/+/, '')
      : '';
    out.push({ label: floorLabel.value, adapterPath: props.rootPath });
    let acc = floor;
    for (const part of below.split('/').filter(Boolean)) {
      acc = acc ? `${acc}/${part}` : part;
      out.push({ label: part === '.trash' ? t('node.trash') : part, adapterPath: `${adapterPrefix}${acc}` });
    }
    return out;
  }

  if (props.multiStorageRoot) {
    // Global "/" crumb — clicking it pops back to the storage picker
    // (FileExplorer treats the empty string as the virtual root).
    out.push({ label: '/', adapterPath: '' });
    if (props.adapter) {
      // Storage segment — wire form `<adapter>://` lands at storage
      // root regardless of where we are now.
      out.push({ label: props.rootLabel || props.adapter, adapterPath: adapterPrefix });
    }
  } else {
    // Single-storage mode: the storage name IS the root, no "/"
    // above it.
    out.push({
      label: props.rootLabel || props.adapter,
      adapterPath: adapterPrefix,
    });
  }

  let acc = '';
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part;
    const label = part === '.trash' ? t('node.trash') : part;
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
  const rel = props.dirname.startsWith(adapterPrefix)
    ? props.dirname.slice(adapterPrefix.length)
    : props.dirname;
  // Confined: edit relative to the root folder — `/` is the root, `/sub` below.
  if (props.rootPath) {
    const floor = confinedRel.value;
    const cur = rel.replace(/^\/+|\/+$/g, '');
    const below = floor && (cur === floor || cur.startsWith(floor + '/'))
      ? cur.slice(floor.length).replace(/^\/+/, '')
      : '';
    pathDraft.value = below ? `/${below}` : '/';
    editing.value = true;
    void nextTick(() => {
      pathInput.value?.focus();
      pathInput.value?.select();
    });
    return;
  }
  // Multi-storage: show `/<storage>/<rel>` so the user can edit the
  // whole tree (`/main/foo`, `/s3-test/example`, just `/` for root).
  if (props.multiStorageRoot) {
    if (!props.adapter) {
      pathDraft.value = '/';
    } else {
      const trimmed = rel.replace(/^\/+|\/+$/g, '');
      pathDraft.value = trimmed
        ? `/${props.adapter}/${trimmed}`
        : `/${props.adapter}`;
    }
  } else {
    pathDraft.value = rel;
  }
  editing.value = true;
  void nextTick(() => {
    pathInput.value?.focus();
    pathInput.value?.select();
  });
}

function submitPath() {
  const raw = pathDraft.value.trim();
  editing.value = false;

  // Confined: the typed path is relative to the root folder; prepend the floor.
  if (props.rootPath) {
    const sub = raw.replace(/^\/+|\/+$/g, '');
    const floor = confinedRel.value;
    const rel = sub ? (floor ? `${floor}/${sub}` : sub) : floor;
    emit('navigate', rel ? `${props.adapter}://${rel}` : `${props.adapter}://`);
    return;
  }

  if (props.multiStorageRoot) {
    // Strip leading/trailing slashes. First segment = storage.
    const clean = raw.replace(/^\/+|\/+$/g, '');
    if (!clean) {
      // navigate to global root by emitting empty wire path; the
      // FileExplorer treats it as virtual storage list.
      emit('navigate', '');
      return;
    }
    const slash = clean.indexOf('/');
    const adapter = slash === -1 ? clean : clean.slice(0, slash);
    const rel = slash === -1 ? '' : clean.slice(slash + 1);
    emit('navigate', rel ? `${adapter}://${rel}` : `${adapter}://`);
    return;
  }

  const v = raw.replace(/^\/+|\/+$/g, '');
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
