<script setup lang="ts">
/**
 * ConnectionsPanel — how to connect your computer to this server.
 *
 * ⚠⚠ This component is the reason the feature exists ONCE. The desktop
 * app, the web app and any embed mount this same file; none of them owns a
 * hand-written copy of the credential panels or the instructions. A fix here
 * lands everywhere on the next release of the package, which is precisely
 * the standing rule ("never write surface-specific behaviour") applied to a
 * feature that was asked for on three surfaces at once.
 *
 * ONE question, answered for the protocol you pick: "connect my computer to
 * filex". Generated from the live deployment — the real host, the storage
 * name, the caller's own username — with the credential each protocol needs
 * minted right above the commands that use it, and a copy button.
 *
 * ⚠ There is NO storage form here, and there has not been one since v0.43.0.
 * The owner, testing the release: "nasıl bağlanılır kısmında ve adminde
 * bağlantılar sayfası … ikisinde de depolar gözüküyor bu depolar sekmesine
 * hiç ihtiyaç yok. kaldıralım." Managing storages is a console job and it
 * already had a console: Admin → Storages (create, edit, delete, plus sync
 * mode, RBAC, drift — everything this panel's half never had). Browsing the
 * storages you can see is the explorer's navigation panel. A second, poorer
 * copy of the first in the screen that answers the OTHER question was the
 * whole complaint; do not bring it back, extend Admin → Storages instead.
 */
import { computed, onMounted, ref, watch } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { useSystemDark } from '../composables/useSystemDark';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import { useConnections, connectionsOrigin } from '../composables/useConnections';
import {
  buildGuide,
  guideName,
  guideProtocols,
  hostOf,
  type ProtocolGuide,
} from '../lib/connectionGuides';
import ConnectionGuideView from './ConnectionGuideView.vue';
import S3KeysPanel from './S3KeysPanel.vue';
import SSHKeysPanel from './SSHKeysPanel.vue';
import NFSExportsPanel from './NFSExportsPanel.vue';
import TokensPanel from './TokensPanel.vue';
import { resolveLocale } from '../locales/resolve';

const props = defineProps<{
  config: ExplorerConfig;
  /** Draw a close control — the desktop app opens this as a full surface
   *  and needs a way out; a page-embedded copy has the page's own chrome. */
  closable?: boolean;
}>();

/* ⚠ No `changed`. It existed for one reason — a storage added or removed in
   this panel had to reach the host's own list — and this panel no longer
   adds or removes one. An event that can never fire is worse than none: every
   host wires a handler for it and then trusts a refresh that never comes. */
const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'error', err: { message: string }): void;
}>();

const locale = computed<LocaleCode>(() => resolveLocale(props.config.locale));
const { t } = useLocale(locale);

// Destructured on purpose: Vue only auto-unwraps refs that are top-level in
// the setup scope, so `conn.storages` inside a template would render a Ref
// object rather than its value.
const { visible, me, publicUrl, error, load } = useConnections(props.config);

// ── theme ────────────────────────────────────────────────────────────
// Resolved in JS rather than left to `prefers-color-scheme`, because the
// stylesheet's auto rule keys off the explorer's own `.fe` root and this
// panel is mounted on its own.
const osDark = useSystemDark();
const themeResolved = computed(() => {
  const mode = props.config.theme ?? 'auto';
  if (mode === 'light' || mode === 'dark') return mode;
  return osDark.value ? 'dark' : 'light';
});

// ── outward: the guides ──────────────────────────────────────────────
const protocols = guideProtocols();
const protocol = ref(protocols[0] ?? 'webdav');
const guideStorage = ref<string>('');

/** What the NFS panel published: where to mount, and the path just minted. */
const nfs = ref<{ host: string; port: number; enabled: boolean; path?: string; readOnly: boolean } | null>(
  null,
);

const origin = computed(() => connectionsOrigin(props.config, publicUrl.value));

/**
 * What the S3 key panel published: the caller's own key, the endpoint the
 * SERVER computed, and whether path-style is mandatory.
 *
 * ⚠ The endpoint is not derived here. With a dedicated host it is a different
 * host from the application, and a guide that assembled it from the page
 * origin would print a URL that reaches the web app — which is exactly how the
 * first real-client run failed.
 */
const s3 = ref<{ accessKeyID: string; secret?: string; endpoint: string; pathStyle: boolean } | null>(
  null,
);

/**
 * What the SSH key panel published: where the SFTP endpoint listens, the login
 * name the account actually uses, and whether a key is registered.
 *
 * ⚠ The port comes from the SERVER. SFTP is raw TCP on a port of its own, and a
 * page that printed the web port would send every client at a proxy that speaks
 * only HTTP.
 */
const sftp = ref<{
  host: string;
  port: number;
  login: string;
  enabled: boolean;
  hasKey: boolean;
  ftps?: { enabled: boolean; host: string; port: number; pasv_min: number; pasv_max: number; self_signed: boolean };
} | null>(null);

const guide = computed<ProtocolGuide | null>(() =>
  buildGuide(
    protocol.value,
    {
      origin: origin.value,
      user: me.value?.email ?? '',
      storages: visible.value,
      storage: guideStorage.value || undefined,
      s3Endpoint: s3.value?.endpoint,
      s3PathStyle: s3.value?.pathStyle,
      s3AccessKeyID: s3.value?.accessKeyID || undefined,
      s3Secret: s3.value?.secret,
      sftpHost: sftp.value?.host || undefined,
      sftpPort: sftp.value?.port,
      sftpEnabled: sftp.value?.enabled,
      sftpLogin: sftp.value?.login || undefined,
      sftpHasKey: sftp.value?.hasKey,
      ftpsHost: sftp.value?.ftps?.host || undefined,
      ftpsPort: sftp.value?.ftps?.port,
      ftpsEnabled: sftp.value?.ftps?.enabled,
      ftpsPasvMin: sftp.value?.ftps?.pasv_min,
      ftpsPasvMax: sftp.value?.ftps?.pasv_max,
      ftpsSelfSigned: sftp.value?.ftps?.self_signed,
      nfsHost: nfs.value?.host || undefined,
      nfsPort: nfs.value?.port,
      nfsEnabled: nfs.value?.enabled,
      nfsPath: nfs.value?.path,
      nfsReadOnly: nfs.value?.readOnly,
    },
    t,
  ),
);

watch(
  () => visible.value,
  (names) => {
    if (!guideStorage.value && names.length === 1) guideStorage.value = names[0];
  },
);

// ── lifecycle ────────────────────────────────────────────────────────
onMounted(() => {
  void load();
});

// The panel is often mounted once and re-pointed (the desktop app switches
// accounts without tearing the window down), so a changed server must
// re-fetch rather than keep showing the previous one's storages.
watch(
  () => [props.config.apiBase, props.config.endpoint],
  () => {
    void load();
  },
);
</script>

<template>
  <div
    class="fe-conn"
    :class="{
      'fe--theme-dark': themeResolved === 'dark',
      'fe--theme-light': themeResolved === 'light',
    }"
    data-testid="connections-panel"
  >
    <header class="fe-conn__head">
      <div>
        <h2 class="fe-conn__title">{{ t('conn.title') }}</h2>
        <p class="fe-conn__sub">{{ t('conn.subtitle', { host: hostOf(origin) }) }}</p>
      </div>
      <button
        v-if="closable"
        type="button"
        class="fe-conn__btn"
        data-testid="connections-close"
        :aria-label="t('conn.close')"
        @click="emit('close')"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
      </button>
    </header>

    <!-- ⚠ The server's own words when the storage list cannot be fetched. It
         used to be printed by the storage half, which is gone; the guides
         below are built from that same list, so swallowing the failure here
         would leave a page of instructions quietly missing its storages. -->
    <p v-if="error" class="fe-conn__error" data-testid="conn-error">{{ error }}</p>

    <section class="fe-conn__body">
      <div class="fe-conn__guidebar">
        <label v-if="protocols.length > 1" class="fe-conn__pick">
          <span class="fe-cfield__label">{{ t('conn.guide.protocol') }}</span>
          <select v-model="protocol" class="fe-cfield__input" data-testid="guide-protocol">
            <option v-for="p in protocols" :key="p" :value="p">{{ guideName(p) }}</option>
          </select>
        </label>
        <label class="fe-conn__pick">
          <span class="fe-cfield__label">{{ t('conn.guide.storage') }}</span>
          <select v-model="guideStorage" class="fe-cfield__input" data-testid="guide-storage">
            <option value="">{{ t('conn.guide.allStorages') }}</option>
            <option v-for="s in visible" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
      </div>

      <!-- The keys come first: the guide below is filled in from whichever
           key is active, so minting one rewrites every command on the page. -->
      <S3KeysPanel
        v-if="protocol === 's3'"
        :config="config"
        :storages="visible"
        @active="s3 = $event"
      />

      <!-- The same shape for SFTP: the credential first, because the commands
           below are only worth anything with a real login name in them. -->
      <!-- Mounted for FTPS too: it is the same call that reports where the FTP
           endpoint listens, and the login name is the same one. -->
      <SSHKeysPanel
        v-if="protocol === 'sftp' || protocol === 'ftps'"
        :config="config"
        :keys-visible="protocol === 'sftp'"
        @active="sftp = $event"
      />

      <NFSExportsPanel
        v-if="protocol === 'nfs'"
        :config="config"
        :storages="visible"
        @active="nfs = $event"
      />

      <!-- ⚠⚠ The credential for the other three. FTPS, WebDAV and `filex
           mount` all take an API TOKEN as the password — the guides below say
           so — and until this panel existed the only place to mint one was the
           admin panel, so a normal user read the instruction and had nowhere
           to follow it. Same component on all three surfaces. -->
      <TokensPanel
        v-if="protocol === 'ftps' || protocol === 'webdav' || protocol === 'mount'"
        :config="config"
        :protocol="protocol"
        :host="hostOf(origin)"
      />

      <ConnectionGuideView v-if="guide" :guide="guide" :locale="locale" />
    </section>
  </div>
</template>

<style>
.fe-conn {
  display: flex;
  flex-direction: column;
  gap: 14px;
  font-family: var(--fe-font);
  font-size: 14px;
  color: var(--fe-text);
  /* ⚠ No painted ground. This is a SECTION of whatever page mounts it, not a
     full-bleed app like the explorer, and every host paints its own page:
     the web admin's dark shell is zinc #09090b, the desktop's is #14181d,
     the light shells are #fafafa / #ffffff. `background: var(--fe-bg)`
     (#0f1419 / #ffffff) drew a slightly-different rectangle behind the
     panel that ended where the panel ended — measured on demo.filex.sh
     2026-08-18 in dark mode: a blue-black box on a zinc page, cards and
     inputs inside it on their own tint. The tokens still colour the cards,
     inputs and buttons; only the page ground belongs to the host. */
  background: transparent;
  min-width: 0;
}
/* An explicit light palette, so a panel asked for light stays light even
   inside a host that publishes dark tokens at :root (the admin shell sets
   `.dark` on <html>). Without this, "light" only means "not dark". */
.fe-conn.fe--theme-light {
  --fe-bg: #ffffff;
  --fe-bg-elev: #f7f8fa;
  --fe-bg-hover: #edf0f5;
  --fe-border: #e2e6ed;
  --fe-border-strong: #c7ced9;
  --fe-text: #1a1e27;
  --fe-text-muted: #5a6475;
  --fe-primary: #2f6fe0;
  --fe-danger: #dc2626;
}
.fe-conn__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
/* ⚠ The component renders into the HOST's document (shadowRoot: false, on
   purpose — Tailwind, OS dark mode and the host's fonts are meant to reach
   it). That also means the host's element selectors reach it: the desktop
   shell styles `h2 { text-transform: uppercase; letter-spacing: .05em }`
   for its own section headings, and the panel's title came out
   "STORAGE CONNECTIONS" there while the web app rendered
   "Storage connections". Same component, two typographies, decided by
   whichever page it landed on — which is the split this package exists to
   prevent. Headings state their own type. */
.fe-conn h2,
.fe-conn h3,
.fe-conn h4 {
  text-transform: none;
  letter-spacing: normal;
}
.fe-conn__title {
  margin: 0;
  font-size: 17px;
  font-weight: 650;
}
.fe-conn__sub {
  margin: 3px 0 0;
  color: var(--fe-text-muted);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.fe-conn__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}
.fe-conn__error {
  margin: 0;
  color: var(--fe-danger);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.fe-conn__btn {
  font: inherit;
  font-size: 13px;
  padding: 6px 12px;
  border-radius: var(--fe-radius);
  border: 1px solid var(--fe-border-strong);
  background: var(--fe-bg);
  color: var(--fe-text);
  cursor: pointer;
}
.fe-conn__btn:hover:not(:disabled) {
  border-color: var(--fe-primary);
}
.fe-conn__btn:disabled {
  opacity: 0.55;
  cursor: default;
}
.fe-conn__guidebar {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
.fe-conn__pick {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 180px;
}
</style>
