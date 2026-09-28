<script setup lang="ts">
/**
 * The admin app's user settings — core's UserSettingsDialog, bound to THIS app.
 *
 * ⚠⚠ Not a second dialog. The dialog is `@brftech/filex-core`'s since
 * 2026-09-27 (it moved there so the desktop app opens the very same one inside
 * its window). This file hands it what only this app has — its `host`
 * (core lib/userSettingsHost):
 *
 *   · who is signed in and what the server can do: the auth and capabilities
 *     stores;
 *   · the account endpoints through this app's axios client (its CSRF header,
 *     its 401 redirect), and its toast;
 *   · the rows only this app has: the language (i18n), the start page (the
 *     router's front door), one click or two, the desktop-app downloads and
 *     the browser's own notifications.
 *
 * Mounted from the admin panel's top nav, the explorer page's avatar and the
 * admin Notifications page, exactly as before.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  UserSettingsDialog,
  setThemeMode as setCoreThemeMode,
  type SettingsUser,
  type UserSettingsHost,
} from '@brftech/filex-core';

// ⚠ The `--fe-*` tokens the dialog is drawn with live in core's stylesheet,
// and the admin panel does not load it: only the views that embed the
// explorer do. Without this import the dialog opened from TopNav would resolve
// every token to nothing — a white box with unstyled text — while looking
// correct on Home. The sheet is namespaced (`.fe`, `.fx-*`, `filex-explorer`)
// apart from the `:root` custom properties, so pulling it in cannot restyle
// the panel around it.
import '@brftech/filex-core/style.css';

import { AuthApi } from '@/api/auth';
import { quotaApi } from '@/api/quota';
import { NotificationsApi } from '@/api/notifications';
import { extractError } from '@/api/client';
import type { User } from '@/api/types';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useNotificationsStore } from '@/stores/notifications';
import { useToastStore } from '@/stores/toast';
import { setStoredLocale } from '@/i18n';
import { getStoredTheme, setStoredTheme } from '@/lib/theme';
import { openTriggerPref, setOpenTriggerPref } from '@/lib/explorerConfig';
import { getStoredTimeZone, setStoredTimeZone } from '@/lib/timezone';
import { getStartPage, setStartPage, type StartPage } from '@/lib/startPage';
// ⚠⚠ THE SAME LIST THE REMINDER SHOWS, not a second copy of it. The corner
// reminder (`InstallPrompt.vue`) can be closed for good, so the downloads need
// a home that is always there; the platform detection and the per-platform
// entries live in the composable and are RENDERED twice.
import { useDesktopDownloads } from '@/composables/useInstallPrompt';
import {
  browserNotifyEnabled,
  browserNotifyPermission,
  isDesktopShell,
  requestBrowserNotifyPermission,
  setBrowserNotifyEnabled,
} from '@/lib/browserNotify';

const props = defineProps<{
  modelValue: boolean;
  /** The pane to open on (the admin Notifications page opens `notifications`). */
  initialSection?: 'profile' | 'preferences' | 'notifications' | 'security' | 'ai';
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>();

const { locale } = useI18n();
const auth = useAuthStore();
const caps = useCapabilitiesStore();
const notif = useNotificationsStore();
const toast = useToastStore();
const desktop = useDesktopDownloads();

const host: UserSettingsHost = {
  get locale() {
    return String(locale.value);
  },
  get user() {
    return auth.user;
  },
  get isAdmin() {
    return auth.isAdmin;
  },
  get demoReadOnly() {
    return caps.demoReadOnly;
  },
  get capabilities() {
    return caps.data;
  },
  api: {
    updateProfile: (patch) => AuthApi.updateProfile(patch as Partial<User>),
    changePassword: (current, next) => AuthApi.changePassword(current, next),
    enrollTotp: () => AuthApi.enrollTotp(),
    verifyTotp: (code) => AuthApi.verifyTotp(code),
    disableTotp: (code) => AuthApi.disableTotp(code),
    quota: () => quotaApi.me(),
    // Through the store, so the admin Notifications page reads the same
    // switches this dialog just wrote.
    notificationSettings: async () => {
      await notif.fetchSettings();
      return notif.settings;
    },
    updateNotificationSettings: async (prefs) => {
      await notif.updateSettings(prefs);
      return notif.settings;
    },
  },
  setUser(u: SettingsUser) {
    auth.user = u as User;
  },
  toast(kind, message) {
    if (kind === 'success') toast.success(message);
    else if (kind === 'warn') toast.warn(message);
    else toast.error(message);
  },
  errorText: (err, fallback) => extractError(err, fallback),
  zone: { get: getStoredTimeZone, set: setStoredTimeZone },
  mode: {
    get: getStoredTheme,
    set(v) {
      setStoredTheme(v);
      // ⚠ And core's own pin. The explorer ranks an explicit `filex.thememode`
      // ABOVE the mode its host passes in `config.theme` (lib/themes: 'host' is
      // only the default). Anyone who ever used the explorer's old mode strip
      // has that pin set — so without this line the switch would move the
      // admin chrome and leave the file listing on the old mode.
      setCoreThemeMode(v);
    },
  },
  language: {
    // i18n settle decides and applies — including a pack's language.
    pick: (code) => setStoredLocale(code),
  },
  startPage: {
    // "Admin panel" is not offered to a non-admin AT ALL: a disabled row would
    // advertise a door, an enabled one would hand out a choice the router has
    // to refuse on every launch.
    get options() {
      return auth.isAdmin ? ['home', 'files', 'admin'] : ['home', 'files'];
    },
    get: () => getStartPage(),
    set: (page) => setStartPage(page as StartPage),
  },
  openTrigger: { get: openTriggerPref, set: setOpenTriggerPref },
  desktopApp: {
    // Null on a phone and inside the Electron shell itself — the dialog then
    // draws no downloads group at all.
    get platform() {
      return desktop.platform;
    },
    get platformLabel() {
      return desktop.platformLabel.value;
    },
    get downloads() {
      return desktop.downloads.value;
    },
    releasesUrl: desktop.releasesUrl,
  },
  browserNotifications: {
    get desktopShell() {
      return isDesktopShell();
    },
    permission: () => browserNotifyPermission(),
    enabled: () => browserNotifyEnabled(auth.user?.id),
    setEnabled: (on) => setBrowserNotifyEnabled(on, auth.user?.id),
    ask: () => requestBrowserNotifyPermission(auth.user?.id),
  },
};

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
});
</script>

<template>
  <!-- ⚠ To <body>. The dialog is core's Modal — a fixed layer in the page, no
       longer the browser's top layer — and TopNav mounts it inside its
       sticky header, whose backdrop-filter makes that header the containing
       block of anything fixed in it: left in place, the dialog would be laid
       out inside a 56px bar. -->
  <Teleport to="body">
    <UserSettingsDialog v-model="open" :host="host" :initial-section="initialSection" />
  </Teleport>
</template>
