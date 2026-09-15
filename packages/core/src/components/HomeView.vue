<script setup lang="ts">
/**
 * HomeView — the overview the explorer lands on, INSIDE the shell.
 *
 * Three blocks, each answering a question somebody actually has on arrival:
 *
 *   Storages  which drives are mine to open?   (one card per storage)
 *   Recent    what was I just working on?
 *   Starred   what did I mark to come back to?
 *
 * ⚠⚠ Why it is in this package and not in the web app. It used to be a
 * separate PAGE there (`web/src/views/Home.vue`) with a header of its own — a
 * logo, a refresh, an "All files" button, an apps grid, sign-out and a
 * language switch — and no navigation panel at all, so moving between Home and
 * the files replaced the whole screen and half of that header repeated a
 * control the explorer already drew. Home is a VIEW, the way Recent and
 * Starred are: same panel, same top bar, only the content area changes. A view
 * of the explorer belongs to the explorer, and putting it here is also what
 * makes the panel's "Home" row mean something inside `<filex-explorer>`
 * instead of being a destination that only exists on one surface.
 *
 * ⚠ Recent and Starred render through GridView — the SAME card, with the same
 * thumbnails, the same type tiles and the same right-click menu as the listing
 * behind it. A second card implementation for the same object is how the two
 * start disagreeing about what a .docx looks like (it already happened once:
 * the web page deep-imported `fileIcons` precisely to avoid it).
 *
 * ⚠ No `<style>` block, scoped or otherwise — the package's CSS lives in
 * `styles/base.css`. A scoped block compiles to `.cls[data-v-HASH]` and the
 * hash does not match in the web-component build, so the rules would silently
 * stop applying in every embed.
 */
import { computed, ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconTile } from '../lib/fileIcons';
import { nameMatches } from '../lib/fileFilters';
import GridView from './GridView.vue';

export interface HomeStorage {
  name: string;
  label?: string;
  driver?: string;
  readOnly?: boolean;
  /** Bytes this storage holds, when the server reported one. */
  usedBytes?: number;
}

const props = defineProps<{
  storages: HomeStorage[];
  recent: FileNode[];
  starred: FileNode[];
  /** True while the two per-user lists are still in flight. */
  loading?: boolean;
  locale: LocaleCode;
  /**
   * surucu:d1-scope — what the filter row above this view has in its name box.
   *
   * ⚠ It narrows ALL THREE blocks, not the two made of files. Home's first
   * block is the drive list, and "orada adam isterse storage ismi aratabilir"
   * is precisely the case the owner asked for; a box that skipped the block
   * with the fewest, longest-lived names in it would be the box that misses
   * the one thing people look for here.
   *
   * ⚠ Through `lib/fileFilters`'s own `nameMatches`, which is the predicate the
   * listing's box uses — so an accent folds here exactly as it folds there.
   * Optional: an embedder that draws no filter row passes nothing and sees
   * everything, which is what this view has always done.
   */
  nameFilter?: string;
  /** Authenticated thumb resolver (useThumbs.src) — the grid needs it in an
   *  embed, where a bare root-relative `thumb_url` is not authenticated. */
  thumbSrc?: (n: FileNode) => string | null;
}>();

const emit = defineEmits<{
  (e: 'open-storage', name: string): void;
  (e: 'open-node', node: FileNode): void;
  (e: 'context-node', node: FileNode, ev: MouseEvent): void;
}>();

const { t, formatSize } = useLocale(() => props.locale);

/* surucu:d1-scope — the three narrowed blocks.
 *
 * ⚠ A storage is matched on the word ON THE CARD (`label || name`), which is
 * also the only word a person can see to type. A card labelled "Arşiv" over a
 * storage named `s3-archive` must answer to "arş"; matching the wire name
 * instead would make the filter miss the thing it is pointing at.
 */
const needle = computed(() => (props.nameFilter ?? '').trim());
const filtering = computed(() => needle.value !== '');
const shownStorages = computed(() =>
  props.storages.filter((s) => nameMatches(s.label || s.name, needle.value)),
);
const shownRecent = computed(() =>
  props.recent.filter((n) => nameMatches(n.basename || '', needle.value)),
);
const shownStarred = computed(() =>
  props.starred.filter((n) => nameMatches(n.basename || '', needle.value)),
);
/** True when a block has rows but the filter hid all of them — a different
 *  fact from "you have starred nothing", and it must not borrow that sentence. */
function filteredEmpty(total: number, shown: number): boolean {
  return filtering.value && total > 0 && shown === 0;
}

/**
 * ⚠ One frozen empty set, not `new Set()` in the template. GridView takes the
 * selection as a prop and a fresh Set on every render is a new identity every
 * time, which re-runs everything downstream of it for a value that never
 * changes. Nothing on Home is selectable: there is no selection bar, no
 * checkbox column and no verb to apply — so the honest answer is "nothing",
 * once.
 */
const NO_SELECTION: Set<string> = new Set();

/**
 * The storage card's glyph.
 *
 * ⚠ `mime_type: 'inode/storage'` is the whole point of this line. A storage row
 * IS `type: 'dir'`, so asking for `{ type: 'dir' }` alone falls through to the
 * plain folder branch and every drive on the first screen a person sees — the
 * screen with the most drives on it — came out as a yellow folder. `storage` is
 * a real icon family now (lib/fileIcons answers it BEFORE the folder branch),
 * and it is the two-bay drive the navigation panel already draws for the same
 * storages: one shape for a drive across the panel, the list, the grid and here.
 *
 * One constant rather than a per-card call: every storage resolves to the same
 * family, so this is one string, not one per row.
 */
const storageTile = fileIconTile({ type: 'dir', mime_type: 'inode/storage' });

/**
 * What a storage card says under its name.
 *
 * ⚠ A size only when the server reported one, and it is the same quantity for
 * everybody who gets it (`/api/admin/storages` for an operator, the
 * RBAC-filtered `/api/files/quota/storages` for everybody else). The per-USER
 * quota is a sum across every storage, so printing that here would be a number
 * about the person under a label about the drive. With no figure the caption
 * names the kind of thing instead: a card with no size line says less; a card
 * with the wrong size line says something false.
 */
function storageCaption(s: HomeStorage): string {
  return typeof s.usedBytes === 'number' && s.usedBytes >= 0
    ? t('drive.storage.used_unlimited', { used: formatSize(s.usedBytes) })
    : t('conn.guide.storage');
}

/**
 * A card opens what it names.
 *
 * A single click, exactly as in the listing behind it (issue #26: a click
 * anywhere but the checkbox opens). Home's cards carry no checkbox at all —
 * there is no selection here to tick anything into (see NO_SELECTION).
 *
 * ⚠ Which is why the double click has to be swallowed: a real double click
 * fires `click-card` twice, and two opens of a file are two viewer tabs. The
 * guard is on the NODE, not on a timer alone — opening the same thing twice in
 * a moment is the accident; opening two different things quickly is somebody
 * working fast.
 */
const lastOpen = ref<{ path: string; at: number }>({ path: '', at: 0 });
function openNode(n: FileNode) {
  const now = Date.now();
  if (n.path === lastOpen.value.path && now - lastOpen.value.at < 700) return;
  lastOpen.value = { path: n.path, at: now };
  emit('open-node', n);
}
</script>

<template>
  <!-- ⚠ No title of its own, and as of 2026-09-13 nobody else draws one either.
       The explorer used to render "Home" into the breadcrumb row above this;
       the owner had it removed ("ver sayfa içinde salak bir Home yazısı var,
       onu kaldıralım") because the panel row, the tab and the address bar
       already say it three times over. The section headings below stay — they
       name the blocks, not the page. -->
  <div class="fe-home" data-testid="home-view">
    <!-- ── Storages ──────────────────────────────────────────────────── -->
    <section class="fe-home__section" data-testid="home-storages">
      <h2 class="fe-home__heading">{{ t('sidenav.storages') }}</h2>

      <div v-if="shownStorages.length" class="fe-home__storages">
        <button
          v-for="s in shownStorages"
          :key="s.name"
          type="button"
          class="fe-home__storage"
          data-testid="home-storage-card"
          :data-storage="s.name"
          :title="s.label || s.name"
          @click="emit('open-storage', s.name)"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span class="fe-home__storage-tile" aria-hidden="true" v-html="storageTile"></span>
          <span class="fe-home__storage-main">
            <span class="fe-home__storage-label">{{ s.label || s.name }}</span>
            <span class="fe-home__storage-meta">{{ storageCaption(s) }}</span>
          </span>
        </button>
      </div>

      <!-- ⚠ The filter hid them — NOT "no storages yet". Two different facts,
           and borrowing the second sentence for the first tells an operator
           their drives are gone. One row per block, `data-testid` included, so
           a test can tell the two apart too. -->
      <p
        v-else-if="filteredEmpty(storages.length, shownStorages.length)"
        class="fe-home__nomatch"
        data-testid="home-storages-nomatch"
      >
        {{ t('filter.empty.title') }}
      </p>

      <div v-else class="fe-state fe-home__state" data-testid="home-storages-empty">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="96"
          height="80"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <rect x="26" y="30" width="68" height="18" rx="5" />
          <rect x="26" y="54" width="68" height="18" rx="5" />
          <path d="M38 39h.01M38 63h.01" />
        </svg>
        <p class="fe-state__title">{{ t('home.storages.empty') }}</p>
        <p class="fe-state__hint">{{ t('home.storages.hint') }}</p>
      </div>
    </section>

    <!-- ── Recent ────────────────────────────────────────────────────── -->
    <section class="fe-home__section" data-testid="home-recent">
      <h2 class="fe-home__heading">{{ t('sidenav.recent') }}</h2>

      <GridView
        v-if="shownRecent.length"
        :files="shownRecent"
        :selected="NO_SELECTION"
        :selectable="false"
        :locale="locale"
        :thumb-src="thumbSrc"
        @click-card="openNode"
        @dbl-card="openNode"
        @context-card="(n: FileNode, ev: MouseEvent) => emit('context-node', n, ev)"
      />
      <!-- ⚠ A block still loading says so, and must NOT fall through to the
           empty state: every visit would otherwise flash "Nothing starred yet"
           at somebody who has starred things. -->
      <p v-else-if="loading" class="fe-home__loading">{{ t('loading') }}</p>
      <p
        v-else-if="filteredEmpty(recent.length, shownRecent.length)"
        class="fe-home__nomatch"
        data-testid="home-recent-nomatch"
      >
        {{ t('filter.empty.title') }}
      </p>
      <div v-else class="fe-state fe-home__state" data-testid="home-recent-empty">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="96"
          height="80"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="60" cy="50" r="28" />
          <path d="M60 32v18l12 8" />
        </svg>
        <p class="fe-state__title">{{ t('empty.recent.title') }}</p>
        <p class="fe-state__hint">{{ t('empty.recent.hint') }}</p>
      </div>
    </section>

    <!-- ── Starred ───────────────────────────────────────────────────── -->
    <section class="fe-home__section" data-testid="home-starred">
      <h2 class="fe-home__heading">{{ t('sidenav.starred') }}</h2>

      <GridView
        v-if="shownStarred.length"
        :files="shownStarred"
        :selected="NO_SELECTION"
        :selectable="false"
        :locale="locale"
        :thumb-src="thumbSrc"
        @click-card="openNode"
        @dbl-card="openNode"
        @context-card="(n: FileNode, ev: MouseEvent) => emit('context-node', n, ev)"
      />
      <p v-else-if="loading" class="fe-home__loading">{{ t('loading') }}</p>
      <p
        v-else-if="filteredEmpty(starred.length, shownStarred.length)"
        class="fe-home__nomatch"
        data-testid="home-starred-nomatch"
      >
        {{ t('filter.empty.title') }}
      </p>
      <div v-else class="fe-state fe-home__state" data-testid="home-starred-empty">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="96"
          height="80"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path
            d="M60 26l9 18.6 20.4 3-14.8 14.4 3.5 20.4L60 72.8 41.9 82.4l3.5-20.4L30.6 47.6l20.4-3z"
          />
        </svg>
        <p class="fe-state__title">{{ t('empty.starred.title') }}</p>
        <p class="fe-state__hint">{{ t('empty.starred.hint') }}</p>
      </div>
    </section>
  </div>
</template>
