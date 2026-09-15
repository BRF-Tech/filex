<script setup lang="ts">
// Explore page — fullscreen file browser. Renders the real
// @brftech/filex-core <FileExplorer/> SFC with `multiStorageRoot`
// turned on: the user lands at "/" which lists every configured
// storage as a virtual folder. Clicking one drills into it; the
// breadcrumb walks `/ › s3-test › example › …`.
//
// The old per-storage tab strip is gone — the storage list is now
// the home screen of the explorer itself.

import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { FileExplorer, type ExplorerConfig } from '@brftech/filex-core';
// gorunum:v3-shell — `actionIconSvg` is no longer imported here. The account
// cluster was three icon buttons drawn with the explorer's own glyph set (so
// three marks at a foreign stroke weight would not sit in the same row); it is
// one avatar with a menu now, and an avatar is a letter or a photograph.
// gorunum:v3-shell — the stylesheet is imported once in `main.ts` now. It was
// imported here (and in the Home page that no longer exists), which left every
// route mounting neither of them with no `--fe-*` tokens at all.

import { useAuthStore } from '@/stores/auth';
import { useStoragesStore } from '@/stores/storages';
import Button from '@/components/ui/Button.vue';
// gorunum:v3-shell — the product mark, handed to the explorer's own header.
import LogoMark from '@/components/LogoMark.vue';
import AccountMenu, { type AccountAction } from '@/components/AccountMenu.vue';
// The admin panel's own bell, not a second one — see the header cluster below.
import NotificationBell from '@/components/NotificationBell.vue';

// The person's own settings — the same modal the admin chrome opens. This
// page is where a non-admin account is sent, so without it their notification
// switches stay behind the admin-only panel.
const UserSettingsModal = defineAsyncComponent(
  () => import('@/components/UserSettingsModal.vue'),
);
import { effectiveTheme } from '@/lib/theme';
import { explorerAuth } from '@/lib/explorerConfig';
import { fetchVisibleStorages, type VisibleStorage } from '@/lib/visibleStorages';
// Live collaboration (WebSocket + presence) now lives INSIDE @brftech/filex-core's
// FileExplorer, so every consumer (this panel + the embedded webcomponent) gets
// it automatically — no per-page realtime wiring here anymore.

const { t, locale } = useI18n();
const router = useRouter();
const route = useRoute();
const auth = useAuthStore();
const storages = useStoragesStore();

// gezinti:g1 — the API-key surface moved into the shared package and is opened
// from the explorer's navigation panel. This page no longer owns a copy of it;
// `web/src/components/SelfTokensModal.vue` and `web/src/api/self-tokens.ts` are
// gone, because two implementations of one credential screen is how one of them
// mints tokens the other cannot see.
// gorunum:v2-topbar — "How to connect" is NOT opened from here any more. The
// explorer's navigation panel has carried the same door since gezinti:g1
// (`sidenav-connect`, and it renders the very same ConnectionsPanel), so this
// page held a second implementation of one screen — overlay, z-index, config
// object and all. The panel's entry is the survivor; it is on screen in both
// profiles and for admins and non-admins alike.
const showSettings = ref(false);
async function doLogout() {
  await auth.logout();
  router.push({ name: 'login' });
}

/* === gorunum:v4-hostmenu — the explorer's "⋯" moves in here ================
 *
 * Owner's decision, 2026-09-13, verbatim: *"`...` bölgesini admin dropdown'ının
 * içine alacağız."* The header used to end in TWO dropdowns a glyph apart — the
 * explorer's "⋯" and this avatar — which is the duplicate this whole wave is
 * about (*"aynı işlevi yapan iki buton olmaması lazım"*).
 *
 * ⚠ The rows arrive from the explorer, they are not re-declared here. They are
 * ITS settings, in ITS locale catalogue, and two of the labels are stateful
 * ("Compact view" ⇄ "Comfortable view"). The contract is a DOM event the
 * toolbar announces and this page CLAIMS by setting `claimed` — synchronously,
 * so the toolbar knows before its next paint whether to keep drawing its own
 * button. An embed claims nothing and keeps its "⋯" exactly as it was.
 */
interface ExplorerMenuRow {
  key: string;
  label: string;
  divider?: boolean;
  disabled?: boolean;
  icon?: string;
}
interface HeaderMenuClaim {
  items: ExplorerMenuRow[];
  run: (key: string) => void;
  claimed: boolean;
}

const explorerRows = ref<ExplorerMenuRow[]>([]);
let runExplorerRow: ((key: string) => void) | null = null;

/**
 * ⚠ Rows this page ALREADY has a door for, dropped rather than mirrored.
 *
 * Folding the "⋯" in here is only worth doing if it does not smuggle the
 * duplicates back — an account menu whose second row opens the theme palette
 * while its FIRST row opens a settings modal containing that same palette is
 * the two-doors problem with extra steps. Each key and its surviving door,
 * measured in the browser at 1440 and 390:
 *   theme            → User settings → Appearance (core's own ThemePalette,
 *                      the very component the explorer's gallery modal wraps)
 *   density          → User settings → Preferences ("compact file list")
 *   view-list/grid/gallery → the ViewSwitcher in the breadcrumb row
 *   inspector        → the ⓘ in the breadcrumb row (this page passes
 *                      `showInfoPanel: true`, so it is always drawn)
 *   timezone         → User settings → Preferences (core's own
 *                      TimeZonePicker, the very component the explorer's
 *                      Time zone dialog wraps; picking there also clears
 *                      this browser's in-embed pick — lib/timezone)
 * ⚠ `refresh` is deliberately NOT here: the wide header draws a Refresh button
 * and the toolbar therefore stops publishing the row at that width, but the
 * NARROW header has no Refresh button at all — dropping it by name would make
 * Refresh unreachable on a phone.
 * ⚠ A DROP list, not a keep list: a settings row added to the explorer
 * tomorrow has to arrive here by itself, or this menu silently falls behind
 * the product it belongs to.
 */
const ROWS_WITH_ANOTHER_DOOR = new Set([
  'theme',
  'density',
  'view-list',
  'view-grid',
  'view-gallery',
  'inspector',
  'nav',
  'timezone',
]);

function onHeaderMenu(ev: Event) {
  const detail = (ev as CustomEvent).detail as HeaderMenuClaim | undefined;
  if (!detail || !Array.isArray(detail.items)) return;
  // Say so BEFORE returning: the toolbar reads this the instant dispatch ends.
  detail.claimed = true;
  explorerRows.value = detail.items.filter(
    (r) => !r.divider && !ROWS_WITH_ANOTHER_DOOR.has(r.key),
  );
  runExplorerRow = typeof detail.run === 'function' ? detail.run : null;
}
/* ⚠ Registered in setup(), NOT in onMounted: a child's onMounted runs before
   its parent's, so a listener added there would miss the explorer's first
   announcement and the "⋯" would stand until something else changed. */
if (typeof document !== 'undefined') {
  document.addEventListener('fe:header-menu', onHeaderMenu);
  onBeforeUnmount(() => document.removeEventListener('fe:header-menu', onHeaderMenu));
}

/**
 * gorunum:v2-topbar / v3-shell — the header's trailing cluster: ONE avatar.
 *
 * ⚠ It used to be three separate icon buttons (admin panel · user settings ·
 * sign out). Owner's decision, 2026-09-13, verbatim: *"Tek avatar, menü
 * açılsın."* Three glyphs standing for one subject is three chances to misread
 * the row, and the admin entry is still the single extra thing an
 * administrator gets — as a menu row rather than a fourth icon.
 *
 * Declared as data and rendered from one array because the control appears in
 * two places that are never on screen together: inside the explorer's header
 * once the explorer exists, and under the "no storages / no access" message
 * when it does not. Writing the rows twice is how the two copies drift.
 *
 * ⚠ Language is deliberately NOT here. It already exists inside the settings
 * modal (Preferences → Language); a second switcher in the chrome would be
 * exactly the duplicate this pass removes. Same for the light/dark toggle —
 * the modal's Light/Auto/Dark segment is the one control, and the admin
 * panel's header lost its copies of both in the same pass.
 *
 * ⚠ Every row carries a glyph. Owner's decision, 2026-09-13, verbatim:
 * *"Bizim profil altındaki itemlere ikon koymamız şart."* They are keys into
 * `@brftech/filex-core`'s `actionIcons`, the explorer's own set — the rows
 * arriving from the "⋯" name their glyph by their own key, and the three that
 * belong to this page name theirs here.
 */
const headerActions = computed<AccountAction[]>(() => {
  if (!auth.isAuthenticated) return [];
  const rows: AccountAction[] = [
    { key: 'settings', label: t('userSettings.open'), icon: 'account' },
  ];
  // ⚠ Admins only, and it is the ONLY role check in this cluster. Everything
  // else here belongs to whoever is signed in.
  if (auth.isAdmin) rows.push({ key: 'admin', label: t('explore.gotoAdmin'), icon: 'admin' });
  // The explorer's own settings, between this account's doors and the exit:
  // they are neither "who am I" nor "goodbye", and burying them under Sign out
  // would put the one destructive row in the middle of the list.
  explorerRows.value.forEach((r, i) =>
    rows.push({
      key: `fe:${r.key}`,
      label: r.label,
      icon: r.icon || r.key,
      separated: i === 0,
    }),
  );
  // ⚠ `nav.logout`, not `explore.logout`. They are the same verb and the
  // English differed — "Sign out" in the admin panel's account menu, "Log out"
  // in the explorer's — which is one product speaking with two voices about
  // one action. One string, both menus.
  rows.push({ key: 'signout', label: t('nav.logout'), separated: true, icon: 'sign-out' });
  return rows;
});

/** One place the rows are acted on, for both copies of the control. */
function runAccountAction(key: string) {
  /* ⚠ `fe:` prefixed, so an explorer row named `settings` one day cannot
     silently take over this page's own row. The verb itself is the explorer's
     — run through the callback it handed us, which is the SAME handler its own
     "⋯" uses, so a claimed row and an unclaimed one cannot do different
     things. */
  if (key.startsWith('fe:')) {
    runExplorerRow?.(key.slice(3));
    return;
  }
  if (key === 'admin') void router.push({ name: 'dashboard' });
  else if (key === 'settings') showSettings.value = true;
  else if (key === 'signout') void doLogout();
}

// Remount key for the FileExplorer. Nothing bumps it any more — see
// `rediscoverStorages` for why a Refresh must NOT remount.
const remountKey = ref(0);

/**
 * gorunum:v2-topbar — the other half of Refresh.
 *
 * The page bar had a Refresh of its own that re-ran storage discovery; the bar
 * is gone, so the explorer's Refresh (`@refresh`) has to mean both halves,
 * otherwise a drive granted or created somewhere else stays invisible until
 * the browser reloads the page.
 *
 * ⚠ It replaces `roots`, and that is ALL it does. `explorerConfig` is a
 * computed over `roots`, and the explorer reads `config.storages` reactively
 * for its navigation panel — so the new drive appears in place, with the
 * folder you were standing in, your selection and your scroll position
 * untouched. Bumping `remountKey` here would also work and would throw all
 * three away on every Refresh, which is what "refresh" must not mean.
 *
 * ⚠ No `loading` flag either: that swaps the whole page for a spinner, and
 * this runs while the person is looking at their files.
 */
async function rediscoverStorages() {
  try {
    // ⚠⚠ The admin store FIRST, and this line is load-bearing for exactly one
    // (very common) route into this page.
    //
    // `fetchVisibleStorages` short-circuits on a NON-EMPTY `adminItems` and
    // returns it verbatim; it only falls through to the manager root — which
    // is live — for callers holding no admin list. So which half of that you
    // get depends on whether the pinia store happens to be warm:
    //   • landed straight on /explore  → store empty, discovery hits the
    //     manager root, a new drive shows up even without this line;
    //   • came from the panel (admin → Storages → Explore, one SPA session,
    //     no reload) → store warm, and without this line Refresh re-wraps the
    //     names pinia was already holding.
    // Measured 2026-09-12 on that second route, both ways: store warm
    // (adminItems=1), storage created through the API with the page open,
    // Refresh pressed — WITHOUT this line the drive did not appear in the
    // panel; WITH it, it appeared and opened. The old page-level Refresh had
    // the same gap, and its remount only re-read the same stale names.
    // Best-effort: it 403s for a non-admin, whose list comes from the manager
    // root inside fetchVisibleStorages anyway.
    await storages.fetch().catch(() => {});
    roots.value = await fetchVisibleStorages(storages.items);
  } catch (err) {
    // A failed re-discovery leaves the previous list standing. The listing
    // reloaded regardless — the explorer does that half itself — so there is
    // nothing to tell the user about here.
    // eslint-disable-next-line no-console
    console.warn('[explore] storage re-discovery failed:', err);
  }
}

// Reactive theme passthrough — without this the SFC's CSS variable
// cascade falls back to `prefers-color-scheme: dark` on OS dark
// systems even when the admin shell is on light, leaving the
// explorer pane locked to dark after the user flips the panel.
// MutationObserver watches `<html>` class changes; localStorage
// `storage` events keep cross-tab toggles in sync.
const currentTheme = ref<'light' | 'dark'>(effectiveTheme());
let htmlObserver: MutationObserver | null = null;
const onStorage = (e: StorageEvent) => {
  if (e.key === 'filex.theme') currentTheme.value = effectiveTheme();
};
onMounted(() => {
  htmlObserver = new MutationObserver(() => {
    currentTheme.value = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
  });
  htmlObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  window.addEventListener('storage', onStorage);
});
onBeforeUnmount(() => {
  htmlObserver?.disconnect();
  window.removeEventListener('storage', onStorage);
});

// Visible storages for the explorer root. Admins get the rich admin-store
// list; non-admins (user/viewer) can't hit /api/admin/storages, so we discover
// their visible storages from the manager root (StorageVisible-filtered) —
// otherwise the explorer would show "no storages" for every non-admin.
//
// ⚠ The discovery itself lives in `lib/visibleStorages.ts`, not here: the Home
// page has to answer the same question, and two copies of "which drives may I
// show you" is how one of them starts showing a drive the other hides.
const roots = ref<VisibleStorage[]>([]);
// True until the first storage-discovery pass finishes, so we show a loading
// screen instead of flashing the "no storage" empty state during startup.
const loading = ref(true);

/*
 * `?select=` — "open this folder with THIS row selected".
 *
 * This is the landing half of a notification click: the hash says which folder
 * (the explorer reads that itself), and this says which row inside it the
 * person was told about. Both halves are produced by one resolver,
 * `lib/notificationTarget.ts`.
 *
 * ⚠ Done from the embedder, by dispatching a click at the row's CHECKBOX,
 * because the explorer component takes no "start with this selected"
 * configuration and this page may not edit it. The row carries
 * `data-fe-path="<storage>://<rel>"` — the attribute the component already puts
 * there for middle-click open-in-new-tab — and ticking its box goes through the
 * component's own selection code, so range/ctrl behaviour afterwards is exactly
 * as if a hand had done it.
 *
 * ⚠⚠ The checkbox, never the row. Since issue #26 a click anywhere else on a
 * row OPENS it — clicking the row here opened the file from the notification
 * instead of pointing at it, and the retry loop below would have kept doing it.
 * A tick adds to a selection rather than replacing it, so any other row that is
 * still ticked is unticked first: the notification points at ONE file.
 *
 * ⚠ Compared attribute-by-attribute rather than through a `[data-fe-path="…"]`
 * selector: a real file name may contain a quote or a backslash, and a
 * selector built by string concatenation breaks on exactly those names.
 */
const selectFromQuery = computed(() => {
  const raw = route.query.select;
  const v = Array.isArray(raw) ? raw[0] : raw;
  return typeof v === 'string' ? v : '';
});

/** Poll for the row the listing has not drawn yet. Bounded — a target that
 *  never appears (deleted meanwhile, filtered out) must not spin forever. */
const SELECT_TIMEOUT_MS = 8000;
let selectToken = 0;

/** Toggle a row's selection the one way a click selects: its checkbox. */
function tick(row: HTMLElement) {
  row.querySelector<HTMLElement>('.fe-list__check')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

async function revealSelection(qualified: string): Promise<boolean> {
  if (!qualified) return false;
  const mine = ++selectToken;
  const deadline = Date.now() + SELECT_TIMEOUT_MS;
  // ⚠⚠ The selection has to STICK, not merely happen. Measured in a real
  // browser: the row was found, clicked, and drawn selected — and came back
  // `aria-selected="false"` a moment later, because the listing clears the
  // selection when its load finishes, which is AFTER its first rows are on
  // screen. A reveal that returns on the first success is therefore a coin
  // toss: two runs of the same measurement disagreed. So it re-clicks
  // whenever it finds the row unselected and only stops once the selection
  // has survived three consecutive checks.
  let stable = 0;
  while (Date.now() < deadline) {
    if (mine !== selectToken) return false; // a newer click superseded this one
    const row = Array.from(document.querySelectorAll<HTMLElement>('[data-fe-path]')).find(
      (el) => el.getAttribute('data-fe-path') === qualified,
    );
    if (row && row.getAttribute('aria-selected') === 'true') {
      if (++stable >= 3) return true;
    } else {
      stable = 0;
      if (row) {
        for (const other of Array.from(document.querySelectorAll<HTMLElement>('[data-fe-path][aria-selected="true"]'))) {
          if (other !== row) tick(other);
        }
        row.scrollIntoView({ block: 'center', behavior: 'auto' });
        tick(row);
      }
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  return false;
}

// A click from the bell while ALREADY on this page changes the route without
// remounting anything, so the reveal has to be driven by the route as well as
// by mount.
watch(
  () => [route.hash, selectFromQuery.value] as const,
  () => {
    if (selectFromQuery.value) void revealSelection(selectFromQuery.value);
  },
);

// `?storage=` deep links: `/admin/explore?storage=s3-test` →
// initialPath becomes `s3-test://`. Without one the explorer opens
// at the global root (storage list).
const initialPathFromQuery = computed(() => {
  const raw = route.query.storage;
  const rawStr = Array.isArray(raw) ? raw[0] : raw;
  if (typeof rawStr !== 'string' || !rawStr) return '';
  const byName = roots.value.find((s) => s.name === rawStr);
  if (byName) return `${byName.name}://`;
  return '';
});

/*
 * gezinti:g1 / gorunum:v3-shell — ⚠⚠ THERE IS NO `uiProfile` HERE ANY MORE.
 *
 * This page used to pass `auth.isAdmin ? 'standard' : 'drive'`, and that one
 * expression was the bug: the shell — the filter row, "+ New", the
 * Details/Activity tabs, the storage line — arrived with the string `'drive'`,
 * so the screen the owner actually uses, and the one every screenshot is taken
 * from, was the only screen that never got it. An operator's tool and an end
 * user's drive are not two products. Owner's decision, 2026-09-12, verbatim
 * (translated from Turkish): "their app and our app will be one to one. The
 * admin gets one extra button, nothing else."
 *
 * So the profile is not passed at all: the default IS the shell (see
 * ExplorerConfig.uiProfile), it is the same in the desktop app and in every
 * embed, and the admin's one extra button is `headerActions` above. Reaching
 * for a profile string here again would put the difference back in the one
 * place a shared package cannot see it.
 */

/**
 * gorunum:v3-shell — the `/home` route opens the explorer ON the Home view.
 *
 * ⚠ Through `initialPath`, not through a second mechanism. `.home` is the same
 * sentinel the address-bar hash, a restored tab and the panel's own Home row
 * already speak (lib/listing VIRTUAL_SEGMENTS), so Home is a location like
 * every other location and the two routes differ by where they open — not by
 * what they render. `/explore` passes no sentinel and therefore restores
 * wherever the person was, exactly as before.
 */
const opensOnHome = computed(() => route.name === 'home');

const explorerConfig = computed<ExplorerConfig | null>(() => {
  if (!roots.value.length) return null;
  const authConf: ExplorerConfig['auth'] = explorerAuth();
  return {
    apiBase: '',
    endpoint: '/api/files/manager',
    capabilities: '/api/files/capabilities',
    auth: authConf,
    theme: currentTheme.value,
    locale: locale.value === 'en' ? 'en' : 'tr',
    // The address bar mirrors the current folder (#<storage>/<sub>…) so the
    // URL is a shareable deep link; localStorage still remembers the last
    // folder for hash-less visits. Priority: hash → ?storage= → remembered.
    pathPersist: 'hash+localStorage',
    trashVisible: true,
    showInfoPanel: true,
    multiStorageRoot: true,
    // ⚠ Explicit, and NOT the simple profile's default. In this deployment a
    // non-admin is a real account that mounts drives, and the only screen that
    // can mint the token WebDAV / FTPS / `filex mount` ask for is this one —
    // leaving it to the profile default would re-open the exact gap
    // e2e/tests/25-connections.spec.ts guards ("a non-admin has to be able to
    // mint the credential the guide tells them to use"). An embedder whose
    // users should never see mount instructions sets `connections: false`.
    connections: true,
    storages: roots.value,
    initialPath: opensOnHome.value ? '.home' : initialPathFromQuery.value || '',
    // "Open" / double-click → open the standalone editor in a new tab.
    // The route reads `?path=&type=&mode=` and mounts the right viewer
    // (OnlyOffice for office, Monaco for code, drawio iframe for
    // .drawio, image/PDF/3D viewers otherwise) with save-on-change.
    openPageBase: '/files/edit',
    viewerBaseUrl: '/files/edit',
    saveText: '/api/files/save-text',
    onlyOfficeConfig: '/api/files/onlyoffice/config',
  };
});

// gorunum:v2-topbar — `back()` is gone with the bar. It pushed the dashboard
// route, which is exactly what the "Admin panel" button beside it did: two
// buttons, one destination, both admin-only. The cluster's `admin` row is the
// survivor.

function onExplorerError(err: { message: string; context?: unknown }) {
  // eslint-disable-next-line no-console
  console.warn('[explore] FileExplorer error:', err);
}

onMounted(async () => {
  try {
    await auth.fetchMe();
    // Admin store fetch is best-effort (403s for non-admins) — roots then fall
    // back to manager-root discovery inside fetchVisibleStorages().
    await storages.fetch().catch(() => {});
    roots.value = await fetchVisibleStorages(storages.items);
  } finally {
    loading.value = false;
  }
  if (selectFromQuery.value) void revealSelection(selectFromQuery.value);
});
</script>

<template>
  <!-- ui-fix — h-screen (was min-h-screen): min-height lets the page GROW past
       the viewport when the explorer content is tall (e.g. grid view / split),
       so .fe (height:100%) grows with it and .fe__body's internal overflow:auto
       never engages → the whole PAGE scrolls. height:100vh caps the shell so the
       listing scrolls INSIDE each pane instead. -->
  <div class="h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950">
    <!-- gorunum:v2-topbar — THE PAGE HAS NO TOP BAR.
         What stood here (wordmark + tagline, Back, Admin panel, How to
         connect, Settings, Sign out, the dark-mode toggle and the language
         switcher) was a second header above the explorer's own, and half of
         it repeated a control the explorer already drew. Owner's decision:
         "Explore'da üst bara gerek yok… bizim üst bar olmayacak hiç."
         Where each one went:
           · Back + Admin panel → ONE `admin` button in the cluster below
             (they pushed the same route).
           · How to connect     → the navigation panel's own entry
             (`sidenav-connect`), which opens the same ConnectionsPanel.
           · Refresh            → the explorer's own Refresh, two rows down.
           · Dark mode          → settings modal, Preferences → Theme.
           · Language           → settings modal, Preferences → Language.
           · Settings, Sign out → the cluster below, inside the explorer's
             header, where the reference shell puts them. -->

    <main class="flex-1 flex flex-col min-h-0">
      <div
        v-if="loading"
        class="flex flex-1 flex-col items-center justify-center gap-4 text-zinc-500"
      >
        <span class="fx-explore-spinner" aria-hidden="true"></span>
        <p class="text-sm">{{ t('explore.loading') }}</p>
      </div>

      <div
        v-else-if="!roots.length"
        class="flex flex-col items-center justify-center gap-3 mt-16 text-sm text-zinc-500"
      >
        <!-- Two different facts wear the same empty screen. "No storages
             configured yet" is true for the operator and a guess for everyone
             else: a non-admin sees no storages when the instance has none AND
             when it has several that nobody granted them (RBAC on, docs/RBAC).
             Telling that user the server is empty sends them to configure
             something they cannot configure — the same admin-tool framing as
             the URL and the tab title in GitHub #14. -->
        <p>{{ auth.isAdmin ? t('explore.noStorage') : t('explore.noAccess') }}</p>
        <Button v-if="auth.isAdmin" size="sm" variant="primary" @click="router.push({ name: 'storages.new' })">
          {{ t('explore.addStorage') }}
        </Button>
        <!-- ⚠ The control again, and the reason it is data rather than markup.
             With no storage there is no explorer, and with no explorer there is
             no header — a user whose access was revoked would be looking at one
             sentence with no way to sign out or open their settings. Same rows,
             same handler, one definition. -->
        <div class="fe fx-explore-actions" data-testid="explore-empty-actions">
          <!-- The bell too: an account whose access was revoked is exactly
               the account that will next be told it was granted some. -->
          <NotificationBell v-if="auth.isAuthenticated" />
          <AccountMenu
            :actions="headerActions"
            :locale="locale === 'tr' ? 'tr' : 'en'"
            :fallback-label="t('explore.account')"
            @select="runAccountAction"
          />
        </div>
      </div>

      <div v-else-if="explorerConfig" class="flex-1 min-h-0 explore-host">
        <FileExplorer
          :key="`fx-multi-${remountKey}`"
          :config="explorerConfig"
          @error="onExplorerError"
          @refresh="rediscoverStorages /* gorunum:v2-topbar — the other half of Refresh */"
        >
          <!-- gorunum:v3-shell — the top bar's far-left corner. The explorer
               draws the collapse control there itself; this fills the rest of
               it with the product mark, which is OURS and not the package's:
               `<filex-explorer>` inside somebody else's app has no "filex"
               wordmark to show, so the slot arrives empty there and the
               toggle simply sits at the edge.

               ⚠ It is not a control. The reference shell makes its own logo
               the reload button; we do not, and the reason is the sentence
               above — an embed has no logo, so binding reload to it would be
               a control that exists on one surface and nowhere else. Refresh
               stays the explicit icon in the trailing cluster, where every
               surface has it. -->
          <template #brand>
            <LogoMark class="fx-explore-logo" />
            <span>filex</span>
          </template>

          <!-- gorunum:v2-topbar — the page's chrome, inside the explorer's
               header, on its trailing edge. This is the whole of what the top
               bar used to be. -->
          <template #header-actions>
            <!-- The notification bell, to the left of the avatar. Owner's
                 ruling, 2026-09-14, verbatim: *"üst bara zil koyalım."* It was
                 only ever drawn in the admin panel's top nav, so a non-admin —
                 for whom this page IS the product — received browser
                 notifications and had no way to open the list, mark one read
                 or follow one to its target.
                 ⚠ The SAME component as the admin nav's (NotificationBell.vue),
                 fed by the same store and the one poll in App.vue; a bell
                 written for this header would be a second list of the same
                 rows that sooner or later disagrees with the first.
                 ⚠ Guarded on a session: /explore is a public route (the demo
                 flow lands here signed out), and a bell for nobody would open
                 onto a 401. -->
            <NotificationBell v-if="auth.isAuthenticated" />
            <AccountMenu
              :actions="headerActions"
              :locale="locale === 'tr' ? 'tr' : 'en'"
              :fallback-label="t('explore.account')"
              @select="runAccountAction"
            />
          </template>
        </FileExplorer>
      </div>
    </main>

    <UserSettingsModal v-if="showSettings" v-model="showSettings" />
  </div>
</template>

<style scoped>
.explore-host {
  /* The FileExplorer SFC fills its host via flex layout. */
  display: flex;
  flex-direction: column;
  min-height: 0;
}
/* Direct child only. `.explore-host :deep(.fe)` reached every descendant
 * carrying the root class — the modal backdrop and the quick-look hint
 * both do — and beat the package's own rules on specificity, which is
 * how the hint pill ended up stretched to the full viewport (#22). The
 * explorer root is the only `.fe` we mean to size here. */
.explore-host > :deep(.fe) {
  flex: 1 1 auto;
  min-height: 0;
  height: 100%;
}
/* gorunum:v2-topbar — the empty state's copy of the cluster. `.fe` on the
 * wrapper so the package's own button rules (and its `--fe-*` tokens) reach
 * these three buttons: they are the same control as the ones in the header
 * and must not come out as a different button because they are outside the
 * explorer. */
/* gorunum:v4-wordmark — the mark in the explorer's header.
   ⚠ 28px, and it is not a free choice: the contributor's reference build draws
   its logo at 28×28 beside an 18px wordmark (measured signed-in at 1440), and
   the package sizes the `config.brand` fallback (`.fe-toolbar__markimg`) to the
   same 28 for the same reason — a slot-filled mark and a config-filled one
   coming out at two different heights is the split that rule exists to stop.
   It was 22px beside a 13px word, a third small in both.
   ⚠ It grows the brand block's CONTENT by 6px, which the gutter absorbs: the
   search field's left edge is the gutter's `min-width`, not this. Measured at
   255px and at the 192px the panel is being narrowed to — the field does not
   move at either. */
.fx-explore-logo {
  width: 28px;
  height: 28px;
  flex: 0 0 auto;
}
.fx-explore-actions {
  display: flex;
  /* ⚠⚠ The wrapper carries `.fe` for the package's tokens and button rules —
   * and with them the explorer ROOT's box: a 420px-tall bordered column
   * (`.fe { flex-direction: column; min-height: 420px; height: 100%; border }`).
   * Measured 2026-09-14 on a fresh install, the very first screen an operator
   * sees after signing in: the bell and the avatar stacked in a narrow framed
   * column hanging down the middle of the page. Undo every part of that box
   * here; this is a row of two controls, not an explorer. */
  flex-direction: row;
  min-height: 0;
  height: auto;
  border: 0;
  border-radius: 0;
  align-items: center;
  gap: 6px;
  background: transparent;
  /* The top bar's trailing corner is where these two live on every other
   * screen, so that is where they sit when there is no top bar. */
  position: fixed;
  top: 12px;
  right: 16px;
}
.fx-explore-spinner {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  border: 3px solid rgb(161 161 170 / 0.25); /* zinc-400/25 */
  border-top-color: #2f6ceb; /* the product blue (--fe-primary) — this spinner sits outside .fe */
  animation: fx-explore-spin 0.7s linear infinite;
}
@keyframes fx-explore-spin {
  to { transform: rotate(360deg); }
}
</style>
