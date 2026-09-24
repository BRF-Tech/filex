<script setup lang="ts">
/**
 * User settings — one modal for the things that belong to the PERSON, opened
 * from the account menu on every front door.
 *
 * Why it exists at all: a user's own settings were scattered across pages of
 * the admin panel, and the whole panel is admin-only (`router/index.ts` →
 * `meta: { requiresAdmin: true }`). The notification switches were the sharp
 * edge — `GET/PATCH /api/notifications/settings` is a per-USER endpoint open
 * to every account, and its only screen sat behind the admin gate, so the
 * people who receive notifications were the exact people who could not turn
 * them off. This modal is mounted on the admin chrome (TopNav), on Home and on
 * the standalone explorer, which between them are every screen a non-admin
 * can be on.
 *
 * ⚠⚠ EVERY CONTROL HERE HAS A READER. A switch that saves, reads back and
 * changes nothing is worse than a missing one: it answers a question the
 * product never asks again. Each field below names the endpoint or the store
 * that reads it.
 *
 * zaman:z1 — the time zone used to be listed BELOW as a control we refused to
 * draw, because `users.timezone` was written by the profile endpoint and read
 * by nothing at all: every date in the product came out in the browser's own
 * zone. It has a reader now — `@brftech/filex-core`'s `lib/timezone`, which
 * `useLocale.formatDate` (the explorer) and `lib/format.ts` (this app) both
 * format against — so the control is here, and it is the whole point of the
 * feature rather than a decoration: an instant is stored once and every person
 * reads it on their own clock.
 *
 * What the reference shell has and we do NOT draw, and why:
 *
 *   • Default upload folder, auto-open preview, upload conflict behaviour,
 *     job title — no column, no endpoint, no reader anywhere.
 *   • Active sessions — there is no session list endpoint. `/api/auth/*`
 *     offers me / profile / password / totp and nothing else; sessions are
 *     cookies the server does not enumerate.
 *   • AI assistant — the per-user half of that surface does not exist; the
 *     AI settings that do exist are instance-wide and admin-only. The rail
 *     DOES carry the item (the reference shell has five, and a missing one is
 *     read as a missing feature rather than as a deliberate absence), and the
 *     pane behind it says exactly the sentence above instead of drawing a
 *     switch. It promises nothing and offers nothing to press.
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Bell, ShieldCheck, SlidersHorizontal, Sparkles, User as UserIcon, X } from 'lucide-vue-next';

// ⚠ The `--fe-*` tokens this modal is drawn with live in core's stylesheet,
// and the admin panel does not load it: only the four views that embed the
// explorer do (Explore, Home, Connections, Editor). Without this import the
// modal opened from TopNav would resolve every token to nothing — a white box
// with unstyled text — while looking correct on Home. The sheet is namespaced
// (`.fe`, `.fx-*`, `filex-explorer`) apart from the `:root` custom properties,
// so pulling it in cannot restyle the panel around it.
import '@brftech/filex-core/style.css';

import { AuthApi } from '@/api/auth';
import { quotaApi, type QuotaSnapshot } from '@/api/quota';
import { extractError } from '@/api/client';
import { emailProblem, refusalField, usernameProblem } from '@/lib/accountRules';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useNotificationsStore } from '@/stores/notifications';
import { useToastStore } from '@/stores/toast';
import { setStoredLocale, type Locale } from '@/i18n';
import { ProductVersion, availableLocales, gateOnService } from '@brftech/filex-core';
import { getStoredTheme, setStoredTheme, type ThemeMode } from '@/lib/theme';
import { getDensity, setDensity, type Density } from '@/lib/density';
import { openTriggerPref, setOpenTriggerPref } from '@/lib/explorerConfig';
// tablo:t3 — the DEFAULT FOLDER VIEW: what a folder the person has never
// changed opens as. ⚠ It replaced the "remember each folder's view" switch,
// which was off by default — and off, every change in any folder became the
// view of every folder (owner, 2026-09-21: "tüm klasörlerde görünüm
// değişikliği geçerli oluyor"). A change now always belongs to its folder, and
// this is where the person says what an untouched one opens as. The state
// lives in the server-side document core keeps, so it follows the person
// between browsers and machines; an unset field follows the operator's
// instance default, and filex's own after that.
import {
  forgetAllFolders,
  instanceFolderDefault,
  personFolderDefault,
  rememberedCount,
  setPersonFolderDefault,
  type ViewMode,
} from '@brftech/filex-core';
import { formatBytes } from '@/lib/format';
// gorunum — the palette grid is core's component, mounted here. Not a copy:
// see ThemePalette.vue's header for why it had to be split out of the
// explorer's own gallery modal (a modal cannot be a settings pane).
import {
  ThemePalette,
  useThemeState,
  setTheme as setCorePalette,
  setThemeMode as setCoreThemeMode,
} from '@brftech/filex-core';
// zaman:z3 — the zone picker is core's component, mounted here. Not a copy:
// the embed's own "⋯ → Time zone" dialog mounts the very same one (see
// TimeZonePicker.vue's header for why it had to move out of this file).
import { TimeZonePicker, formatInstant, localeTag, personInitial, personName, resolvedTimeZone } from '@brftech/filex-core';
import { deviceTimeZone, getStoredTimeZone, setStoredTimeZone } from '@/lib/timezone';
import { getStartPage, setStartPage, type StartPage } from '@/lib/startPage';
// ⚠⚠ THE SAME LIST THE REMINDER SHOWS, not a second copy of it. The corner
// reminder (`InstallPrompt.vue`) can be closed for good — that is the owner's
// ruling, "kapanınca ack olduğu için tekrar gösterilmesin" — so the downloads
// need a home that is always there, for the person who closed it by mistake
// and for the person signing in from a laptop they bought this morning. The
// platform detection and the per-platform entries live in the composable and
// are RENDERED twice; writing them out again here is precisely what
// `web/tests/quality/duplication.test.ts` exists to catch.
import { useDesktopDownloads } from '@/composables/useInstallPrompt';
import { downscaleImageToDataURL } from '@/lib/image';
import { WEBHOOK_EVENTS, eventOffReason, userEventKey } from '@/lib/webhookEvents';
import {
  browserNotifyEnabled,
  browserNotifyPermission,
  isDesktopShell,
  requestBrowserNotifyPermission,
  setBrowserNotifyEnabled,
  type BrowserNotifyPermission,
} from '@/lib/browserNotify';

const props = defineProps<{
  modelValue: boolean;
  /** The pane to open on (the admin Notifications page opens `notifications`). */
  initialSection?: 'profile' | 'preferences' | 'notifications' | 'security' | 'ai';
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>();

const { t, locale } = useI18n();
const auth = useAuthStore();
const caps = useCapabilitiesStore();
const notif = useNotificationsStore();
const toast = useToastStore();

type Section = 'profile' | 'preferences' | 'notifications' | 'security' | 'ai';

const section = ref<Section>(props.initialSection ?? 'profile');
/**
 * ⚠ `ai` is listed, and it is listed as NOT HERE YET — the rail carries it so
 * the shape matches the reference shell, and the pane behind it says in one
 * sentence that there is nothing to set, because there is not: the AI settings
 * this server has are instance-wide and admin-only (see the header). Owner's
 * call, 2026-09-12: it shows as coming soon rather than as a feature, and it
 * promises nothing. The moment a per-user assistant setting exists, the
 * `soon` flag comes off and the pane fills; until then a rail item that
 * pretended to have controls would be the worse half of both options.
 */
const sections: { key: Section; icon: typeof UserIcon; soon?: boolean }[] = [
  { key: 'profile', icon: UserIcon },
  { key: 'preferences', icon: SlidersHorizontal },
  { key: 'notifications', icon: Bell },
  { key: 'security', icon: ShieldCheck },
  { key: 'ai', icon: Sparkles, soon: true },
];

const dialog = ref<HTMLDialogElement | null>(null);

function close() {
  emit('update:modelValue', false);
}

/* ── profile ─────────────────────────────────────────────────────────────
 * Reader: PATCH /api/auth/profile (backend auth_self.go → profileReq), whose
 * response replaces `auth.user`; GET /api/auth/me reads the same row back on
 * every cold load.
 *
 * ⚠ This pane sends the four identity fields and nothing else. The other two
 * the endpoint accepts, `locale` and `timezone`, are written from Preferences
 * — by the control that changes them, the moment it is used, because both take
 * effect on screen immediately and a person who has watched the UI switch
 * language does not then go looking for a Save button. */

const email = ref('');
const username = ref('');
const displayName = ref('');
const avatarUrl = ref('');
const avatarError = ref('');
const avatarInput = ref<HTMLInputElement | null>(null);
const savingProfile = ref(false);

/*
 * ⚠⚠ The e-mail and the username are checked WHILE they are typed
 * (lib/accountRules.ts, the mirror of the server's rules), and a refusal the
 * server still makes lands under the box it is about, in the words the
 * server wrote for the reader. Before (release-candidate sweep, 2026-09-21):
 * "bu-bir-eposta-degil" saved with "Profil kaydedildi", another account's
 * address answered "saved" and changed nothing, and "Ayşe Yılmaz!" as a
 * username came back as raw English in a toast.
 */
const serverRefusal = ref<{ field: string; message: string } | null>(null);
watch([email, username], () => {
  serverRefusal.value = null;
});
const emailError = computed(() => {
  if (serverRefusal.value?.field === 'email') return serverRefusal.value.message;
  // An account that never had an address may keep having none (the server
  // agrees: account_rules / UpdateProfile).
  if (!email.value.trim() && !(auth.user?.email ?? '')) return '';
  const p = emailProblem(email.value);
  return p ? t(`account.errors.${p.key}`, p.params ?? {}) : '';
});
const usernameError = computed(() => {
  if (serverRefusal.value?.field === 'username') return serverRefusal.value.message;
  const p = usernameProblem(username.value, auth.user?.username ?? '');
  return p ? t(`account.errors.${p.key}`, p.params ?? {}) : '';
});
const profileInvalid = computed(() => !!emailError.value || !!usernameError.value);

// Mirrors the server's cap (handlers.avatarMaxBytes) so a picture is resized
// to fit rather than 400-ing after the fact.
const AVATAR_MAX_BYTES = 48 * 1024;
const AVATAR_MAX_PX = 160;

const avatarInitial = computed(
  () =>
    personInitial(
      { display_name: displayName.value, username: username.value, email: email.value },
      localeTag(currentLocale.value),
    ) || '?',
);

function hydrateProfile() {
  const u = auth.user;
  if (!u) return;
  email.value = u.email;
  username.value = u.username ?? '';
  displayName.value = u.display_name;
  avatarUrl.value = u.avatar_url ?? '';
}

async function onAvatarFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (!file) return;
  avatarError.value = '';
  if (!file.type.startsWith('image/')) {
    avatarError.value = t('profile.avatar.notImage');
    return;
  }
  try {
    avatarUrl.value = await downscaleImageToDataURL(file, {
      maxPx: AVATAR_MAX_PX,
      maxBytes: AVATAR_MAX_BYTES,
    });
  } catch {
    avatarError.value = t('profile.avatar.failed');
  } finally {
    if (avatarInput.value) avatarInput.value.value = '';
  }
}

async function saveProfile() {
  // Nothing goes to the server while a box says what is wrong with it.
  if (profileInvalid.value) return;
  savingProfile.value = true;
  try {
    const u = await AuthApi.updateProfile({
      email: email.value.trim(),
      username: username.value.trim().toLowerCase(),
      display_name: displayName.value.trim(),
      avatar_url: avatarUrl.value,
    });
    auth.user = u;
    toast.success(t('profile.saved'));
  } catch (e: unknown) {
    const refusal = refusalField(e);
    if (refusal) serverRefusal.value = refusal;
    else toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingProfile.value = false;
  }
}

/* ── preferences ─────────────────────────────────────────────────────────
 * Language  → localStorage `filex.locale` (read by i18n/applyStoredLocale on
 *             every boot) AND `users.locale` via PATCH /api/auth/profile,
 *             which i18n/applyAccountLocale reads on a device that has not
 *             chosen for itself.
 * Time zone → localStorage `filex.timezone` (core's lib/timezone, read by
 *             useLocale.formatDate and by lib/format) AND `users.timezone` via
 *             PATCH /api/auth/profile, which stores/auth applyAccountTimeZone
 *             reads back on every cold load.
 * Mode      → localStorage `filex.theme`, read by lib/theme applyStoredTheme,
 *             and mirrored onto core's own `filex.thememode` pin.
 * Palette   → localStorage `filex.palette`, read by core's lib/themes.
 * Density   → localStorage `filex.density`, read by packages/core's Toolbar.
 * Storage   → GET /api/files/quota/me, read-only. */

/*
 * ⚠⚠ The OFFERED languages (packages/core lib/uiLocales `availableLocales`),
 * not a pair written here. This used to be `[en, tr]`, and `currentLocale`
 * mapped anything that was not `tr` to `en` — so a language pack's language
 * could not be chosen, and once chosen elsewhere this dialog claimed the
 * person was reading English. Reactive: a pack installed while the dialog is
 * open adds its button.
 */
const localeOptions = computed<{ value: Locale; label: string }[]>(() =>
  availableLocales().map((o) => ({ value: o.code, label: o.label || o.code })),
);

const currentLocale = computed<Locale>(() => locale.value);

async function pickLocale(v: Locale) {
  // setStoredLocale decides and applies (web/src/i18n settle) — including a
  // pack's language, whose strings it fetches.
  setStoredLocale(v);
  // Also on the account, so the next browser this person signs in from opens
  // in the language they chose here instead of the instance default.
  if (caps.demoReadOnly) return;
  try {
    const u = await AuthApi.updateProfile({ locale: v });
    auth.user = u;
  } catch {
    /* the device-local choice above already took effect; a failed account
       write is not worth a toast over a language the user can see changed */
  }
}

/* ── appearance: two axes, one pane ──────────────────────────────────────
 *
 * MODE is whether the light or the dark variant paints. PALETTE is which set
 * of colours that variant is made of. They are drawn as two clearly separate
 * rows under one heading because they compose — Night Blue has a light variant
 * and a dark one — and a reader who thinks the second control replaces the
 * first will "turn on dark mode" by clicking a palette and get the light
 * variant of a bluer theme.
 *
 * Both moved here from the explorer's toolbar, whose appearance controls are
 * gone: the owner asked for them in user settings, which is also where a
 * non-admin can reach them from every front door. */

const theme = ref<ThemeMode>('auto');
const themeOptions: { value: ThemeMode; label: string }[] = [
  { value: 'light', label: 'nav.themeLight' },
  { value: 'auto', label: 'nav.themeAuto' },
  { value: 'dark', label: 'nav.themeDark' },
];

const openTriggerOptions: { value: 'single' | 'double'; label: string }[] = [
  { value: 'double', label: 'userSettings.prefs.openTriggerDouble' },
  { value: 'single', label: 'userSettings.prefs.openTriggerSingle' },
];

// System-dark, watched — so the palette previews repaint when the OS flips
// while the modal is open on 'auto'.
const systemDark = ref(false);
let darkMq: MediaQueryList | null = null;
const onSystemDark = (e: MediaQueryListEvent) => (systemDark.value = e.matches);

/** Which variant the previews should paint — the same question `lib/theme`'s
 *  `effectiveTheme()` answers for the panel itself. */
const resolvedDark = computed(() =>
  theme.value === 'auto' ? systemDark.value : theme.value === 'dark',
);

function pickTheme(v: ThemeMode) {
  theme.value = v;
  setStoredTheme(v);
  // ⚠ And core's own pin. The explorer ranks an explicit `filex.thememode`
  // ABOVE the mode its host passes in `config.theme` (lib/themes: 'host' is
  // only the default). Anyone who ever used the explorer's old mode strip has
  // that pin set — so without this line the switch above would move the admin
  // chrome and leave the file listing on the old mode, with no control left
  // anywhere to un-pin it now that the strip is gone from the toolbar.
  setCoreThemeMode(v);
}

/* Palette — core owns the list, the selection and the persistence
 * (lib/themes.ts). This reads its reactive handle; it does not keep a copy. */
const { themeId: paletteId } = useThemeState();

function pickPalette(id: string) {
  setCorePalette(id);
}

/* ── time zone ───────────────────────────────────────────────────────────
 *
 * The owner's requirement, verbatim (translated from Turkish): "the time zone
 * is a very important thing. I uploaded a video at 03:00; the person on GMT+0
 * will see that video's upload time in their OWN time zone."
 *
 * So the stored instant never moves and the READER picks the clock. The select
 * writes both halves in one handler: core's localStorage mirror (so the very
 * first paint after a reload is already right, before /api/auth/me lands) and
 * `users.timezone` on the account (so the next browser this person signs in
 * from agrees). */

const timeZone = ref<string>('');
const savingTz = ref(false);

const deviceZone = computed(() => deviceTimeZone());

/* Stable per-instance id, so the field's own `<label for>` names the
 * combobox (two settings modals can be mounted on one page). */
const tzInputId = `fx-us-tz-${Math.random().toString(36).slice(2, 9)}`;

/* ⚠ The picker's first row is "use this device's zone": what an EMPTY account
 * zone resolves to for this person. The admin app sets no host zone, and
 * picking here clears this browser's viewer pick (lib/timezone
 * setStoredTimeZone), so the device is exactly what `''` means here. */
const tzFallback = { zone: undefined, tier: 'device' } as const;

/** The zone every date on this page is drawn in right now — from the ONE
 *  resolver, not from this modal's copy of the account's value. */
const tzInForce = computed(() => resolvedTimeZone().zone ?? deviceZone.value);

/** "Right now it is 03:14 there" — the one readout that makes a zone name
 *  concrete, and the fastest way for a person to tell they picked wrong. */
const tzNow = ref('');
let tzTimer: ReturnType<typeof setInterval> | undefined;

function refreshTzNow() {
  // Through core's formatter, which reads the RESOLVED zone — the readout says
  // what the page is actually drawn in.
  tzNow.value = formatInstant(new Date(), currentLocale.value, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

async function pickTimeZone(v: string) {
  timeZone.value = v;
  // Local first: the dates on screen repaint on this line, and they must do so
  // whether or not the network is there.
  setStoredTimeZone(v);
  refreshTzNow();
  if (caps.demoReadOnly) return;
  savingTz.value = true;
  try {
    const u = await AuthApi.updateProfile({ timezone: v });
    auth.user = u;
  } catch (e: unknown) {
    // ⚠ Surfaced, unlike the language above. A language you can SEE is wrong;
    // a zone that failed to reach the account looks perfect on this device and
    // is simply absent on the next one.
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingTz.value = false;
  }
}

const density = ref<Density>('comfortable');

/* ── the default folder view ─────────────────────────────────────────── */

type FvSort = 'name' | 'type' | 'modified' | 'size';
const FV_VIEWS: ViewMode[] = ['list', 'grid', 'gallery'];
const FV_SORTS: FvSort[] = ['name', 'type', 'modified', 'size'];

/* Read straight from core's reactive state — the modal holds no copy, so it
   cannot show a default the explorer is not using. */
const fvPerson = computed(() => personFolderDefault());
const fvInstance = computed(() => instanceFolderDefault());
const fvFoldersKept = computed(() => rememberedCount());

/** What "follow the server" resolves to right now, so the choice that says
 *  "inherit" also says WHAT it inherits — a default nobody can read is a
 *  default nobody can predict. */
const fvInheritView = computed<ViewMode>(() => fvInstance.value.v ?? 'list');
const fvInheritSort = computed(() => {
  const k: FvSort = fvInstance.value.k ?? 'name';
  const d = fvInstance.value.d ?? (k === 'modified' ? 'desc' : 'asc');
  return `${t(`userSettings.prefs.fvSort_${k}`)} ${d === 'asc' ? '↑' : '↓'}`;
});

function pickFolderView(v: ViewMode | '') {
  setPersonFolderDefault({ v: v || undefined });
}
function pickFolderSort(k: FvSort | '') {
  /* A new key arrives in its own natural direction (dates newest first), the
     same rule a column header follows. */
  setPersonFolderDefault({ k: k || undefined, d: k ? (k === 'modified' ? 'desc' : 'asc') : undefined });
}
function pickFolderDir(d: 'asc' | 'desc') {
  if (!fvPerson.value.k) return;
  setPersonFolderDefault({ d });
}
function resetDefaultColumns() {
  setPersonFolderDefault({ c: undefined });
}

function pickDensity(compact: boolean) {
  density.value = compact ? 'compact' : 'comfortable';
  setDensity(density.value);
}

// How a mouse opens a file: 'double' (single click selects, double opens) or
// 'single' (first click opens). Touch always taps-to-open regardless. Writing
// it re-applies live — the explorer's config recomputes (see lib/explorerConfig).
const openTrigger = ref<'single' | 'double'>('double');
function pickOpenTrigger(value: 'single' | 'double') {
  openTrigger.value = value;
  setOpenTriggerPref(value);
}

const quota = ref<QuotaSnapshot | null>(null);

const quotaLine = computed(() => {
  const q = quota.value;
  if (!q) return '';
  const used = formatBytes(q.used_bytes, currentLocale.value);
  if (q.unlimited || !q.quota_bytes) {
    return t('quota.unlimitedTooltip', { used });
  }
  return t('quota.tooltip', {
    used,
    limit: formatBytes(q.quota_bytes, currentLocale.value),
    percent: `${Math.round(q.percent_used)}%`,
  });
});

/* ── start page ──────────────────────────────────────────────────────────
 * Reader: router/index.ts — the `/` front door and the already-signed-in
 * /login bounce both ask lib/startPage which route to open. Owner's decision,
 * 2026-09-12: selectable rather than one landing forced on everyone.
 *
 * ⚠ "Admin panel" is not offered to a non-admin AT ALL. Rendering a disabled
 * row would advertise a door; rendering an enabled one would hand out a choice
 * the router has to refuse on every launch. (`startRouteName` degrades it to
 * Home anyway, for the account that is demoted after choosing it.) */

const startPage = ref<StartPage | null>(null);

const startPageOptions = computed<StartPage[]>(() =>
  auth.isAdmin ? ['home', 'files', 'admin'] : ['home', 'files'],
);

function pickStartPage(v: StartPage) {
  startPage.value = v;
  setStartPage(v);
}

/* ── desktop app ─────────────────────────────────────────────────────────
 * Reader: none — these are links to GitHub release assets, so the "every
 * control here has a reader" rule is satisfied in the only way it can be for
 * a download: the thing that reads them is the browser, and a wrong entry is
 * a 404 the person sees immediately. What matters instead is that there is
 * exactly ONE list (see the import) so this pane and the reminder cannot point
 * at different files.
 *
 * ⚠ Drawn only when there is a build for the machine this browser is on.
 * `platform` is null on a phone and inside the Electron shell itself — the
 * first has nothing to install a desktop app onto, the second already IS the
 * desktop app, and a group headed "Desktop app" with a GitHub link and no
 * answer would be worse than no group. Same judgement as the `ai` pane above:
 * offer nothing rather than pretend. */
const {
  platform: desktopPlatform,
  platformLabel: desktopPlatformLabel,
  downloads: desktopDownloads,
  releasesUrl: desktopReleasesUrl,
} = useDesktopDownloads();

/* ── notifications ───────────────────────────────────────────────────────
 * In-app + per-event mutes → GET/PATCH /api/notifications/settings, a
 * per-user endpoint every account may call (routes.go, outside the admin
 * block). The mutes gate the READ: notify/service.go bellPrefs feeds them to
 * ListNotifications and UnreadNotificationCount.
 * Browser notifications → lib/browserNotify, the same module the bell's
 * watcher calls before it raises a toast. Not a second implementation. */

const permission = ref<BrowserNotifyPermission>('unsupported');
const browserOn = ref(true);
const desktopShell = ref(false);
const savingNotif = ref(false);

const inAppOn = computed(() => notif.settings?.in_app_enabled !== false);
const mutedEvents = computed<string[]>(() => notif.settings?.muted_events ?? []);
// The events that can happen on this instance (lib/webhookEvents
// eventOffReason): a switch for virus hits with scanning off, or for the
// escrow key where there is none, is a promise the product cannot keep. ⚠ The
// same split as every "needs a service" entry (core lib/serviceGate): an
// ADMINISTRATOR, who can switch the service on, sees it greyed with the reason
// (QA #39); everybody else is not offered it at all.
const offeredEvents = computed(() =>
  WEBHOOK_EVENTS.map((ev) => {
    const off = eventOffReason(ev, caps.data);
    const gate = gateOnService(off === null, caps.data.caller_admin === true, off ? t(off) : '');
    return { ev, off, gate };
  }).filter((row) => !row.gate.hidden),
);

async function patchSettings(inApp: boolean, muted: string[]) {
  savingNotif.value = true;
  try {
    // ⚠ PATCH replaces the WHOLE preference — sending one half clears the
    // other (docs/NOTIFICATIONS.md → Per-user settings).
    await notif.updateSettings({ in_app_enabled: inApp, muted_events: muted });
    toast.success(t('notifications.prefs.saved'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingNotif.value = false;
  }
}

function setInApp(v: boolean) {
  void patchSettings(v, mutedEvents.value);
}

/** A switch per event, ON meaning "tell me". Muting is the stored half. */
function setEventOn(event: string, on: boolean) {
  const next = new Set(mutedEvents.value);
  if (on) next.delete(event);
  else next.add(event);
  void patchSettings(inAppOn.value, [...next]);
}

function setBrowser(v: boolean) {
  browserOn.value = v;
  setBrowserNotifyEnabled(v, auth.user?.id);
}

/** ⚠ From a click and from nowhere else — see lib/browserNotify's header. */
async function askPermission() {
  permission.value = await requestBrowserNotifyPermission(auth.user?.id);
  if (permission.value === 'granted') setBrowser(true);
}

/* ── security ────────────────────────────────────────────────────────────
 * Password → POST /api/auth/password. TOTP → POST /api/auth/totp/{enroll,
 * verify,disable}. Nothing else on this pane, because nothing else has an
 * endpoint (see the header note on active sessions). */

const currentPassword = ref('');
const newPassword = ref('');
const newPasswordConfirm = ref('');
const savingPassword = ref(false);

async function changePassword() {
  if (newPassword.value !== newPasswordConfirm.value) {
    toast.warn(t('errors.validationFailed'));
    return;
  }
  savingPassword.value = true;
  try {
    await AuthApi.changePassword(currentPassword.value, newPassword.value);
    currentPassword.value = '';
    newPassword.value = '';
    newPasswordConfirm.value = '';
    toast.success(t('profile.passwordChanged'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingPassword.value = false;
  }
}

const totpEnabled = computed(() => auth.user?.totp_enabled ?? false);
const totpStage = ref<'idle' | 'enroll' | 'disable'>('idle');
const totpQr = ref('');
const totpSecret = ref('');
// The server generates ten codes, stores them against the pending secret and
// returns them ONCE. Not rendering them means the user has ten valid codes
// nobody ever showed them.
const totpRecoveryCodes = ref<string[]>([]);
const totpCode = ref('');
const totpBusy = ref(false);

async function startTotp() {
  totpBusy.value = true;
  try {
    const res = await AuthApi.enrollTotp();
    totpSecret.value = res.secret;
    totpQr.value = res.qr_svg;
    totpRecoveryCodes.value = res.recovery_codes ?? [];
    totpStage.value = 'enroll';
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    totpBusy.value = false;
  }
}

async function verifyTotp() {
  totpBusy.value = true;
  try {
    await AuthApi.verifyTotp(totpCode.value);
    if (auth.user) auth.user.totp_enabled = true;
    resetTotp();
    toast.success(t('userSettings.security.totpEnabled'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    totpBusy.value = false;
  }
}

async function disableTotp() {
  totpBusy.value = true;
  try {
    await AuthApi.disableTotp(totpCode.value);
    if (auth.user) auth.user.totp_enabled = false;
    resetTotp();
    toast.success(t('userSettings.security.totpDisabled'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    totpBusy.value = false;
  }
}

function resetTotp() {
  totpStage.value = 'idle';
  totpQr.value = '';
  totpSecret.value = '';
  totpRecoveryCodes.value = [];
  totpCode.value = '';
}

/* ── open / close ────────────────────────────────────────────────────── */

function sync(open: boolean) {
  {
    const el = dialog.value;
    if (open) {
      hydrateProfile();
      theme.value = getStoredTheme();
      density.value = getDensity();
      openTrigger.value = openTriggerPref();
      timeZone.value = getStoredTimeZone();
      startPage.value = getStartPage();
      refreshTzNow();
      desktopShell.value = isDesktopShell();
      permission.value = browserNotifyPermission();
      browserOn.value = browserNotifyEnabled(auth.user?.id);
      resetTotp();
      // Both are best-effort: a modal that will not open because one GET
      // failed is worse than a pane that says nothing.
      void notif.fetchSettings();
      quotaApi
        .me()
        .then((q) => (quota.value = q))
        .catch(() => (quota.value = null));
      if (el && !el.open) el.showModal();
    } else if (el?.open) {
      el.close();
    }
  }
}

// ⚠ `onMounted` AND the watch, not `{ immediate: true }`. The parents mount
// this lazily (`v-if="showSettings"`), so the very first value the component
// ever sees is already `true` — and an immediate watcher runs during setup,
// when the <dialog> ref is still null, so `showModal()` would never be called
// and the modal would silently not open on the click that created it.
onMounted(() => {
  sync(props.modelValue);
  // The zone readout is a clock; a clock that does not move is a screenshot.
  // One second is the unit it prints, so one second is what it ticks on.
  refreshTzNow();
  tzTimer = setInterval(refreshTzNow, 1000);
  try {
    darkMq = window.matchMedia('(prefers-color-scheme: dark)');
    systemDark.value = darkMq.matches;
    darkMq.addEventListener('change', onSystemDark);
  } catch {
    /* engine without matchMedia — 'auto' then previews as light, which is
       what `lib/theme.effectiveTheme()` falls back to as well. */
  }
});
watch(() => props.modelValue, sync, { flush: 'post' });

onBeforeUnmount(() => {
  if (tzTimer) clearInterval(tzTimer);
  darkMq?.removeEventListener('change', onSystemDark);
});

/** Whether the footer shows Cancel + Save rather than a single way out.
 *  Profile is the only tab that stages anything, and the demo account cannot
 *  save at all — on both of those "Cancel" would name a discard that is not
 *  happening. */
const profileSaveVisible = computed(() => section.value === 'profile' && !caps.demoReadOnly);

function onCancel(ev: Event) {
  ev.preventDefault();
  close();
}

function onBackdropClick(ev: MouseEvent) {
  if (ev.target === dialog.value) close();
}
</script>

<template>
  <dialog
    ref="dialog"
    class="fx-us-dialog"
    data-testid="user-settings-dialog"
    @cancel="onCancel"
    @click="onBackdropClick"
  >
    <div class="fe fx-us" role="document">
      <!-- ── the modal's own head ─────────────────────────────────────
           One bar across the FULL width — who you are, what this dialog is,
           and the way out — with the rail starting underneath it. The rail
           used to carry a "SETTINGS" caption of its own and the close button
           sat inside the right-hand pane, which read as two dialogs side by
           side rather than one. -->
      <header class="fx-us__head">
        <img v-if="avatarUrl" :src="avatarUrl" alt="" class="fx-us__head-avatar" />
        <span v-else class="fx-us__head-avatar fx-us__head-avatar--initial" aria-hidden="true">
          {{ avatarInitial }}
        </span>
        <div class="fx-us__head-text">
          <h2 class="fx-us__title">{{ t('userSettings.heading') }}</h2>
          <p class="fx-us__subtitle">{{ t('userSettings.headingSub') }}</p>
        </div>
        <!-- Which filex this is — at the end of the head, before the way out,
             quiet: the same piece the account menus draw (Burak, 2026-09-24).
             Nothing is drawn while the server's answer is not in. -->
        <ProductVersion :version="caps.data.version" class="fx-us__version" />
        <button
          type="button"
          class="fx-us__icon-btn"
          :aria-label="t('common.close')"
          data-testid="user-settings-close"
          @click="close"
        >
          <X class="fx-us__rail-icon" aria-hidden="true" />
        </button>
      </header>

      <!-- ── rail ─────────────────────────────────────────────────── -->
      <nav class="fx-us__rail" :aria-label="t('userSettings.title')">
        <button
          v-for="s in sections"
          :key="s.key"
          type="button"
          class="fx-us__rail-item"
          :class="{ 'is-active': section === s.key }"
          :aria-current="section === s.key ? 'page' : undefined"
          :data-testid="`user-settings-tab-${s.key}`"
          @click="section = s.key"
        >
          <component :is="s.icon" class="fx-us__rail-icon" aria-hidden="true" />
          <span class="fx-us__rail-label">{{ t(`userSettings.tabs.${s.key}`) }}</span>
          <span v-if="s.soon" class="fx-us__rail-soon">{{ t('userSettings.ai.soon') }}</span>
        </button>
      </nav>

      <!-- ── pane ─────────────────────────────────────────────────── -->
      <div class="fx-us__main">
        <div class="fx-us__body">
          <div class="fx-us__panehead">
            <h3 class="fx-us__panetitle">{{ t(`userSettings.tabs.${section}`) }}</h3>
            <p class="fx-us__subtitle">{{ t(`userSettings.subtitle.${section}`) }}</p>
          </div>
          <!-- ── Profile ────────────────────────────────────────── -->
          <section v-if="section === 'profile'" class="fx-us__pane" data-testid="user-settings-profile">
            <!-- Who this account IS, in one row: the picture, the identity
                 the rest of the product shows you by, whether you are an
                 admin, and the one button that changes the picture. The
                 badge is read off `auth.isAdmin` (the account's real role),
                 not decoration. -->
            <div class="fx-us__identity">
              <img v-if="avatarUrl" :src="avatarUrl" :alt="t('profile.avatar.label')" class="fx-us__avatar" />
              <span v-else class="fx-us__avatar fx-us__avatar--initial" aria-hidden="true">
                {{ avatarInitial }}
              </span>
              <div class="fx-us__identity-text">
                <p class="fx-us__identity-name">
                  <span class="fx-us__identity-who">{{ personName({ display_name: displayName, username, email }) }}</span>
                  <span v-if="auth.isAdmin" class="fx-us__badge">{{ t('userSettings.adminBadge') }}</span>
                </p>
                <p class="fx-us__identity-sub">{{ email }}</p>
              </div>
              <button
                type="button"
                class="fx-us__btn fx-us__identity-change"
                :disabled="caps.demoReadOnly"
                @click="avatarInput?.click()"
              >
                {{ t('profile.avatar.change') }}
              </button>
              <button
                v-if="avatarUrl"
                type="button"
                class="fx-us__btn fx-us__btn--quiet"
                :disabled="caps.demoReadOnly"
                @click="avatarUrl = ''"
              >
                {{ t('common.remove') }}
              </button>
              <input
                ref="avatarInput"
                type="file"
                accept="image/*"
                class="fx-us__file"
                @change="onAvatarFile"
              />
            </div>
            <p class="fx-us__hint">{{ t('profile.avatar.help') }}</p>
            <p v-if="avatarError" class="fx-us__hint fx-us__hint--bad">{{ avatarError }}</p>

            <div class="fx-us__grid">
              <label class="fx-us__field">
                <span class="fx-us__label">{{ t('users.fields.displayName') }}</span>
                <input v-model="displayName" class="fx-us__input" :disabled="caps.demoReadOnly" />
              </label>
              <label class="fx-us__field">
                <span class="fx-us__label">
                  {{ t('common.email') }}
                  <span class="fx-us__required" aria-hidden="true">*</span>
                </span>
                <input
                  v-model="email"
                  type="email"
                  class="fx-us__input"
                  :class="{ 'is-invalid': emailError }"
                  autocomplete="email"
                  required
                  :aria-invalid="emailError ? 'true' : undefined"
                  :disabled="caps.demoReadOnly"
                  data-testid="profile-email"
                />
                <span v-if="emailError" class="fx-us__hint fx-us__hint--bad" role="alert" data-testid="profile-email-error">
                  {{ emailError }}
                </span>
              </label>
            </div>

            <label class="fx-us__field">
              <span class="fx-us__label">
                {{ t('profile.username.label') }}
                <span class="fx-us__required" aria-hidden="true">*</span>
              </span>
              <input
                v-model="username"
                class="fx-us__input"
                :class="{ 'is-invalid': usernameError }"
                autocomplete="username"
                required
                :aria-invalid="usernameError ? 'true' : undefined"
                :disabled="caps.demoReadOnly"
                data-testid="profile-username"
              />
              <span v-if="usernameError" class="fx-us__hint fx-us__hint--bad" role="alert" data-testid="profile-username-error">
                {{ usernameError }}
              </span>
              <span v-else class="fx-us__hint">{{ t('profile.username.help') }}</span>
            </label>

            <p v-if="caps.demoReadOnly" class="fx-us__note">{{ t('userSettings.demoReadOnly') }}</p>
          </section>

          <!-- ── Preferences ────────────────────────────────────── -->
          <section
            v-else-if="section === 'preferences'"
            class="fx-us__pane"
            data-testid="user-settings-preferences"
          >
            <!-- ⚠ A segmented strip, not a select — the owner's call, and the
                 shape already used by Start page and Appearance below. Two
                 options do not earn a dropdown: a select hides half the answer
                 behind a click and then asks for a second one. ⚠ If a third
                 language ever ships, measure the strip before adding a fourth
                 button — a segmented control stops being readable somewhere
                 around four, and that is the point to go back to a select. -->
            <div class="fx-us__field">
              <span class="fx-us__label">{{ t('profile.locale') }}</span>
              <div
                class="fx-us__segmented"
                role="group"
                :aria-label="t('profile.locale')"
                data-testid="user-settings-locale"
              >
                <button
                  v-for="o in localeOptions"
                  :key="o.value"
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': currentLocale === o.value }"
                  :aria-pressed="currentLocale === o.value"
                  :data-testid="`user-settings-locale-${o.value}`"
                  @click="pickLocale(o.value)"
                >
                  {{ o.label }}
                </button>
              </div>
              <span class="fx-us__hint">{{ t('userSettings.prefs.languageHint') }}</span>
            </div>

            <!-- ── time zone ─────────────────────────────
                 core's TimeZonePicker — the same combobox the embed's own
                 "⋯ → Time zone" dialog mounts. What a pick WRITES is this
                 modal's business (`pickTimeZone`: the account, and this
                 browser's copy of it); the control is shared. -->
            <div class="fx-us__field">
              <label class="fx-us__label" :for="tzInputId">
                {{ t('userSettings.prefs.timezone') }}
              </label>
              <TimeZonePicker
                data-testid="user-settings-timezone"
                testid="user-settings-tz"
                :input-id="tzInputId"
                :locale="currentLocale"
                :model-value="timeZone"
                :fallback="tzFallback"
                :label="t('userSettings.prefs.timezone')"
                :disabled="savingTz"
                @update:model-value="pickTimeZone"
              />
              <span class="fx-us__hint">
                {{ t('userSettings.prefs.timezoneHint') }}
              </span>
              <span class="fx-us__readout" data-testid="user-settings-tz-now">
                {{ t('userSettings.prefs.timezoneNow', { zone: tzInForce, time: tzNow }) }}
              </span>
            </div>

            <div class="fx-us__field">
              <span class="fx-us__label">{{ t('userSettings.prefs.startPage') }}</span>
              <div
                class="fx-us__segmented"
                role="group"
                :aria-label="t('userSettings.prefs.startPage')"
              >
                <button
                  v-for="o in startPageOptions"
                  :key="o"
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': startPage === o }"
                  :aria-pressed="startPage === o"
                  :data-testid="`user-settings-start-${o}`"
                  @click="pickStartPage(o)"
                >
                  {{ t(`userSettings.prefs.startPage_${o}`) }}
                </button>
              </div>
              <span class="fx-us__hint">{{ t('userSettings.prefs.startPageHint') }}</span>
            </div>

            <!-- Appearance: two axes, one heading. The strip answers WHICH
                 VARIANT paints, the grid answers WHICH COLOURS it is made of,
                 and they compose — so they are drawn as two labelled rows of
                 one group rather than as two controls that look alike. -->
            <div class="fx-us__group" data-testid="user-settings-appearance">
              <span class="fx-us__group-title">{{ t('userSettings.prefs.appearance') }}</span>

              <div class="fx-us__field">
                <span class="fx-us__label">{{ t('userSettings.prefs.mode') }}</span>
                <div class="fx-us__segmented" role="group" :aria-label="t('userSettings.prefs.mode')">
                  <button
                    v-for="o in themeOptions"
                    :key="o.value"
                    type="button"
                    class="fx-us__seg"
                    :class="{ 'is-active': theme === o.value }"
                    :aria-pressed="theme === o.value"
                    :data-testid="`user-settings-theme-${o.value}`"
                    @click="pickTheme(o.value)"
                  >
                    {{ t(o.label) }}
                  </button>
                </div>
                <span class="fx-us__hint">{{ t('userSettings.prefs.modeHint') }}</span>
              </div>

              <div class="fx-us__field">
                <span class="fx-us__label">{{ t('userSettings.prefs.palette') }}</span>
                <ThemePalette
                  :locale="currentLocale"
                  :dark="resolvedDark"
                  :current="paletteId"
                  :hint="false"
                  @select="pickPalette"
                />
                <span class="fx-us__hint">{{ t('userSettings.prefs.paletteHint') }}</span>
              </div>
            </div>

            <div class="fx-us__switch-row">
              <button
                type="button"
                role="switch"
                class="fx-us__switch"
                :class="{ 'is-on': density === 'compact' }"
                :aria-checked="density === 'compact'"
                data-testid="user-settings-density"
                @click="pickDensity(density !== 'compact')"
              >
                <span class="fx-us__switch-knob" />
              </button>
              <div class="fx-us__switch-text">
                <span class="fx-us__label">{{ t('userSettings.prefs.compact') }}</span>
                <span class="fx-us__hint">{{ t('userSettings.prefs.compactHint') }}</span>
              </div>
            </div>

            <!-- tablo:t3 — the default folder view. Every choice has an
                 "inherit" option that NAMES what it inherits, because the
                 operator's default can change underneath a person who never
                 picked one, and they should be able to see that it did. -->
            <div class="fx-us__field" data-testid="user-settings-folder-view">
              <span class="fx-us__label">{{ t('userSettings.prefs.folderView') }}</span>
              <span class="fx-us__hint">{{ t('userSettings.prefs.folderViewHint') }}</span>

              <span class="fx-us__label fx-us__label--sub">{{ t('userSettings.prefs.fvMode') }}</span>
              <div class="fx-us__segmented" role="group" :aria-label="t('userSettings.prefs.fvMode')">
                <button
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': !fvPerson.v }"
                  :aria-pressed="!fvPerson.v"
                  data-testid="user-settings-fv-view-inherit"
                  @click="pickFolderView('')"
                >
                  {{ t('userSettings.prefs.fvInherit', { value: t(`userSettings.prefs.fvView_${fvInheritView}`) }) }}
                </button>
                <button
                  v-for="v in FV_VIEWS"
                  :key="v"
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': fvPerson.v === v }"
                  :aria-pressed="fvPerson.v === v"
                  :data-testid="`user-settings-fv-view-${v}`"
                  @click="pickFolderView(v)"
                >
                  {{ t(`userSettings.prefs.fvView_${v}`) }}
                </button>
              </div>

              <span class="fx-us__label fx-us__label--sub">{{ t('userSettings.prefs.fvSort') }}</span>
              <div class="fx-us__segmented" role="group" :aria-label="t('userSettings.prefs.fvSort')">
                <button
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': !fvPerson.k }"
                  :aria-pressed="!fvPerson.k"
                  data-testid="user-settings-fv-sort-inherit"
                  @click="pickFolderSort('')"
                >
                  {{ t('userSettings.prefs.fvInherit', { value: fvInheritSort }) }}
                </button>
                <button
                  v-for="k in FV_SORTS"
                  :key="k"
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': fvPerson.k === k }"
                  :aria-pressed="fvPerson.k === k"
                  :data-testid="`user-settings-fv-sort-${k}`"
                  @click="pickFolderSort(k)"
                >
                  {{ t(`userSettings.prefs.fvSort_${k}`) }}
                </button>
              </div>
              <div
                v-if="fvPerson.k"
                class="fx-us__segmented"
                role="group"
                :aria-label="t('userSettings.prefs.fvDir')"
              >
                <button
                  v-for="d in (['asc', 'desc'] as const)"
                  :key="d"
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': (fvPerson.d ?? (fvPerson.k === 'modified' ? 'desc' : 'asc')) === d }"
                  :aria-pressed="(fvPerson.d ?? (fvPerson.k === 'modified' ? 'desc' : 'asc')) === d"
                  :data-testid="`user-settings-fv-dir-${d}`"
                  @click="pickFolderDir(d)"
                >
                  {{ t(`userSettings.prefs.fvDir_${d}`) }}
                </button>
              </div>

              <span v-if="fvPerson.c" class="fx-us__hint">
                {{ t('userSettings.prefs.fvColumns') }}
                <button type="button" class="fx-us__link" data-testid="user-settings-fv-columns-reset" @click="resetDefaultColumns">
                  {{ t('userSettings.prefs.fvColumnsReset') }}
                </button>
              </span>
              <span v-if="fvFoldersKept > 0" class="fx-us__hint">
                {{ t('userSettings.prefs.fvKept', { count: fvFoldersKept }, fvFoldersKept) }}
                <button type="button" class="fx-us__link" data-testid="user-settings-fv-forget" @click="forgetAllFolders()">
                  {{ t('userSettings.prefs.fvForget') }}
                </button>
              </span>
            </div>

            <div class="fx-us__field">
              <span class="fx-us__label">{{ t('userSettings.prefs.openTrigger') }}</span>
              <div class="fx-us__segmented" role="group" :aria-label="t('userSettings.prefs.openTrigger')">
                <button
                  v-for="o in openTriggerOptions"
                  :key="o.value"
                  type="button"
                  class="fx-us__seg"
                  :class="{ 'is-active': openTrigger === o.value }"
                  :aria-pressed="openTrigger === o.value"
                  :data-testid="`user-settings-opentrigger-${o.value}`"
                  @click="pickOpenTrigger(o.value)"
                >
                  {{ t(o.label) }}
                </button>
              </div>
              <span class="fx-us__hint">{{ t('userSettings.prefs.openTriggerHint') }}</span>
            </div>

            <!-- The downloads, permanently. The corner reminder is dismissible
                 for good; this is where it goes on living. -->
            <div
              v-if="desktopPlatform"
              class="fx-us__group"
              data-testid="user-settings-desktop-app"
            >
              <span class="fx-us__group-title">{{ t('userSettings.prefs.desktopApp') }}</span>
              <p class="fx-us__readout">
                {{ t('install.desktopSubtitle', { platform: desktopPlatformLabel }) }}
              </p>
              <div class="fx-us__dl-list">
                <a
                  v-for="d in desktopDownloads"
                  :key="d.href"
                  :href="d.href"
                  class="fx-us__dl"
                  data-testid="user-settings-desktop-download"
                >
                  <span class="fx-us__dl-text">
                    <span class="fx-us__label">{{ d.label }}</span>
                    <span class="fx-us__hint">{{ d.hint }}</span>
                  </span>
                  <span aria-hidden="true" class="fx-us__dl-arrow">↓</span>
                </a>
              </div>
              <a
                :href="desktopReleasesUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="fx-us__dl-all"
              >
                {{ t('install.desktopAllDownloads') }}
              </a>
              <span class="fx-us__hint">{{ t('userSettings.prefs.desktopAppHint') }}</span>
            </div>

            <div class="fx-us__field">
              <span class="fx-us__label">{{ t('quota.title') }}</span>
              <p class="fx-us__readout" data-testid="user-settings-quota">
                {{ quotaLine || t('common.loading') }}
              </p>
            </div>
          </section>

          <!-- ── Notifications ──────────────────────────────────── -->
          <section
            v-else-if="section === 'notifications'"
            class="fx-us__pane"
            data-testid="user-settings-notifications"
          >
            <div class="fx-us__switch-row">
              <button
                type="button"
                role="switch"
                class="fx-us__switch"
                :class="{ 'is-on': inAppOn }"
                :aria-checked="inAppOn"
                :disabled="savingNotif"
                data-testid="user-settings-inapp"
                @click="setInApp(!inAppOn)"
              >
                <span class="fx-us__switch-knob" />
              </button>
              <div class="fx-us__switch-text">
                <span class="fx-us__label">{{ t('notifications.prefs.inApp') }}</span>
                <span class="fx-us__hint">{{ t('notifications.prefs.inAppHint') }}</span>
              </div>
            </div>

            <div class="fx-us__switch-row">
              <button
                type="button"
                role="switch"
                class="fx-us__switch"
                :class="{ 'is-on': browserOn }"
                :aria-checked="browserOn"
                :disabled="desktopShell || permission === 'unsupported'"
                data-testid="user-settings-browser"
                @click="setBrowser(!browserOn)"
              >
                <span class="fx-us__switch-knob" />
              </button>
              <div class="fx-us__switch-text">
                <span class="fx-us__label">{{ t('notifications.prefs.browser') }}</span>
                <span class="fx-us__hint">{{ t('notifications.prefs.browserHint') }}</span>
                <div class="fx-us__chips">
                  <span v-if="desktopShell" class="fx-us__chip">
                    {{ t('notifications.prefs.desktopHandled') }}
                  </span>
                  <template v-else>
                    <span v-if="permission === 'granted'" class="fx-us__chip fx-us__chip--ok">
                      {{ t('notifications.prefs.permGranted') }}
                    </span>
                    <span v-else-if="permission === 'denied'" class="fx-us__chip fx-us__chip--bad">
                      {{ t('notifications.prefs.permDenied') }}
                    </span>
                    <span v-else-if="permission === 'unsupported'" class="fx-us__chip">
                      {{ t('notifications.prefs.permUnsupported') }}
                    </span>
                    <span v-else class="fx-us__chip">{{ t('notifications.prefs.permDefault') }}</span>
                    <button
                      v-if="permission === 'default'"
                      type="button"
                      class="fx-us__btn fx-us__btn--sm"
                      data-testid="user-settings-browser-ask"
                      @click="askPermission"
                    >
                      {{ t('notifications.prefs.enableBrowser') }}
                    </button>
                    <span v-if="permission === 'denied'" class="fx-us__hint">
                      {{ t('notifications.prefs.permDeniedHint') }}
                    </span>
                  </template>
                </div>
              </div>
            </div>

            <div class="fx-us__field">
              <span class="fx-us__label">{{ t('userSettings.notifications.eventsTitle') }}</span>
              <span class="fx-us__hint">{{ t('userSettings.notifications.eventsHint') }}</span>
              <div class="fx-us__events">
                <div
                  v-for="row in offeredEvents"
                  :key="row.ev"
                  class="fx-us__switch-row"
                  :class="{ 'is-off': row.gate.disabled }"
                  :title="row.gate.title"
                >
                  <button
                    type="button"
                    role="switch"
                    class="fx-us__switch"
                    :class="{ 'is-on': !row.gate.disabled && !mutedEvents.includes(row.ev) }"
                    :aria-checked="!row.gate.disabled && !mutedEvents.includes(row.ev)"
                    :disabled="savingNotif || row.gate.disabled"
                    :data-testid="`user-settings-event-${row.ev}`"
                    @click="setEventOn(row.ev, mutedEvents.includes(row.ev))"
                  >
                    <span class="fx-us__switch-knob" />
                  </button>
                  <span class="fx-us__event-label">
                    {{ t(userEventKey(row.ev)) }}
                    <span
                      v-if="row.gate.disabled"
                      class="fx-us__event-off"
                      :data-testid="`user-settings-event-off-${row.ev}`"
                    >{{ row.gate.title }}</span>
                  </span>
                </div>
              </div>
            </div>
          </section>

          <!-- ── Security ───────────────────────────────────────── -->
          <!-- ⚠ Named, not `v-else`. This used to be the chain's fallback,
               which is why adding a pane after it would not compile at all
               ("v-else-if has no adjacent v-if"): nothing may follow a
               `v-else`. The last branch is now the one that genuinely has
               nothing left to test for. -->
          <section
            v-else-if="section === 'security'"
            class="fx-us__pane"
            data-testid="user-settings-security"
          >
            <p v-if="caps.demoReadOnly" class="fx-us__note">{{ t('userSettings.demoReadOnly') }}</p>

            <form v-else class="fx-us__pane" @submit.prevent="changePassword">
              <span class="fx-us__label">{{ t('userSettings.security.passwordTitle') }}</span>
              <label class="fx-us__field">
                <span class="fx-us__label">{{ t('common.currentPassword') }}</span>
                <input
                  v-model="currentPassword"
                  type="password"
                  class="fx-us__input"
                  autocomplete="current-password"
                  required
                />
              </label>
              <div class="fx-us__grid">
                <label class="fx-us__field">
                  <span class="fx-us__label">{{ t('common.newPassword') }}</span>
                  <input
                    v-model="newPassword"
                    type="password"
                    class="fx-us__input"
                    autocomplete="new-password"
                    required
                  />
                </label>
                <label class="fx-us__field">
                  <span class="fx-us__label">{{ t('common.confirm') }}</span>
                  <input
                    v-model="newPasswordConfirm"
                    type="password"
                    class="fx-us__input"
                    autocomplete="new-password"
                    required
                  />
                </label>
              </div>
              <div class="fx-us__actions">
                <button type="submit" class="fx-us__btn fx-us__btn--primary" :disabled="savingPassword">
                  {{ savingPassword ? t('common.saving') : t('common.save') }}
                </button>
              </div>
            </form>

            <div v-if="!caps.demoReadOnly" class="fx-us__field">
              <span class="fx-us__label">{{ t('profile.totp.title') }}</span>
              <div v-if="totpStage === 'idle'" class="fx-us__switch-row">
                <span class="fx-us__chip" :class="totpEnabled ? 'fx-us__chip--ok' : ''">
                  {{ totpEnabled ? t('profile.totp.enabled') : t('profile.totp.disabled') }}
                </span>
                <button
                  v-if="!totpEnabled"
                  type="button"
                  class="fx-us__btn"
                  :disabled="totpBusy"
                  data-testid="user-settings-totp-enable"
                  @click="startTotp"
                >
                  {{ t('profile.totp.enable') }}
                </button>
                <button
                  v-else
                  type="button"
                  class="fx-us__btn"
                  data-testid="user-settings-totp-disable"
                  @click="totpStage = 'disable'"
                >
                  {{ t('profile.totp.disable') }}
                </button>
              </div>

              <div v-else-if="totpStage === 'enroll'" class="fx-us__pane">
                <p class="fx-us__hint">{{ t('profile.totp.scanHint') }}</p>
                <div v-if="totpQr" class="fx-us__qr" data-testid="user-settings-totp-qr" v-html="totpQr" />
                <code v-if="totpSecret" class="fx-us__code">{{ totpSecret }}</code>
                <div v-if="totpRecoveryCodes.length" class="fx-us__recovery">
                  <p class="fx-us__label">{{ t('profile.totp.recoveryTitle') }}</p>
                  <p class="fx-us__hint">{{ t('profile.totp.recoveryHint') }}</p>
                  <ul class="fx-us__recovery-list">
                    <li v-for="c in totpRecoveryCodes" :key="c">{{ c }}</li>
                  </ul>
                </div>
                <label class="fx-us__field">
                  <span class="fx-us__label">{{ t('profile.totp.code') }}</span>
                  <input
                    v-model="totpCode"
                    class="fx-us__input"
                    inputmode="numeric"
                    autocomplete="one-time-code"
                  />
                </label>
                <div class="fx-us__actions">
                  <button type="button" class="fx-us__btn fx-us__btn--quiet" @click="resetTotp">
                    {{ t('common.cancel') }}
                  </button>
                  <button
                    type="button"
                    class="fx-us__btn fx-us__btn--primary"
                    :disabled="totpBusy"
                    @click="verifyTotp"
                  >
                    {{ t('common.confirm') }}
                  </button>
                </div>
              </div>

              <div v-else class="fx-us__pane">
                <label class="fx-us__field">
                  <span class="fx-us__label">{{ t('profile.totp.code') }}</span>
                  <input
                    v-model="totpCode"
                    class="fx-us__input"
                    inputmode="numeric"
                    autocomplete="one-time-code"
                  />
                </label>
                <div class="fx-us__actions">
                  <button type="button" class="fx-us__btn fx-us__btn--quiet" @click="resetTotp">
                    {{ t('common.cancel') }}
                  </button>
                  <button
                    type="button"
                    class="fx-us__btn fx-us__btn--danger"
                    :disabled="totpBusy"
                    @click="disableTotp"
                  >
                    {{ t('common.confirm') }}
                  </button>
                </div>
              </div>
            </div>

            <!-- ⚠ No "Active sessions" block. There is no endpoint that lists
                 or revokes a session; drawing "2 sessions" would be a number
                 with nothing behind it. -->
          </section>
          <!-- ── AI assistant ───────────────────────── -->
          <!-- Deliberately empty. One sentence saying there is nothing to set
               and why; no switch, no field, no "enable" nobody reads. -->
          <section v-else class="fx-us__pane" data-testid="user-settings-ai">
            <div class="fx-us__soon">
              <Sparkles class="fx-us__soon-icon" aria-hidden="true" />
              <p class="fx-us__soon-title">{{ t('userSettings.ai.soon') }}</p>
              <p class="fx-us__soon-body">{{ t('userSettings.ai.body') }}</p>
            </div>
          </section>
        </div>

        <footer class="fx-us__foot">
          <!-- ⚠ Only Profile carries a Save, so only Profile carries a
               Cancel: on that tab, closing really does drop what you typed.
               Preferences and Notifications apply on the click that made
               them — a staged "Save changes" over controls that have visibly
               already taken effect is a lie about what the button does — so
               those tabs get the sentence that says so, and one way out. -->
          <span v-if="!profileSaveVisible" class="fx-us__foot-note">
            {{ t(`userSettings.footNote.${section}`) }}
          </span>
          <button type="button" class="fx-us__btn fx-us__btn--quiet" @click="close">
            {{ profileSaveVisible ? t('common.cancel') : t('common.close') }}
          </button>
          <button
            v-if="profileSaveVisible"
            type="button"
            class="fx-us__btn fx-us__btn--primary"
            :disabled="savingProfile || profileInvalid"
            data-testid="user-settings-save-profile"
            @click="saveProfile"
          >
            {{ savingProfile ? t('common.saving') : t('userSettings.saveChanges') }}
          </button>
        </footer>
      </div>
    </div>
  </dialog>
</template>

<style scoped>
/* Only declared --fe-* tokens (packages/core/src/styles/variables.css): a raw
   value here cannot be reached by a theme, and the heights and the type scale
   are the shell's measured ones — 28/34/40px controls, 12/12.5/13px text. */

.fx-us-dialog {
  margin: auto;
  padding: 0;
  border: 0;
  background: transparent;
  max-width: 100vw;
  max-height: 100vh;
}
.fx-us-dialog::backdrop {
  background: rgba(17, 24, 39, 0.55);
}

/* `.fe` brings the palette, the font and the 13px base; the sizing is this
   dialog's own.

   A GRID, not a row of two columns: the head spans the whole width and the
   rail starts under it. The rail used to begin at the very top with a
   "SETTINGS" caption of its own while the title and the close button sat
   inside the right-hand pane — two dialogs standing side by side rather than
   one dialog with a rail. */
.fx-us {
  display: grid;
  /* 224, not 208: the assistant row carries a "coming soon" chip beside its
     label and at 208 the label truncated to "AI assist…" — a rail item whose
     own name is cut is worse than the extra 16px. */
  grid-template-columns: 224px minmax(0, 1fr);
  grid-template-rows: auto minmax(0, 1fr);
  width: min(800px, calc(100vw - 32px));
  height: min(620px, calc(100vh - 32px));
  min-height: 0;
  border-radius: var(--fe-radius-lg);
  box-shadow: var(--fe-shadow);
  overflow: hidden;
}

/* ── rail ─────────────────────────────────────────────────────────── */
.fx-us__rail {
  grid-column: 1;
  grid-row: 2;
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-xs);
  padding: var(--fe-gap) var(--fe-gap-sm);
  background: var(--fe-bg-elev);
  border-inline-end: 1px solid var(--fe-border);
  overflow-y: auto;
}
.fx-us__rail-item {
  display: flex;
  align-items: center;
  gap: var(--fe-gap-sm);
  height: var(--fe-h-md);
  padding: 0 var(--fe-gap);
  border: 0;
  border-radius: var(--fe-radius-md);
  background: transparent;
  color: var(--fe-text);
  font: inherit;
  font-size: var(--fe-text-md);
  font-weight: 500;
  text-align: start;
  cursor: pointer;
}
.fx-us__rail-item:hover {
  background: var(--fe-bg-hover);
}
.fx-us__rail-item.is-active {
  background: var(--fe-primary-soft);
  color: var(--fe-primary);
}
.fx-us__rail-icon {
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
}
.fx-us__rail-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* The rail says "coming soon" before you click it. A row that looked like the
   other four and then opened onto an apology is the thing this avoids. */
.fx-us__rail-soon {
  flex: 0 0 auto;
  margin-inline-start: auto;
  letter-spacing: 0.01em;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--fe-bg-hover);
  color: var(--fe-text-muted);
  font-size: 10px;
  font-weight: 600;
  line-height: 16px;
  white-space: nowrap;
}

/* ── pane ─────────────────────────────────────────────────────────── */
.fx-us__main {
  grid-column: 2;
  grid-row: 2;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.fx-us__head {
  grid-column: 1 / -1;
  grid-row: 1;
  display: flex;
  align-items: center;
  gap: var(--fe-gap);
  padding: var(--fe-gap-lg);
  border-bottom: 1px solid var(--fe-border);
  background: var(--fe-bg);
}
.fx-us__head-avatar {
  flex: 0 0 auto;
  width: 40px;
  height: 40px;
  border-radius: 999px;
  object-fit: cover;
}
.fx-us__head-avatar--initial {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--fe-primary-soft);
  color: var(--fe-primary);
  font-size: var(--fe-text-md);
  font-weight: 600;
  text-transform: uppercase;
}
.fx-us__head-text {
  min-width: 0;
  flex: 1 1 auto;
}
/* The version sits between the heading and the close button and never pushes
   either: it keeps its own width, and the heading is what gives way. */
.fx-us__version {
  flex: 0 0 auto;
}
.fx-us__title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.25;
  color: var(--fe-text);
}
/* The pane's own heading — which section you are in, under the dialog's. */
.fx-us__panehead {
  margin-bottom: var(--fe-gap-lg);
}
.fx-us__panetitle {
  margin: 0;
  font-size: var(--fe-text-md);
  font-weight: 600;
  color: var(--fe-text);
}
.fx-us__subtitle {
  margin: 2px 0 0;
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
.fx-us__icon-btn {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--fe-h-sm);
  height: var(--fe-h-sm);
  border: 0;
  border-radius: var(--fe-radius-md);
  background: transparent;
  color: var(--fe-text-muted);
  cursor: pointer;
}
.fx-us__icon-btn:hover {
  background: var(--fe-bg-hover);
}
.fx-us__body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: var(--fe-gap-lg);
}
.fx-us__pane {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-lg);
}
.fx-us__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--fe-gap);
}
/* On a phone the rail stops being a rail and becomes a scrolling row of
   chips under the head; the pane takes the rest. Measured at 390: five items
   do not fit across, so the row scrolls sideways rather than wrapping into
   three lines of buttons above a two-line pane. */
@media (max-width: 640px) {
  .fx-us {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto auto minmax(0, 1fr);
  }
  .fx-us__head {
    grid-column: 1;
    grid-row: 1;
    padding: var(--fe-gap);
  }
  .fx-us__rail {
    grid-column: 1;
    grid-row: 2;
    flex-direction: row;
    gap: var(--fe-gap-xs);
    padding: var(--fe-gap-sm);
    border-inline-end: 0;
    border-bottom: 1px solid var(--fe-border);
    overflow-x: auto;
    overflow-y: hidden;
  }
  .fx-us__rail-item {
    flex: 0 0 auto;
  }
  .fx-us__rail-soon {
    margin-inline-start: var(--fe-gap-xs);
  }
  .fx-us__main {
    grid-column: 1;
    grid-row: 3;
  }
  .fx-us__body {
    padding: var(--fe-gap);
  }
  .fx-us__grid {
    grid-template-columns: minmax(0, 1fr);
  }
  /* The identity row wraps instead of squeezing the e-mail to three letters:
     picture and name on one line, the button under them. */
  .fx-us__identity {
    flex-wrap: wrap;
  }
  .fx-us__identity-text {
    flex: 1 1 60%;
  }
  .fx-us__identity-change {
    margin-inline-start: 0;
  }
  .fx-us__foot {
    padding: var(--fe-gap-sm) var(--fe-gap);
  }
}

/* ── controls ─────────────────────────────────────────────────────── */

/* A titled cluster of related fields. Appearance uses it to say out loud that
   the mode strip and the palette grid are two answers to ONE question
   ("how should this look") rather than two unrelated switches that happen to
   sit together. */
.fx-us__group {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap);
  padding: var(--fe-gap);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg-elev);
  min-width: 0;
}
.fx-us__group-title {
  font-size: var(--fe-text-xs);
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--fe-text-muted);
}


.fx-us__field {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-xs);
  min-width: 0;
}
.fx-us__label {
  font-size: var(--fe-text-sm);
  font-weight: 500;
  color: var(--fe-text);
}
.fx-us__hint {
  font-size: var(--fe-text-xs);
  font-weight: 400;
  color: var(--fe-text-muted);
}
.fx-us__hint--bad {
  color: var(--fe-danger);
}
/* The default folder view's three rows sit under ONE heading, so their own
   labels step down a size rather than reading as three separate settings. */
.fx-us__label--sub {
  margin-top: var(--fe-gap-xs);
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
/* An inline action inside a hint ("Reset them", "Forget them all"): a link,
   not a button — the hint is the sentence and this is its verb. */
.fx-us__link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--fe-primary);
  font: inherit;
  text-decoration: underline;
  cursor: pointer;
}
.fx-us__required {
  color: var(--fe-danger);
}
.fx-us__input.is-invalid {
  border-color: var(--fe-danger);
}
.fx-us__note {
  margin: 0;
  padding: var(--fe-gap-sm) var(--fe-gap);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg-elev);
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
.fx-us__readout {
  margin: 0;
  font-size: var(--fe-text-sm);
  color: var(--fe-text-muted);
}

/* Desktop-app downloads. The ROWS are drawn in this pane's own idiom rather
   than borrowed from the reminder's stylesheet — a scoped style cannot cross
   components anyway, and the two live in different furniture. What is shared
   is the thing that would actually rot if it were copied: which file, what it
   does, how big it is (`useDesktopDownloads`). */
.fx-us__dl-list {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-xs);
  min-width: 0;
}
.fx-us__dl {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--fe-gap-sm);
  padding: var(--fe-gap-sm) var(--fe-gap);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg);
  text-decoration: none;
  min-width: 0;
}
.fx-us__dl:hover {
  border-color: var(--fe-primary);
}
.fx-us__dl-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.fx-us__dl-arrow {
  flex: 0 0 auto;
  color: var(--fe-primary);
}
.fx-us__dl-all {
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
  text-decoration: underline;
}
.fx-us__dl-all:hover {
  color: var(--fe-text);
}
.fx-us__input {
  height: var(--fe-h-md);
  padding: 0 10px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg);
  color: var(--fe-text);
  font: inherit;
  font-size: var(--fe-text-sm);
  width: 100%;
}


.fx-us__input:focus {
  outline: none;
  border-color: var(--fe-primary);
}
/* The time-zone combobox is core's TimeZonePicker and carries its own
   stylesheet (.fe-tzpick*), shared with the embed's dialog. */

.fx-us__input:disabled {
  background: var(--fe-bg-elev);
  color: var(--fe-text-muted);
}
select.fx-us__input {
  padding-inline-end: 6px;
}
.fx-us__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--fe-gap-xs);
  height: var(--fe-h-md);
  padding: 0 var(--fe-gap);
  border: 1px solid var(--fe-border-strong);
  border-radius: var(--fe-radius);
  background: var(--fe-bg);
  color: var(--fe-text);
  font: inherit;
  font-size: var(--fe-text-sm);
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
}
.fx-us__btn:hover:not(:disabled) {
  background: var(--fe-bg-hover);
}
.fx-us__btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.fx-us__btn--sm {
  height: var(--fe-h-sm);
  padding: 0 var(--fe-gap-sm);
  font-size: var(--fe-text-xs);
}
.fx-us__btn--quiet {
  border-color: transparent;
  background: transparent;
  color: var(--fe-text-muted);
}
.fx-us__btn--primary {
  border-color: var(--fe-primary);
  background: var(--fe-primary);
  color: var(--fe-text-on-primary);
}
.fx-us__btn--primary:hover:not(:disabled) {
  background: var(--fe-primary-hover);
}
.fx-us__btn--danger {
  border-color: var(--fe-danger);
  background: var(--fe-danger);
  color: var(--fe-text-on-primary);
}
.fx-us__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--fe-gap-sm);
}
.fx-us__file {
  display: none;
}

/* ── segmented (theme) ────────────────────────────────────────────── */
.fx-us__segmented {
  display: inline-flex;
  align-self: flex-start;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  overflow: hidden;
}
.fx-us__seg {
  height: var(--fe-h-md);
  padding: 0 var(--fe-gap);
  border: 0;
  border-inline-end: 1px solid var(--fe-border);
  background: var(--fe-bg);
  color: var(--fe-text-muted);
  font: inherit;
  font-size: var(--fe-text-sm);
  font-weight: 500;
  cursor: pointer;
}
.fx-us__seg:last-child {
  border-inline-end: 0;
}
.fx-us__seg:hover {
  background: var(--fe-bg-hover);
}
.fx-us__seg.is-active {
  background: var(--fe-primary-soft);
  color: var(--fe-primary);
}

/* ── switch ───────────────────────────────────────────────────────── */
.fx-us__switch-row {
  display: flex;
  align-items: flex-start;
  gap: var(--fe-gap);
}
.fx-us__switch {
  flex: 0 0 auto;
  position: relative;
  width: 34px;
  height: 20px;
  margin-top: 1px;
  padding: 0;
  border: 0;
  border-radius: 999px;
  background: var(--fe-border-strong);
  cursor: pointer;
  transition: background 120ms ease;
}
.fx-us__switch.is-on {
  background: var(--fe-primary);
}
.fx-us__switch:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.fx-us__switch-knob {
  position: absolute;
  top: 2px;
  inset-inline-start: 2px;
  width: 16px;
  height: 16px;
  border-radius: 999px;
  background: var(--fe-bg);
  transition: transform 120ms ease;
}
.fx-us__switch.is-on .fx-us__switch-knob {
  /* ⚠ RTL: toward "on" = toward the inline end (--filex-dir-x, core base.css). */
  transform: translateX(calc(14px * var(--filex-dir-x, 1)));
}
.fx-us__switch-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.fx-us__event-label {
  font-size: var(--fe-text-sm);
  color: var(--fe-text);
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
/* An event whose service is off: greyed, with the reason under it (the
   administrator's view — everybody else is not offered it). */
.fx-us__switch-row.is-off .fx-us__event-label {
  color: var(--fe-text-muted);
}
.fx-us__event-off {
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
.fx-us__events {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-sm);
  margin-top: var(--fe-gap-xs);
}

/* ── chips ────────────────────────────────────────────────────────── */
.fx-us__chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--fe-gap-sm);
  margin-top: var(--fe-gap-xs);
}
.fx-us__chip {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 var(--fe-gap-sm);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-sm);
  background: var(--fe-bg-elev);
  color: var(--fe-text-muted);
  font-size: var(--fe-text-xs);
  white-space: nowrap;
}
.fx-us__chip--ok {
  border-color: var(--fe-keep-ok);
  color: var(--fe-keep-ok);
}
.fx-us__chip--bad {
  border-color: var(--fe-danger);
  color: var(--fe-danger);
}

/* ── identity row ─────────────────────────────────────────────────── */
/* Picture, who you are, whether you are an admin — and the one button that
   changes the picture, pushed to the far edge so the row reads left-to-right
   as a statement with an action at the end of it. */
.fx-us__identity {
  display: flex;
  align-items: center;
  gap: var(--fe-gap);
}
.fx-us__identity-text {
  flex: 1 1 auto;
  min-width: 0;
}
.fx-us__identity-name {
  display: flex;
  align-items: center;
  gap: var(--fe-gap-sm);
  margin: 0;
  font-size: var(--fe-text-md);
  font-weight: 600;
  color: var(--fe-text);
}
.fx-us__identity-who {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fx-us__identity-sub {
  margin: 3px 0 0;
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fx-us__badge {
  flex: 0 0 auto;
  padding: 0 8px;
  border-radius: 999px;
  background: var(--fe-primary-soft);
  color: var(--fe-primary);
  font-size: var(--fe-text-xs);
  font-weight: 600;
  line-height: 20px;
}
.fx-us__identity-change {
  margin-inline-start: auto;
}

/* ── the assistant pane, which has nothing in it on purpose ───────── */
.fx-us__soon {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--fe-gap-sm);
  padding: var(--fe-gap-lg);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-md);
  background: var(--fe-bg-elev);
}
.fx-us__soon-icon {
  width: 20px;
  height: 20px;
  color: var(--fe-text-muted);
}
.fx-us__soon-title {
  margin: 0;
  font-size: var(--fe-text-md);
  font-weight: 600;
  color: var(--fe-text);
}
.fx-us__soon-body {
  margin: 0;
  max-width: 52ch;
  font-size: var(--fe-text-sm);
  line-height: 1.55;
  color: var(--fe-text-muted);
}

/* ── avatar ───────────────────────────────────────────────────────── */
.fx-us__avatar-row {
  display: flex;
  align-items: center;
  gap: var(--fe-gap-sm);
}
.fx-us__avatar {
  width: 48px;
  height: 48px;
  border-radius: 999px;
  border: 1px solid var(--fe-border);
  object-fit: cover;
}
.fx-us__avatar--initial {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--fe-bg-elev);
  color: var(--fe-text-muted);
  font-size: 18px;
  font-weight: 600;
}

/* ── TOTP ─────────────────────────────────────────────────────────── */
.fx-us__qr {
  align-self: flex-start;
  padding: var(--fe-gap-sm);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: #ffffff;
}
.fx-us__qr :deep(svg) {
  width: 148px;
  height: 148px;
  display: block;
}
.fx-us__code {
  align-self: flex-start;
  padding: 6px var(--fe-gap-sm);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-sm);
  background: var(--fe-bg-elev);
  font-family: var(--fe-font-mono);
  font-size: var(--fe-text-xs);
  user-select: all;
}
.fx-us__recovery {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-xs);
  padding: var(--fe-gap);
  border: 1px solid var(--fe-border-strong);
  border-radius: var(--fe-radius);
  background: var(--fe-bg-elev);
}
.fx-us__recovery-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 2px var(--fe-gap);
  margin: 0;
  padding: 0;
  list-style: none;
  font-family: var(--fe-font-mono);
  font-size: var(--fe-text-xs);
  user-select: all;
}

/* ── footer ───────────────────────────────────────────────────────── */
.fx-us__foot {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--fe-gap-sm);
  padding: var(--fe-gap) var(--fe-gap-lg);
  border-top: 1px solid var(--fe-border);
  background: var(--fe-bg);
}
.fx-us__foot-note {
  flex: 1 1 auto;
  min-width: 0;
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
</style>
