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
import { computed, ref } from 'vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import ContextMenu, { type ContextAction } from './ContextMenu.vue';

/** The virtual listings the panel can open. '' = an ordinary folder.
 *  ⚠ `home` is not a listing: it is the overview (storages · recent · starred)
 *  the reference shell lands on, and the explorer renders it instead of a file
 *  list. It is in this union because the panel's selected-row logic, the
 *  address bar and the tab strip all key off ONE type — a second mechanism for
 *  "which destination am I on" is how two of them end up disagreeing. */
export type NavView = '' | 'home' | 'recent' | 'starred' | 'shared' | 'trash' | 'tag';

/**
 * A row in the panel's first group — what `open-view` carries.
 *
 * ⚠ It is NOT `NavView`. "My files" is a destination without being a view: it
 * opens the root LISTING, so the explorer answers it with an ordinary
 * navigation and `activeView` goes back to `''`. Giving it a NavView value
 * would mean a mode that every `if (navView)` in the explorer — "is this a
 * cross-folder view?", "is there a folder to upload into?", "what does Up do?"
 * — would answer wrongly, for a row whose whole job is to be an ordinary
 * folder again.
 */
export type NavDest = Exclude<NavView, '' | 'tag'> | 'myfiles';

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
  /* === etiket:t1 — the Tags section ==================================
   * "Tagged files should show up inside the tag." Tags are the one
   * navigation family whose entries are USER data: unbounded in number and
   * dynamic in name. Still presentational here — the host fetches
   * `tags/all` (once, cached) and hands the list over, exactly as it does
   * for storages. */
  /** Every tag that exists, alphabetical. Empty → the section shows its own
   *  "no tags yet" line rather than disappearing. */
  tags?: string[];
  /** False until the first answer arrives, so "no tags yet" is never shown
   *  to somebody who is simply still waiting. */
  tagsLoaded?: boolean;
  /** The tag currently on screen (activeView === 'tag'). */
  activeTag?: string;
  /**
   * Show the Connections entries — "How to connect" and "API keys".
   * ⚠ Never derived from a role here or anywhere: the backend decides what a
   * caller may see, and hiding the surface client-side only hides it from the
   * accounts that need it (see ExplorerConfig.connections).
   */
  showConnections?: boolean;
  /**
   * Draw the surfaces that only mean something for ONE person: API keys,
   * Recent, Starred, Shared with me. False when the caller is an app token —
   * a host proxy's shared credential, where "your keys" would be the proxy's
   * own and "your Recent" would be the token owner's history shown to a
   * stranger (see ExplorerConfig.callerKind).
   *
   * ⚠ This is a KIND check, not the role check the comment above forbids, and
   * it is not the whole panel: Upload, the storages, Trash and "How to
   * connect" stay — an embed's users still upload and still mount. An embedder
   * who wants no panel at all already has `sideNav: false`.
   */
  showIdentitySurfaces?: boolean;
  /** RBAC/root state — false hides the write affordances. */
  canWrite?: boolean;
  locale: LocaleCode;
  /* === surucu:d1 — the shell ========================================== */
  /**
   * ⚠ There is no `newMenu` prop any more. The two-button block (Upload as the
   * primary, New folder one step quieter) is gone and the "+ New" menu is what
   * this panel draws, everywhere — it is the shell, not a profile. Nothing was
   * removed: the menu holds what the explorer could already do — upload files,
   * make a folder (the modal offers the encrypted variant from inside itself),
   * and ask somebody else for files.
   *
   * There is deliberately no "Upload folder": the upload path takes a flat
   * `File[]` and would have to create the intermediate directories itself, and
   * an entry that quietly flattens someone's folder into one heap is worse
   * than an entry that is not there.
   */
  /** Offer "Request files" in that menu — a folder we may write to and share. */
  canRequestFiles?: boolean;
  /**
   * belge:n1 — offer "New document" in that menu. False on a server that
   * publishes no `newdoc_types`, and on one where every type it publishes
   * needs an editor service this deployment has not got.
   */
  canNewDocument?: boolean;
  /**
   * The signed-in person's storage line, from `GET /api/files/quota/me`. Null
   * (the default) renders nothing at all.
   *
   * ⚠ It is a PER-USER figure, not this storage's — `quota.Snapshot` sums
   * `nodes.size WHERE owner_id = me`, and there is no per-provider quota
   * (internal/quota/service.go). The mockup labels it with the drive's name;
   * that would be a lie about which number this is, so the label says
   * "Storage" and the drive name stays out of it.
   */
  quota?: { used: number; total: number; unlimited: boolean } | null;
  /** Resolved theme — the teleported New menu leaves the `.fe` variable scope. */
  theme?: ThemeMode;
}>();

const emit = defineEmits<{
  (e: 'toggle'): void;
  (e: 'open-view', view: NavDest): void;
  (e: 'open-tag', tag: string): void;
  (e: 'open-storage', name: string): void;
  (e: 'open-root'): void;
  (e: 'upload'): void;
  (e: 'new-folder'): void;
  (e: 'new-document'): void;
  (e: 'open-connections'): void;
  (e: 'open-tokens'): void;
  /* surucu:d1 — "Request files": the access modal on THIS folder, drop tab. */
  (e: 'request-files'): void;
  /** Drawer scrim / Esc — narrow mode only. */
  (e: 'close'): void;
}>();

const { t, formatSize } = useLocale(() => props.locale);

// In drawer mode "expanded" is the only meaningful state: a rail inside an
// overlay would be an overlay that shows nothing but icons while covering the
// listing anyway.
const showLabels = computed(() => props.expanded || !!props.narrow);

const sharedSet = computed(() => new Set(props.sharedStorages ?? []));

/** The views that answer "what did *I* do" — dropped for an app token. */
const IDENTITY_VIEWS = new Set<string>(['recent', 'starred', 'shared']);

/**
 * gorunum:v3-shell — the destinations, and their ORDER.
 *
 * Home · My files · Shared with me · Recent · Starred · Trash. The order is
 * not a taste: it runs from the widest answer to the narrowest — everything
 * you have, then your own tree, then somebody else's, then two slices of your
 * own, then what you threw away. The panel used to open with Recent, which
 * put a slice of the tree above the tree.
 *
 * ⚠⚠ "My files" is drawn ONLY where it can honestly mean "my files" — a
 * deployment whose caller can see at most ONE storage. There it opens that
 * storage's root and the label is the truth (measured: `soleStorageName` in
 * FileExplorer sends `load('')` straight to the storage, so the row lands on
 * the file list, exactly as the reference build's own "My files" does).
 *
 * With SEVERAL storages it cannot mean that, and what it actually did was open
 * the multi-storage root — a listing OF THE DRIVES. Owner, 2026-09-13: "my
 * files kısmında yüklediğimiz dosyalar gelmesi lazım ama onun yerine storages
 * gösteren ana bölge geliyor… direk silebiliriz." Three reasons it goes rather
 * than gets repointed:
 *
 *   1. it was the THIRD copy of that list. Home's "Storages" section draws the
 *      same drives as cards with a size caption, and this panel's own STORAGES
 *      group draws them one click away — and all three are on screen together.
 *      "Aynı işlevi yapan iki buton olmaması lazım."
 *   2. it was the worst of the three. The drives are synthesized rows, so the
 *      listing shows them under Type / Modified / Size columns that are all
 *      "—", owner "System", and with no filter row at all.
 *   3. nothing is stranded. Measured in the browser: the breadcrumb's house
 *      crumb (`crumbs[0]`, `adapterPath: ''`) and Alt+↑ from a storage root
 *      both land on exactly that listing, and both leave the panel in the same
 *      state this row used to.
 *
 * ⚠ `<= 1`, not `=== 1`: a caller who can see NO storage keeps the row, so an
 * account with nothing mounted still has a door back to the root listing and
 * its "no storages" message, rather than a panel of views with no files in it.
 * ⚠ It is still not an identity view — where it IS drawn it stays drawn for an
 * app token, which has no Recent and no Starred to escape through.
 */
const views = computed(() => {
  const list: Array<{ key: NavDest; label: string }> = [
    { key: 'home', label: t('sidenav.home') },
    ...(props.storages.length <= 1
      ? [{ key: 'myfiles' as NavDest, label: t('sidenav.myfiles') }]
      : []),
    { key: 'shared', label: t('sidenav.shared') },
    { key: 'recent', label: t('sidenav.recent') },
    { key: 'starred', label: t('sidenav.starred') },
  ];
  if (props.trashVisible !== false) list.push({ key: 'trash', label: t('sidenav.trash') });
  // Filtered at the end rather than built conditionally: Trash is shared by
  // everyone and the list keeps growing, so one rule at the bottom beats a
  // condition wrapped around each entry.
  if (props.showIdentitySurfaces === false) return list.filter((v) => !IDENTITY_VIEWS.has(v.key));
  return list;
});

const writable = computed(() => props.canWrite !== false);

/**
 * Which row reads as the one you are standing on.
 *
 * Every view answers for itself. "My files" is the exception, because it has
 * no view value to compare against: it is lit when the explorer is showing the
 * ROOT of the tree and nothing else — no virtual view, and no storage opened,
 * which is the state where the storage rows below all read as inactive too.
 * Inside a storage the storage's own row is the active one, so lighting both
 * would claim the selection is in two places.
 */
function isActiveDest(key: NavDest): boolean {
  if (key === 'myfiles') return !props.activeView && !props.activeStorage;
  return props.activeView === key;
}

/* === etiket:t1 — an unbounded list in a fixed panel ====================
 * A user with sixty tags must not push Storages and Connections off the
 * bottom of the panel, and must not be handed sixty identical glyphs on a
 * 56px rail either.
 *
 *   expanded / drawer → the first TAG_PEEK, then "Show all (N)". Both states
 *                       scroll (.fe-sidenav__scroll), so this is about the
 *                       sections BELOW staying reachable, not about overflow.
 *   rail             → ONE "Tags" button that opens the panel. Sixty rail
 *                       icons would be sixty copies of the same glyph with no
 *                       label — the rail's contract is "every destination one
 *                       click away", and this keeps it with one click more.
 */
const TAG_PEEK = 8;
const tagsExpanded = ref(false);
const allTags = computed(() => props.tags ?? []);
const visibleTags = computed(() =>
  tagsExpanded.value ? allTags.value : allTags.value.slice(0, TAG_PEEK),
);
const hiddenTagCount = computed(() => Math.max(0, allTags.value.length - visibleTags.value.length));
/* The section is rendered as soon as the panel knows there ARE tags, and also
 * once the answer came back empty — an empty section that says why is how a
 * user learns the feature exists at all. It stays hidden only while the first
 * answer is still in flight. */
const showTags = computed(() => !!props.tagsLoaded || allTags.value.length > 0);

/* === surucu:d1 — the "+ New" menu ====================================
 * One primary action instead of two buttons, the shape the reporter drew and
 * the shape Drive/FileRun/Nextcloud share. It is the SAME ContextMenu the
 * right-click menu uses — teleported to <body>, so the panel's own scroll
 * container cannot clip it, and one keyboard/focus behaviour for both. */
const newBtnEl = ref<HTMLElement | null>(null);
const newMenuRef = ref<InstanceType<typeof ContextMenu> | null>(null);

const newActions = computed<ContextAction[]>(() => {
  const list: ContextAction[] = [
    /* ⚠ NO `icon:` on any of these three, and that is the fix rather than an
       omission. `ContextMenu.iconFor` resolves `actionIconSvg(a.icon || a.key)`,
       so an `icon` string is an OVERRIDE — and `'⬆'` / `'📁'` / `'🔗'` are not
       keys, so the lookup missed, the component fell through to its literal
       branch and printed the emoji. Meanwhile `new-document`, which carries no
       override, resolved by key and drew a proper glyph: one menu, two icon
       systems, three rows apart. All three keys have had stroked glyphs in
       `lib/actionIcons.ts` the whole time (`upload`, `new-folder`,
       `request-files`); dropping the override is what reaches them.
       ⚠ Emoji in markup is forbidden here for a reason that is visible in this
       very menu: they are rendered by whatever font the OS ships, so the row
       came out flat-grey on one machine and full-colour on another, at a weight
       that matched nothing beside it. */
    { key: 'upload', label: t('drive.new.upload'), disabled: !writable.value },
    { key: 'new-folder', label: t('drive.new.folder'), disabled: !writable.value },
  ];
  // belge:n1 — DROPPED rather than disabled when unavailable, unlike
  // "Request files": a disabled row invites the question "why?" and cannot
  // answer it. The dialog behind it explains what is missing; a greyed row
  // in a menu has nowhere to say so.
  if (props.canNewDocument) {
    list.push({ key: 'new-document', label: t('drive.new.document'), disabled: !writable.value });
  }
  // Only when there is a real folder to hang a drop link on. Rendered as a
  // disabled row rather than dropped, so the menu does not change height
  // between folders — a menu whose items move is a menu people misclick.
  list.push({ divider: true, key: 'new-sep', label: '' });
  list.push({
    key: 'request-files',
    label: t('drive.new.request'),
    disabled: !props.canRequestFiles,
  });
  return list;
});

function openNewMenu() {
  const r = newBtnEl.value?.getBoundingClientRect();
  newMenuRef.value?.show(
    { clientX: r ? r.left : 0, clientY: r ? r.bottom + 6 : 0 } as MouseEvent,
    [],
  );
}

function onNewSelect(a: ContextAction) {
  if (a.key === 'upload') emit('upload');
  else if (a.key === 'new-document') emit('new-document');
  else if (a.key === 'new-folder') emit('new-folder');
  else if (a.key === 'request-files') emit('request-files');
}

/* === surucu:d1 — the storage line ===================================
 * The size comes from `useLocale.formatSize`, the same call `HomeView` makes
 * for the same `drive.storage.*` string. There was a private byte formatter
 * here with hardcoded English units and its own rounding, so one quota
 * rendered two ways — "9.3 GB" in this rail, "9.31 GB" on the home screen,
 * and "10 GB" in the admin panel that set it. */

const quotaPercent = computed(() => {
  const q = props.quota;
  if (!q || q.unlimited || q.total <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((q.used / q.total) * 100)));
});

const quotaText = computed(() => {
  const q = props.quota;
  if (!q) return '';
  return q.unlimited || q.total <= 0
    ? t('drive.storage.used_unlimited', { used: formatSize(q.used) })
    : t('drive.storage.used', { used: formatSize(q.used), total: formatSize(q.total) });
});

/** Only the drawer draws this control now (see the template). */
const toggleLabel = computed(() => t('sidenav.close'));
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
    <!-- gorunum:v3-shell — the panel's own edge control is now the DRAWER's
         close button and nothing else.
         ⚠ At 560px and up it is gone, deliberately: the collapse button lives
         at the far left of the top bar (`.fe-toolbar__brand`), above the panel
         rather than inside it, which is where the reference puts it and where
         it still exists when the panel is a 56px rail. Two buttons for one
         verb, one of them inside the thing it collapses, was the duplicate
         this removed. At 390px the panel is an overlay ON TOP of the files, so
         it keeps a dismiss of its own — the same reason a dialog has one. -->
    <div v-if="narrow" class="fe-sidenav__head">
      <button
        type="button"
        class="fe-sidenav__toggle"
        :aria-expanded="showLabels"
        :title="toggleLabel"
        :aria-label="toggleLabel"
        data-testid="sidenav-toggle"
        @click="emit('close')"
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
          <path d="M6 6l12 12M18 6L6 18" />
        </svg>
      </button>
    </div>

    <!-- Primary action. Making something is what people come to this panel to
         do, so it reads as the main button rather than one toolbar icon among
         fourteen. -->
    <!-- Rendered even with nowhere to write, and disabled instead. A block
         that appears and disappears makes every row below it jump by 90px each
         time the user opens a view, which reads as the panel reloading. -->
    <div class="fe-sidenav__primary">
      <!-- surucu:d1 — ONE primary action, the shape #14's mockups draw, in
           every profile and every embed. The two-button block that used to
           stand here behind `v-if="!newMenu"` (Upload + New folder) is gone:
           both verbs are the first two rows of this menu. -->
      <button
        ref="newBtnEl"
        type="button"
        class="fe-sidenav__new"
        aria-haspopup="menu"
        :title="t('drive.new')"
        :aria-label="t('drive.new')"
        data-testid="sidenav-new"
        @click="openNewMenu"
      >
        <!-- ⚠⚠ TWO SPANS AND A DIVIDER, NOT A SPLIT BUTTON. The reference draws
             a label half, a hairline and a chevron half; the owner asked twice
             for the chevron ("new tuşunun yanına caret koymamışsın, onu koymanı
             istiyorum") because a button that opens a menu has to look like
             one. But it stays ONE `<button aria-haspopup="menu">`: a real split
             button needs two focusable regions with two different actions, and
             here both halves do the same thing. Copying the shape without
             copying the mechanism is deliberate — two tab stops that lead to
             one menu is a keyboard user counting controls that do not exist.
             ⚠ The divider is `aria-hidden` decoration; it is 16px inset inside
             a 34px pill, not full height, or it reads as a seam between two
             buttons — which is exactly the thing this is not. -->
        <span class="fe-sidenav__new-main">
          <svg
            class="fe-ficon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            aria-hidden="true"
            focusable="false"
          >
            <path d="M12 5v14M5 12h14" />
          </svg>
          <span v-if="showLabels" class="fe-sidenav__text">{{ t('drive.new') }}</span>
        </span>
        <span v-if="showLabels" class="fe-sidenav__new-rule" aria-hidden="true"></span>
        <span v-if="showLabels" class="fe-sidenav__new-caret" aria-hidden="true">
          <svg
            class="fe-ficon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
            focusable="false"
          >
            <path d="M7 10l5 5 5-5" />
          </svg>
        </span>
      </button>
    </div>

    <div class="fe-sidenav__scroll">
      <ul class="fe-sidenav__group" :aria-label="t('sidenav.views')">
        <li v-for="v in views" :key="v.key">
          <button
            type="button"
            class="fe-sidenav__item"
            :class="{ 'is-active': isActiveDest(v.key) }"
            :aria-current="isActiveDest(v.key) ? 'page' : undefined"
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
              <template v-if="v.key === 'home'">
                <path d="M4 10.5L12 4l8 6.5" />
                <path d="M6 9.8V19a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1V9.8" />
                <path d="M10 20v-5.5h4V20" />
              </template>
              <template v-else-if="v.key === 'myfiles'">
                <path d="M3.5 7.5A1.5 1.5 0 0 1 5 6h4l2 2.5h8A1.5 1.5 0 0 1 20.5 10v7.5A1.5 1.5 0 0 1 19 19H5a1.5 1.5 0 0 1-1.5-1.5z" />
              </template>
              <template v-else-if="v.key === 'recent'">
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

      <!-- etiket:t1 — Tags. Between the views and the storages because a tag
           IS a view (a listing with no folder behind it), not a place files
           live. On the rail it collapses to one button that opens the panel:
           sixty tags would otherwise be sixty copies of one glyph with no
           label, and the rail's promise is that every destination stays one
           click away. -->
      <div v-if="showTags" class="fe-sidenav__section">
        <template v-if="showLabels">
          <p class="fe-sidenav__heading">{{ t('sidenav.tags') }}</p>
          <ul class="fe-sidenav__group" :aria-label="t('sidenav.tags')">
            <li v-for="tag in visibleTags" :key="tag">
              <button
                type="button"
                class="fe-sidenav__item fe-sidenav__item--tag"
                :class="{ 'is-active': activeView === 'tag' && activeTag === tag }"
                :aria-current="activeView === 'tag' && activeTag === tag ? 'page' : undefined"
                :title="tag"
                :aria-label="tag"
                :data-testid="`sidenav-tag-${tag}`"
                @click="emit('open-tag', tag)"
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
                  <path d="M4 4.5h7l9 9-6.5 6.5-9-9z" />
                  <circle cx="8" cy="8.5" r="1.4" />
                </svg>
                <span class="fe-sidenav__text">{{ tag }}</span>
              </button>
            </li>
            <!-- Nothing tagged yet: the section stays, and says how tags get
                 made. A section that only exists once you already know the
                 feature teaches nobody. -->
            <li v-if="allTags.length === 0">
              <p class="fe-sidenav__hint">{{ t('sidenav.tags.empty') }}</p>
            </li>
            <li v-if="hiddenTagCount > 0">
              <button
                type="button"
                class="fe-sidenav__more"
                data-testid="sidenav-tags-more"
                @click="tagsExpanded = true"
              >
                {{ t('sidenav.tags.more', { count: hiddenTagCount }) }}
              </button>
            </li>
            <li v-else-if="tagsExpanded && allTags.length > 8">
              <button
                type="button"
                class="fe-sidenav__more"
                data-testid="sidenav-tags-less"
                @click="tagsExpanded = false"
              >
                {{ t('sidenav.tags.less') }}
              </button>
            </li>
          </ul>
        </template>
        <template v-else>
          <hr class="fe-sidenav__rule" aria-hidden="true" />
          <ul class="fe-sidenav__group" :aria-label="t('sidenav.tags')">
            <li>
              <button
                type="button"
                class="fe-sidenav__item"
                :class="{ 'is-active': activeView === 'tag' }"
                :title="t('sidenav.tags')"
                :aria-label="t('sidenav.tags')"
                data-testid="sidenav-tags-rail"
                @click="emit('toggle')"
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
                  <path d="M4 4.5h7l9 9-6.5 6.5-9-9z" />
                  <circle cx="8" cy="8.5" r="1.4" />
                </svg>
              </button>
            </li>
          </ul>
        </template>
      </div>

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
          <!-- API keys is the person half of this section: "How to connect"
               stays for an app token (mount instructions are not identity),
               the keys go. -->
          <li v-if="showIdentitySurfaces !== false">
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

    <!-- surucu:d1 — the storage line. Under the navigation, above nothing:
         it is the last thing in the panel because it is a status, not a
         destination. Hidden entirely when the server did not answer (an app
         token has no person to have a quota) rather than drawn empty. -->
    <div v-if="quota" class="fe-sidenav__quota" data-testid="sidenav-quota">
      <p v-if="showLabels" class="fe-sidenav__quota-label">{{ t('drive.storage.label') }}</p>
      <div
        class="fe-sidenav__quota-bar"
        role="progressbar"
        :aria-valuenow="quotaPercent"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-label="quotaText"
        :title="quotaText"
      >
        <span class="fe-sidenav__quota-fill" :style="{ width: quotaPercent + '%' }"></span>
      </div>
      <p v-if="showLabels" class="fe-sidenav__quota-text">{{ quotaText }}</p>
    </div>

    <ContextMenu
      ref="newMenuRef"
      :locale="locale"
      :theme="theme"
      :actions="newActions"
      @select="onNewSelect"
    />
  </nav>
</template>
