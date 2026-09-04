<script setup lang="ts">
/**
 * SideNav — the explorer's navigation panel.
 *
 * A left column inside `.fe__main`, sibling to `.fe__primary` and the mirror
 * image of `InspectorPanel` on the other side. Three sections, top to bottom:
 * the primary action (Upload, with New folder as a secondary), the views
 * (Recent / Starred / Shared with me / Trash), and the storages the caller can
 * see.
 *
 * Why it exists (GitHub #14): the explorer's shape — split pane, tabs, three
 * view modes, "How to connect" — is a power-user tool, and the reporter's
 * users read that as a file manager they would have to relearn. The answer is
 * not a second UI: it is one explorer with the navigation people already know
 * from Drive, that anyone can collapse when they want the width back.
 *
 * Collapse goes to a RAIL, not to nothing. A panel that vanishes takes its own
 * re-open affordance with it; the rail keeps every destination one click away
 * and every icon tab-reachable, with its label in `title` + `aria-label`.
 *
 * ⚠ Presentational only. It fetches nothing and owns no listing state — the
 * host (FileExplorer) loads the views and tells this component which one is
 * active, exactly as it does for Toolbar. Two components fetching the same
 * list is how the panel and the pane end up disagreeing.
 *
 * ⚠ No `<style>` block, scoped or otherwise: the package's CSS lives in
 * `styles/base.css`. A scoped block compiles to `.cls[data-v-HASH]` and the
 * hash does not match in the web-component build, so the rules silently stop
 * applying in every embed (measured on the share dialog: raw unstyled HTML).
 */
import { computed } from 'vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';

/** The virtual listings the panel can open. '' = an ordinary folder. */
export type NavView = '' | 'recent' | 'starred' | 'shared' | 'trash';

export interface NavStorage {
  name: string;
  label?: string;
  driver?: string;
  readOnly?: boolean;
}

const props = defineProps<{
  /** Expanded (labels visible) vs collapsed to the icon rail. */
  expanded: boolean;
  /** Narrow/embed mode — the panel is a drawer over the listing, not a column. */
  narrow?: boolean;
  /** Which virtual view is on screen, so the row can read as selected. */
  activeView: NavView;
  /** Storage currently open ('' at the multi-storage root). */
  activeStorage?: string;
  /** Storages the caller can see (ExplorerConfig.storages). */
  storages: NavStorage[];
  /**
   * Names of storages the caller reaches through a GRANT rather than their own
   * role — Drive's "shared drives". Marked, not sorted out: the reporter's ask
   * was that they "just appear there", one click, no mount instructions.
   */
  sharedStorages?: string[];
  /** Show the Trash entry (mirrors ExplorerConfig.trashVisible). */
  trashVisible?: boolean;
  /**
   * Show the Connections entries — "How to connect" and "API keys".
   * ⚠ Never derived from a role here or anywhere: the backend decides what a
   * caller may see, and hiding the surface client-side only hides it from the
   * accounts that need it (see ExplorerConfig.connections).
   */
  showConnections?: boolean;
  /** RBAC/root state — false hides the write affordances. */
  canWrite?: boolean;
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'toggle'): void;
  (e: 'open-view', view: Exclude<NavView, ''>): void;
  (e: 'open-storage', name: string): void;
  (e: 'open-root'): void;
  (e: 'upload'): void;
  (e: 'new-folder'): void;
  (e: 'open-connections'): void;
  (e: 'open-tokens'): void;
  /** Drawer scrim / Esc — narrow mode only. */
  (e: 'close'): void;
}>();

const { t } = useLocale(() => props.locale);

// In drawer mode "expanded" is the only meaningful state: a rail inside an
// overlay would be an overlay that shows nothing but icons while covering the
// listing anyway.
const showLabels = computed(() => props.expanded || !!props.narrow);

const sharedSet = computed(() => new Set(props.sharedStorages ?? []));

const views = computed(() => {
  const list: Array<{ key: Exclude<NavView, ''>; label: string }> = [
    { key: 'recent', label: t('sidenav.recent') },
    { key: 'starred', label: t('sidenav.starred') },
    { key: 'shared', label: t('sidenav.shared') },
  ];
  if (props.trashVisible !== false) list.push({ key: 'trash', label: t('sidenav.trash') });
  return list;
});

const writable = computed(() => props.canWrite !== false);

const toggleLabel = computed(() =>
  props.narrow
    ? t('sidenav.close')
    : props.expanded
      ? t('sidenav.collapse')
      : t('sidenav.expand'),
);
</script>

<template>
  <nav
    class="fe-sidenav"
    :class="{
      'fe-sidenav--rail': !expanded && !narrow,
      'fe-sidenav--drawer': narrow,
    }"
    role="navigation"
    :aria-label="t('sidenav.title')"
    data-testid="sidenav"
  >
    <div class="fe-sidenav__head">
      <button
        type="button"
        class="fe-sidenav__toggle"
        :aria-expanded="showLabels"
        :title="toggleLabel"
        :aria-label="toggleLabel"
        data-testid="sidenav-toggle"
        @click="narrow ? emit('close') : emit('toggle')"
      >
        <!-- The glyph says what the control does NEXT, and each state gets
             its own: an expanded panel closes (arrow into the sidebar), a rail
             opens (arrow out of it), a drawer dismisses (cross). One hamburger
             for all three reads as decoration — and this toolbar already
             carries two other three-line glyphs. -->
        <svg
          class="fe-ficon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <template v-if="narrow">
            <path d="M6 6l12 12M18 6L6 18" />
          </template>
          <template v-else-if="expanded">
            <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
            <path d="M9.5 4.5v15" />
            <path d="M17 9.5L14.5 12l2.5 2.5" />
          </template>
          <template v-else>
            <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
            <path d="M9.5 4.5v15" />
            <path d="M14.5 9.5L17 12l-2.5 2.5" />
          </template>
        </svg>
      </button>
    </div>

    <!-- Primary action. Upload is the thing people come here to do, so in the
         panel it reads as the main button rather than one toolbar icon among
         fourteen. New folder stays, one step quieter. -->
    <!-- Rendered even with nowhere to write, and disabled instead. A block
         that appears and disappears makes every row below it jump by 90px each
         time the user opens a view, which reads as the panel reloading. -->
    <div class="fe-sidenav__primary">
      <button
        type="button"
        class="fe-sidenav__upload"
        :disabled="!writable"
        :title="t('toolbar.upload')"
        :aria-label="t('toolbar.upload')"
        data-testid="sidenav-upload"
        @click="emit('upload')"
      >
        <svg
          class="fe-ficon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.9"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <path d="M12 16V4" />
          <path d="M7 9l5-5 5 5" />
          <path d="M4 17v2a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-2" />
        </svg>
        <span v-if="showLabels" class="fe-sidenav__text">{{ t('toolbar.upload') }}</span>
      </button>
      <button
        type="button"
        class="fe-sidenav__secondary"
        :disabled="!writable"
        :title="t('toolbar.new_folder')"
        :aria-label="t('toolbar.new_folder')"
        data-testid="sidenav-new-folder"
        @click="emit('new-folder')"
      >
        <svg
          class="fe-ficon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <path d="M3 7.5A1.5 1.5 0 0 1 4.5 6h4l2 2.5h7A1.5 1.5 0 0 1 19 10v7.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 3 17.5z" />
          <path d="M12 11.5v5M9.5 14h5" />
        </svg>
        <span v-if="showLabels" class="fe-sidenav__text">{{ t('toolbar.new_folder') }}</span>
      </button>
    </div>

    <div class="fe-sidenav__scroll">
      <ul class="fe-sidenav__group" :aria-label="t('sidenav.views')">
        <li v-for="v in views" :key="v.key">
          <button
            type="button"
            class="fe-sidenav__item"
            :class="{ 'is-active': activeView === v.key }"
            :aria-current="activeView === v.key ? 'page' : undefined"
            :title="v.label"
            :aria-label="v.label"
            :data-testid="`sidenav-view-${v.key}`"
            @click="emit('open-view', v.key)"
          >
            <svg
              class="fe-ficon"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
              focusable="false"
            >
              <template v-if="v.key === 'recent'">
                <circle cx="12" cy="12" r="8.5" />
                <path d="M12 7.5V12l3 2" />
              </template>
              <template v-else-if="v.key === 'starred'">
                <path d="M12 4l2.4 4.9 5.4.8-3.9 3.8.9 5.4-4.8-2.5-4.8 2.5.9-5.4-3.9-3.8 5.4-.8z" />
              </template>
              <template v-else-if="v.key === 'shared'">
                <circle cx="17.5" cy="6.5" r="2.5" />
                <circle cx="6.5" cy="12" r="2.5" />
                <circle cx="17.5" cy="17.5" r="2.5" />
                <path d="M8.8 10.8l6.4-3.2M8.8 13.2l6.4 3.2" />
              </template>
              <template v-else>
                <path d="M4.5 7h15" />
                <path d="M9.5 7V5.2A1.2 1.2 0 0 1 10.7 4h2.6a1.2 1.2 0 0 1 1.2 1.2V7" />
                <path d="M6.5 7l.9 11.1A1.4 1.4 0 0 0 8.8 19.4h6.4a1.4 1.4 0 0 0 1.4-1.3L17.5 7" />
              </template>
            </svg>
            <span v-if="showLabels" class="fe-sidenav__text">{{ v.label }}</span>
          </button>
        </li>
      </ul>

      <div v-if="storages.length" class="fe-sidenav__section">
        <p v-if="showLabels" class="fe-sidenav__heading">{{ t('sidenav.storages') }}</p>
        <hr v-else class="fe-sidenav__rule" aria-hidden="true" />
        <ul class="fe-sidenav__group" :aria-label="t('sidenav.storages')">
          <li v-for="s in storages" :key="s.name">
            <button
              type="button"
              class="fe-sidenav__item"
              :class="{ 'is-active': !activeView && activeStorage === s.name }"
              :title="
                sharedSet.has(s.name)
                  ? `${s.label || s.name} — ${t('sidenav.storage.shared')}`
                  : s.label || s.name
              "
              :aria-label="
                sharedSet.has(s.name)
                  ? `${s.label || s.name} — ${t('sidenav.storage.shared')}`
                  : s.label || s.name
              "
              :data-testid="`sidenav-storage-${s.name}`"
              @click="emit('open-storage', s.name)"
            >
              <!-- A granted storage gets a different glyph, not a badge glued
                   into the label: a badge inside the name span changes the
                   row's textContent and breaks every selector that finds a row
                   by its name (measured on the compliance badges, PR #12). -->
              <svg
                class="fe-ficon"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
                focusable="false"
              >
                <template v-if="sharedSet.has(s.name)">
                  <path d="M3 8.5A1.5 1.5 0 0 1 4.5 7h5L11 9h8.5A1.5 1.5 0 0 1 21 10.5v7A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5z" />
                  <circle cx="15.6" cy="13.6" r="1.6" />
                  <circle cx="9.4" cy="15.4" r="1.6" />
                  <path d="M10.9 14.7l3.3-.9" />
                </template>
                <template v-else>
                  <rect x="3" y="5" width="18" height="6" rx="1.6" />
                  <rect x="3" y="13" width="18" height="6" rx="1.6" />
                  <path d="M6.5 8h.01M6.5 16h.01" />
                </template>
              </svg>
              <span v-if="showLabels" class="fe-sidenav__text">{{ s.label || s.name }}</span>
              <span
                v-if="showLabels && sharedSet.has(s.name)"
                class="fe-sidenav__tag"
                role="img"
                :aria-label="t('sidenav.storage.shared')"
                >{{ t('sidenav.storage.shared') }}</span
              >
            </button>
          </li>
        </ul>
      </div>

      <!-- Connections. Last, because it is where you go once rather than every
           day — and in core rather than in the host app, so an embedded
           explorer's users can reach the guides and mint their own keys
           instead of being told to ask an administrator. -->
      <div v-if="showConnections" class="fe-sidenav__section">
        <p v-if="showLabels" class="fe-sidenav__heading">{{ t('sidenav.connections') }}</p>
        <hr v-else class="fe-sidenav__rule" aria-hidden="true" />
        <ul class="fe-sidenav__group" :aria-label="t('sidenav.connections')">
          <li>
            <button
              type="button"
              class="fe-sidenav__item"
              :title="t('sidenav.connect')"
              :aria-label="t('sidenav.connect')"
              data-testid="sidenav-connect"
              @click="emit('open-connections')"
            >
              <svg
                class="fe-ficon"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
                focusable="false"
              >
                <path d="M9.5 14.5l-2.6 2.6a3.7 3.7 0 0 1-5.2-5.2l2.6-2.6" />
                <path d="M14.5 9.5l2.6-2.6a3.7 3.7 0 0 1 5.2 5.2l-2.6 2.6" />
                <path d="M9 15l6-6" />
              </svg>
              <span v-if="showLabels" class="fe-sidenav__text">{{ t('sidenav.connect') }}</span>
            </button>
          </li>
          <li>
            <button
              type="button"
              class="fe-sidenav__item"
              :title="t('sidenav.apikeys')"
              :aria-label="t('sidenav.apikeys')"
              data-testid="sidenav-apikeys"
              @click="emit('open-tokens')"
            >
              <svg
                class="fe-ficon"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
                focusable="false"
              >
                <circle cx="8" cy="12" r="3.5" />
                <path d="M11.5 12H21" />
                <path d="M17.5 12v3.2M20 12v2.2" />
              </svg>
              <span v-if="showLabels" class="fe-sidenav__text">{{ t('sidenav.apikeys') }}</span>
            </button>
          </li>
        </ul>
      </div>
    </div>
  </nav>
</template>
