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
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { hasInternalDrag } from '../lib/dragOut';
import { virtualSegmentLabel } from '../lib/listing';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons';

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
  /**
   * gorunum:v2-topbar — what is INSIDE the folder the last crumb names, for
   * the chevron the reference shell puts there ("Subfolders"). Supplied by
   * the parent, because the listing it comes from is the parent's: a second
   * fetch from here would be a second answer to "what is in this folder".
   *
   * ⚠ The affordance is drawn whenever the array is PRESENT, empty or not —
   * a control that appears and disappears as you walk the tree is a control
   * nobody learns. An empty folder says so in the menu instead.
   */
  subfolders?: { label: string; adapterPath: string }[];
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
  /* ⚠ Empty adapter → EMPTY prefix, not a bare `://`. A virtual view
     (`.starred`, `.tag~invoices`) belongs to no storage, so the pane hands us
     the sentinel with no adapter at all; gluing `://` onto it would make every
     crumb's `adapterPath` read `://.starred`, which navigates nowhere — the
     view's own address is the bare sentinel. */
  const adapterPrefix = props.adapter ? `${props.adapter}://` : '';
  const raw = adapterPrefix && props.dirname.startsWith(adapterPrefix)
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
      /* ⚠ THE SAME resolver the unconfined branch below uses. This line used to
         carry a private `part === '.trash' ? t('node.trash') : part` — a second
         copy of the sentinel mapping that knew about exactly one sentinel, so a
         confined embed printed `.starred`, `.shared`, `.recent` and
         `.tag~invoices` raw while the panel three inches away named them
         properly. One resolver, and a sentinel added to `lib/listing` reaches
         both branches on the day it lands. */
      out.push({ label: virtualSegmentLabel(part, t) || part, adapterPath: `${adapterPrefix}${acc}` });
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
  } else if (props.adapter || props.rootLabel) {
    // Single-storage mode: the storage name IS the root, no "/"
    // above it.
    // ⚠ …and with NEITHER a storage nor a branded label there is nothing to
    // name: that is a virtual view in a single-storage explorer, where the
    // crumb would otherwise be an empty, clickable box in front of "Starred".
    out.push({
      label: props.rootLabel || props.adapter,
      adapterPath: adapterPrefix,
    });
  }

  let acc = '';
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part;
    // gezinti:g1 — the sentinel segments the virtual views park in `dirname`
    // (`.trash` predates them). Without a mapping the crumb reads ".starred",
    // which is a filename the user never typed and cannot navigate to.
    // etiket:t1 — via the shared resolver, which also knows the tag view's
    // `.tag~<name>` segment; the raw map only covers the fixed-label views.
    const label = virtualSegmentLabel(part, t) || part;
    out.push({ label, adapterPath: `${adapterPrefix}${acc}` });
  }
  return out;
});

// ------------------------------------------------------------------
// Overflow collapse (cila:c / I5) — when the trail grows past the
// threshold, show `root › … › parent › current`; the middle crumbs move
// into a dropdown. Visible crumbs keep their full click/drag-drop/context
// behavior; dropdown entries only navigate.
// ------------------------------------------------------------------
/* gorunum:v2-topbar — the home crumb.
 *
 * ⚠ It REPLACES the root crumb, it is not added beside it. In multi-storage
 * mode `crumbs[0]` is a bare "/" whose only job is "back to the storage
 * list" — exactly what a house glyph says, and better than a slash nobody
 * reads as a button. Drawing both would be two controls for one destination,
 * which is the duplicate this pass exists to remove.
 * ⚠ Only that "/" crumb is swallowed. A single-storage explorer's first crumb
 * is the storage's NAME and a confined one's is the folder's — information a
 * tooltip on a house cannot carry, so those keep their text. */
const homeCrumb = computed<Crumb | null>(() => {
  const first = crumbs.value[0];
  if (!first) return null;
  return props.multiStorageRoot && first.adapterPath === '' ? first : null;
});
/** The crumbs drawn as text — everything the home button did not swallow. */
const trail = computed<Crumb[]>(() =>
  homeCrumb.value ? crumbs.value.slice(1) : crumbs.value,
);
/** True while the home button is the current folder (nothing below it). */
const homeIsCurrent = computed(() => !!homeCrumb.value && trail.value.length === 0);

const OVERFLOW_THRESHOLD = 4;
const collapsed = computed(() => trail.value.length > OVERFLOW_THRESHOLD);
const leadCrumbs = computed<Crumb[]>(() =>
  collapsed.value ? trail.value.slice(0, 1) : trail.value,
);
const middleCrumbs = computed<Crumb[]>(() =>
  collapsed.value ? trail.value.slice(1, -2) : [],
);
const tailCrumbs = computed<Crumb[]>(() =>
  collapsed.value ? trail.value.slice(-2) : [],
);

const overflowOpen = ref(false);
const moreWrapEl = ref<HTMLElement | null>(null);
/* gorunum:v2-topbar — the "Subfolders" chevron's own menu. Separate state
   from the overflow "…", because the two open different lists (crumbs ABOVE
   this folder vs folders INSIDE it) and only ever one at a time. */
const subOpen = ref(false);
const subWrapEl = ref<HTMLElement | null>(null);

const anyMenuOpen = computed(() => overflowOpen.value || subOpen.value);

function closeMenus() {
  overflowOpen.value = false;
  subOpen.value = false;
}

function onOverflowPick(crumb: Crumb) {
  closeMenus();
  emit('navigate', crumb.adapterPath);
}

function toggleSub() {
  overflowOpen.value = false;
  subOpen.value = !subOpen.value;
}

function onDocClick(e: MouseEvent) {
  const t = e.target;
  if (!(t instanceof Node)) {
    closeMenus();
    return;
  }
  if (moreWrapEl.value?.contains(t) || subWrapEl.value?.contains(t)) return;
  closeMenus();
}

function onDocKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation();
    closeMenus();
  }
}

watch(anyMenuOpen, (v) => {
  if (v) {
    document.addEventListener('click', onDocClick, true);
    document.addEventListener('keydown', onDocKeydown, true);
  } else {
    document.removeEventListener('click', onDocClick, true);
    document.removeEventListener('keydown', onDocKeydown, true);
  }
});
// Navigation rebuilds the trail — a stale open menu would list crumbs of
// the previous folder (and subfolders of the one we just left).
watch(crumbs, closeMenus);
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick, true);
  document.removeEventListener('keydown', onDocKeydown, true);
});

function onClick(crumb: Crumb) {
  emit('navigate', crumb.adapterPath);
}

/* The home button's three handlers, null-guarded here rather than in the
 * template: `v-if="homeCrumb"` does not narrow the ref inside an event
 * binding, and a `!` in markup is an assertion no reader can check. */
function onHomeClick() {
  if (homeCrumb.value) onClick(homeCrumb.value);
}
function onHomeContext(ev: MouseEvent) {
  if (homeCrumb.value) onContext(ev, homeCrumb.value);
}
function onHomeDrop(ev: DragEvent) {
  if (homeCrumb.value) onCrumbDrop(ev, homeCrumb.value);
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
  if (!hasInternalDrag(ev)) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
}

function onCrumbDrop(ev: DragEvent, crumb: Crumb) {
  if (!hasInternalDrag(ev)) return;
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
      <!-- gorunum:v2-topbar — the root crumb. It IS `crumbs[0]` (see the
           script), drawn as a house instead of a "/" nobody reads as a
           button; it is never an extra control beside the root.

           ⚠⚠ It is named `breadcrumb.root`, NOT `breadcrumb.home`, and that
           was a real collision rather than a synonym. This button goes to
           `adapterPath: ''` — the top of the tree: the DRIVES listing where
           several storages are visible, the storage root where one is. The
           panel's own "Home" row goes somewhere else entirely (the overview
           with Storages · Recent · Starred). Two controls, one word, two
           destinations — and the word belonged to the other one. It mattered
           more from 2026-09-13, when "My files" stopped being drawn for a
           multi-storage deployment: this button became the only door to that
           listing, so the one thing it must not do is point at a different
           row in the panel.

           `breadcrumb.root` rather than a new string because the product
           ALREADY names this destination: FileExplorer labels the tab you
           land on with exactly this key ("Root" / "Kök"). Hovering the house
           and reading the tab now say the same word instead of two.

           ⚠ Relabelled, NOT repointed. Sending it to the Home overview would
           break two things that are measured: `crumbs[0]` is this crumb, and
           `trail` is `crumbs.slice(1)`, so the root would lose its only crumb
           and (post-"My files") its last door; and `goUp()`/Alt+↑ from a
           storage root loads `''`, so Up and the crumb would then disagree
           about what sits above a storage.
           ⚠ `data-testid` stays `breadcrumb-home`: it is a selector other
           surfaces already use, and renaming it buys nothing. -->
      <button
        v-if="homeCrumb"
        type="button"
        class="fe-breadcrumb__crumb fe-breadcrumb__home"
        :class="{ 'is-last': homeIsCurrent }"
        :aria-current="homeIsCurrent ? 'page' : undefined"
        :title="t('breadcrumb.root')"
        :aria-label="t('breadcrumb.root')"
        data-testid="breadcrumb-home"
        @click="onHomeClick"
        @contextmenu="onHomeContext"
        @dragover="onCrumbDragOver"
        @drop="onHomeDrop"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('home')"></span>
        <span v-if="!homeIsCurrent" class="fe-breadcrumb__sep" aria-hidden="true">›</span>
      </button>
      <button
        v-for="(c, i) in leadCrumbs"
        :key="c.adapterPath"
        class="fe-breadcrumb__crumb"
        :class="{ 'is-last': !collapsed && i === leadCrumbs.length - 1 }"
        type="button"
        :aria-current="!collapsed && i === leadCrumbs.length - 1 ? 'page' : undefined"
        @click="onClick(c)"
        @contextmenu="onContext($event, c)"
        @dragover="onCrumbDragOver"
        @drop="onCrumbDrop($event, c)"
      >
        <span>{{ c.label }}</span>
        <span v-if="collapsed || i < leadCrumbs.length - 1" class="fe-breadcrumb__sep" aria-hidden="true">›</span>
      </button>
      <span v-if="middleCrumbs.length" ref="moreWrapEl" class="fe-breadcrumb__more-wrap">
        <button
          type="button"
          class="fe-breadcrumb__more"
          aria-haspopup="menu"
          :aria-expanded="overflowOpen"
          :aria-label="t('breadcrumb.more')"
          :title="t('breadcrumb.more')"
          @click="overflowOpen = !overflowOpen"
        >…</button>
        <span class="fe-breadcrumb__sep" aria-hidden="true">›</span>
        <div v-if="overflowOpen" class="fe-breadcrumb__menu" role="menu">
          <button
            v-for="c in middleCrumbs"
            :key="c.adapterPath"
            type="button"
            class="fe-breadcrumb__menu-item"
            role="menuitem"
            @click="onOverflowPick(c)"
          >{{ c.label }}</button>
        </div>
      </span>
      <button
        v-for="(c, i) in tailCrumbs"
        :key="c.adapterPath"
        class="fe-breadcrumb__crumb"
        :class="{ 'is-last': i === tailCrumbs.length - 1 }"
        type="button"
        :aria-current="i === tailCrumbs.length - 1 ? 'page' : undefined"
        @click="onClick(c)"
        @contextmenu="onContext($event, c)"
        @dragover="onCrumbDragOver"
        @drop="onCrumbDrop($event, c)"
      >
        <span>{{ c.label }}</span>
        <span v-if="i < tailCrumbs.length - 1" class="fe-breadcrumb__sep" aria-hidden="true">›</span>
      </button>
      <!-- gorunum:v2-topbar — "Subfolders": the chevron the reference shell
           hangs on the LAST crumb, opening what is inside the folder you are
           standing in. Drawn whenever the parent supplies the list, empty or
           not (see the prop's note). -->
      <span v-if="subfolders" ref="subWrapEl" class="fe-breadcrumb__sub-wrap">
        <button
          type="button"
          class="fe-breadcrumb__sub"
          aria-haspopup="menu"
          :aria-expanded="subOpen"
          :title="t('breadcrumb.subfolders')"
          :aria-label="t('breadcrumb.subfolders')"
          data-testid="breadcrumb-subfolders"
          @click="toggleSub"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('subfolders')"></span>
        </button>
        <div v-if="subOpen" class="fe-breadcrumb__menu" role="menu">
          <button
            v-for="c in subfolders"
            :key="c.adapterPath"
            type="button"
            class="fe-breadcrumb__menu-item"
            role="menuitem"
            @click="onOverflowPick(c)"
          >{{ c.label }}</button>
          <p v-if="!subfolders.length" class="fe-breadcrumb__menu-empty">
            {{ t('breadcrumb.subfolders.empty') }}
          </p>
        </div>
      </span>
      <!-- gorunum:v1 — "type a path and go". The glyph was the ✏ emoji, which
           this row draws from whichever emoji font the machine has: on a
           headless Chromium and on a Linux box without one it comes out as a
           stray dash beside the crumbs. An inline SVG is the same mark
           everywhere and inherits the row's colour.
           gorunum:v2-topbar — the mark now comes from lib/actionIcons with
           every other glyph in the row; the reference shell has no path
           editor at all, so this is ours and it stays. -->
      <button
        type="button"
        class="fe-breadcrumb__edit"
        :title="t('breadcrumb.go_to_path')"
        :aria-label="t('breadcrumb.go_to_path')"
        data-testid="breadcrumb-edit-path"
        @click="startEdit"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('edit-path')"></span>
      </button>
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
