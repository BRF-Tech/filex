<script setup lang="ts">
/**
 * ConnectionsPanel — storage connections, and how to connect to them.
 *
 * ⚠⚠ This component is the reason the feature exists ONCE. The desktop
 * app, the web app and any embed mount this same file; none of them owns a
 * hand-written copy of the form, the list or the instructions. A fix here
 * lands everywhere on the next release of the package, which is precisely
 * the standing rule ("never write surface-specific behaviour") applied to a
 * feature that was asked for on three surfaces at once.
 *
 * Two halves, because they are two different questions:
 *
 *   • INWARD  — "connect filex to a bucket / a share / a server". Rendered
 *     entirely from the backend's driver descriptors, so a driver added on
 *     the server needs no frontend release and no surface can drift.
 *   • OUTWARD — "connect my computer to filex". Generated from the live
 *     deployment: the real host, the storage name, the caller's own
 *     username, with a copy button.
 *
 * A user who may not manage storages is told so plainly and still gets the
 * outward half — which is the half they actually need. Rendering a form
 * whose every submit 403s would be the dishonest alternative.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import type { StorageRow } from '../types/Connections';
import { useLocale } from '../composables/useLocale';
import { useConnections, connectionsOrigin } from '../composables/useConnections';
import {
  buildGuide,
  guideName,
  guideProtocols,
  hostOf,
  type ProtocolGuide,
} from '../lib/connectionGuides';
import StorageFields from './StorageFields.vue';
import ConnectionGuideView from './ConnectionGuideView.vue';
import S3KeysPanel from './S3KeysPanel.vue';
import SSHKeysPanel from './SSHKeysPanel.vue';
import NFSExportsPanel from './NFSExportsPanel.vue';
import TokensPanel from './TokensPanel.vue';

const props = defineProps<{
  config: ExplorerConfig;
  /** Which half to open on. */
  initialTab?: 'storages' | 'connect';
  /** Draw a close control — the desktop app opens this as a full surface
   *  and needs a way out; a page-embedded copy has the page's own chrome. */
  closable?: boolean;
}>();

const emit = defineEmits<{
  (e: 'changed'): void;
  (e: 'close'): void;
  (e: 'error', err: { message: string }): void;
}>();

const locale = computed<LocaleCode>(() => props.config.locale ?? 'tr');
const { t } = useLocale(locale);

// Destructured on purpose: Vue only auto-unwraps refs that are top-level in
// the setup scope, so `conn.storages` inside a template would render a Ref
// object rather than its value.
const {
  drivers,
  storages,
  visible,
  me,
  loading,
  loaded,
  error,
  canManage,
  denial,
  load,
  createStorage,
  updateStorage,
  deleteStorage,
  testStorage,
  descriptor,
  fields,
  defaults,
  missingRequired,
} = useConnections(props.config);

// ── theme ────────────────────────────────────────────────────────────
// Resolved in JS rather than left to `prefers-color-scheme`, because the
// stylesheet's auto rule keys off the explorer's own `.fe` root and this
// panel is mounted on its own.
const mq =
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : undefined;
const osDark = ref(!!mq?.matches);
function onMq(e: MediaQueryListEvent) {
  osDark.value = e.matches;
}
const themeResolved = computed(() => {
  const mode = props.config.theme ?? 'auto';
  if (mode === 'light' || mode === 'dark') return mode;
  return osDark.value ? 'dark' : 'light';
});

// ── tabs ─────────────────────────────────────────────────────────────
const tab = ref<'storages' | 'connect'>(props.initialTab ?? 'storages');

// ── inward: the storage form ─────────────────────────────────────────
type FormMode = { kind: 'none' } | { kind: 'new' } | { kind: 'edit'; row: StorageRow };
const form = ref<FormMode>({ kind: 'none' });
const fName = ref('');
const fDriver = ref('');
const fReadOnly = ref(false);
const fEnabled = ref(true);
const fConfig = ref<Record<string, unknown>>({});
const fInvalid = ref<string[]>([]);
const saving = ref(false);
const testing = ref(false);
const testResult = ref<{ ok: boolean; error?: string; object_count?: number } | null>(null);

/** What the NFS panel published: where to mount, and the path just minted. */
const nfs = ref<{ host: string; port: number; enabled: boolean; path?: string; readOnly: boolean } | null>(
  null,
);
const formError = ref<string | null>(null);
const confirmDelete = ref<number | null>(null);

const driverOptions = computed(() =>
  drivers.value.map((d) => {
    const translated = t(d.i18n_key);
    return { value: d.driver, label: translated === d.i18n_key ? d.label : translated };
  }),
);

const formFields = computed(() => fields(fDriver.value));

function openNew() {
  const first = drivers.value[0]?.driver ?? '';
  fDriver.value = first;
  fName.value = '';
  fReadOnly.value = false;
  fEnabled.value = true;
  fConfig.value = defaults(first);
  fInvalid.value = [];
  testResult.value = null;
  formError.value = null;
  form.value = { kind: 'new' };
}

function openEdit(row: StorageRow) {
  fDriver.value = row.driver;
  fName.value = row.name;
  fReadOnly.value = !!row.read_only;
  fEnabled.value = row.enabled !== false;
  // A copy: editing must not mutate the list row under the user while
  // they type, and cancelling must actually cancel.
  fConfig.value = { ...(row.config ?? {}) };
  fInvalid.value = [];
  testResult.value = null;
  formError.value = null;
  form.value = { kind: 'edit', row };
}

function closeForm() {
  form.value = { kind: 'none' };
  testResult.value = null;
  formError.value = null;
}

function onDriverChange(next: string) {
  fDriver.value = next;
  // Descriptor defaults, wholesale. Carrying keys over from the previous
  // driver is how a config ends up with fields nothing reads.
  fConfig.value = defaults(next);
  fInvalid.value = [];
  testResult.value = null;
}

function validate(): boolean {
  const missing = missingRequired(fDriver.value, fConfig.value).map((f) => f.key);
  fInvalid.value = missing;
  if (!fName.value.trim()) formError.value = t('conn.form.nameRequired');
  else if (missing.length) formError.value = t('conn.form.fillRequired');
  else formError.value = null;
  return !formError.value;
}

async function runTest() {
  testing.value = true;
  testResult.value = null;
  try {
    testResult.value = await testStorage({
      driver: fDriver.value,
      config: fConfig.value,
    });
  } finally {
    testing.value = false;
  }
}

async function save() {
  if (!validate()) return;
  saving.value = true;
  formError.value = null;
  try {
    const body = {
      name: fName.value.trim(),
      driver: fDriver.value,
      config: fConfig.value,
      read_only: fReadOnly.value,
      enabled: fEnabled.value,
    };
    if (form.value.kind === 'edit') await updateStorage(form.value.row.id, body);
    else await createStorage(body);
    closeForm();
    emit('changed');
  } catch (e) {
    const msg = (e as { detail?: string; message?: string }) ?? {};
    // The backend's own words beat a generic status line: "400" says
    // nothing, `ROOT_PATH_FORBIDDEN` says exactly which field is wrong.
    let detail = '';
    try {
      detail = msg.detail ? (JSON.parse(msg.detail) as { error?: string }).error ?? '' : '';
    } catch {
      detail = msg.detail ?? '';
    }
    formError.value = detail || msg.message || String(e);
    emit('error', { message: formError.value });
  } finally {
    saving.value = false;
  }
}

async function remove(row: StorageRow) {
  if (confirmDelete.value !== row.id) {
    confirmDelete.value = row.id;
    return;
  }
  confirmDelete.value = null;
  try {
    await deleteStorage(row.id);
    emit('changed');
  } catch (e) {
    emit('error', { message: (e as Error).message });
  }
}

/** The one line under a storage's name: where it actually points. */
function summaryOf(row: StorageRow): string {
  const d = descriptor(row.driver);
  const cfg = row.config ?? {};
  const parts: string[] = [];
  const rootField = d?.fields.find((f) => f.root);
  const pick = (key: string, aliases: string[] = []): string => {
    for (const k of [key, ...aliases]) {
      const v = cfg[k];
      if (typeof v === 'string' && v.trim()) return v;
    }
    return '';
  };
  const host = pick('endpoint') || pick('url') || pick('host');
  if (host) parts.push(host);
  const bucket = pick('bucket');
  if (bucket) parts.push(bucket);
  if (rootField) {
    const r = pick(rootField.key, rootField.aliases ?? []);
    if (r) parts.push(r);
  }
  return parts.join(' · ');
}

// ── outward: the guides ──────────────────────────────────────────────
const protocols = guideProtocols();
const protocol = ref(protocols[0] ?? 'webdav');
const guideStorage = ref<string>('');

const origin = computed(() => connectionsOrigin(props.config));

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
  mq?.addEventListener?.('change', onMq);
  void load();
});
onBeforeUnmount(() => mq?.removeEventListener?.('change', onMq));

// The panel is often mounted once and re-pointed (the desktop app switches
// accounts without tearing the window down), so a changed server must
// re-fetch rather than keep showing the previous one's storages.
watch(
  () => [props.config.apiBase, props.config.endpoint],
  () => {
    closeForm();
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
        ✕
      </button>
    </header>

    <nav class="fe-conn__tabs" role="tablist">
      <button
        type="button"
        role="tab"
        class="fe-conn__tab"
        :class="{ 'is-active': tab === 'storages' }"
        :aria-selected="tab === 'storages'"
        data-testid="tab-storages"
        @click="tab = 'storages'"
      >
        {{ t('conn.tab.storages') }}
      </button>
      <button
        type="button"
        role="tab"
        class="fe-conn__tab"
        :class="{ 'is-active': tab === 'connect' }"
        :aria-selected="tab === 'connect'"
        data-testid="tab-connect"
        @click="tab = 'connect'"
      >
        {{ t('conn.tab.connect') }}
      </button>
    </nav>

    <!-- ══ inward ════════════════════════════════════════════════ -->
    <section v-if="tab === 'storages'" class="fe-conn__body" role="tabpanel">
      <p v-if="loading && !loaded" class="fe-conn__muted">
        {{ t('conn.loading') }}
      </p>

      <template v-else>
        <!-- The honest non-admin state. Not a disabled form: a form you
             cannot submit teaches the wrong thing about whose install this
             is. -->
        <div
          v-if="canManage === false"
          class="fe-conn__card fe-conn__card--notice"
          data-testid="no-admin"
        >
          <strong>{{ t('conn.denied.title') }}</strong>
          <p class="fe-conn__muted">
            {{
              denial === 'anonymous'
                ? t('conn.denied.anonymous')
                : denial === 'unreachable'
                  ? t('conn.denied.unreachable', { error: error ?? '' })
                  : t('conn.denied.none')
            }}
          </p>
          <p class="fe-conn__muted">{{ t('conn.denied.guideHint') }}</p>
          <button type="button" class="fe-conn__btn fe-conn__btn--primary" @click="tab = 'connect'">
            {{ t('conn.denied.guideCta') }}
          </button>
        </div>

        <!-- What they CAN see, even without admin: the storages they may
             browse. Otherwise this half of the panel is blank for them. -->
        <div v-if="canManage === false && visible.length" class="fe-conn__list">
          <div v-for="name in visible" :key="name" class="fe-conn__card">
            <div class="fe-conn__rowmain">
              <strong class="fe-conn__name">{{ name }}</strong>
              <span class="fe-conn__muted">{{ t('conn.visibleOnly') }}</span>
            </div>
          </div>
        </div>

        <template v-if="canManage">
          <p v-if="error" class="fe-conn__error">{{ error }}</p>

          <!-- ── the form ── -->
          <div v-if="form.kind !== 'none'" class="fe-conn__card fe-conn__form" data-testid="storage-form">
            <h3 class="fe-conn__formtitle">
              {{ form.kind === 'edit' ? t('conn.form.editTitle', { name: fName }) : t('conn.form.newTitle') }}
            </h3>

            <div class="fe-cfield__row">
              <label class="fe-cfield__label" for="fe-conn-name">
                {{ t('conn.form.name') }}<span class="fe-cfield__req">*</span>
              </label>
              <input
                id="fe-conn-name"
                v-model="fName"
                class="fe-cfield__input"
                data-testid="storage-name"
                :placeholder="t('conn.form.namePlaceholder')"
              />
              <p class="fe-cfield__help">{{ t('conn.form.nameHelp') }}</p>
            </div>

            <div class="fe-cfield__row">
              <label class="fe-cfield__label" for="fe-conn-driver">{{ t('conn.form.driver') }}</label>
              <select
                id="fe-conn-driver"
                class="fe-cfield__input"
                data-testid="storage-driver"
                :value="fDriver"
                :disabled="form.kind === 'edit'"
                @change="onDriverChange(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="o in driverOptions" :key="o.value" :value="o.value">
                  {{ o.label }}
                </option>
              </select>
              <p v-if="form.kind === 'edit'" class="fe-cfield__help">
                {{ t('conn.form.driverLocked') }}
              </p>
            </div>

            <StorageFields
              v-model="fConfig"
              :fields="formFields"
              :locale="locale"
              :invalid="fInvalid"
            />

            <label class="fe-cfield__check">
              <input v-model="fReadOnly" type="checkbox" data-testid="storage-readonly" />
              <span>{{ t('conn.form.readOnly') }}</span>
            </label>
            <label class="fe-cfield__check">
              <input v-model="fEnabled" type="checkbox" />
              <span>{{ t('conn.form.enabled') }}</span>
            </label>

            <p v-if="formError" class="fe-conn__error" data-testid="form-error">{{ formError }}</p>
            <p
              v-if="testResult"
              class="fe-conn__testresult"
              :class="testResult.ok ? 'is-ok' : 'is-bad'"
              data-testid="test-result"
            >
              {{
                testResult.ok
                  ? t('conn.form.testOk', { count: testResult.object_count ?? 0 })
                  : t('conn.form.testFail', { error: testResult.error ?? '' })
              }}
            </p>

            <div class="fe-conn__actions">
              <button
                type="button"
                class="fe-conn__btn"
                data-testid="storage-test"
                :disabled="testing"
                @click="runTest"
              >
                {{ testing ? t('conn.form.testing') : t('conn.form.test') }}
              </button>
              <button
                type="button"
                class="fe-conn__btn fe-conn__btn--primary"
                data-testid="storage-save"
                :disabled="saving"
                @click="save"
              >
                {{ saving ? t('conn.form.saving') : t('conn.form.save') }}
              </button>
              <button type="button" class="fe-conn__btn" @click="closeForm">
                {{ t('conn.form.cancel') }}
              </button>
            </div>
          </div>

          <!-- ── the list ── -->
          <div v-else>
            <div class="fe-conn__listhead">
              <span class="fe-conn__muted">
                {{ t('conn.list.count', { n: storages.length }) }}
              </span>
              <button
                type="button"
                class="fe-conn__btn fe-conn__btn--primary"
                data-testid="storage-add"
                @click="openNew"
              >
                + {{ t('conn.list.add') }}
              </button>
            </div>

            <p v-if="!storages.length" class="fe-conn__empty">
              {{ t('conn.list.empty') }}
            </p>

            <div class="fe-conn__list" data-testid="storage-list">
              <div v-for="row in storages" :key="row.id" class="fe-conn__card">
                <div class="fe-conn__rowmain">
                  <div class="fe-conn__rowtext">
                    <strong class="fe-conn__name">{{ row.name }}</strong>
                    <span class="fe-conn__badge">{{ row.driver }}</span>
                    <span v-if="row.read_only" class="fe-conn__badge fe-conn__badge--warn">
                      {{ t('conn.list.readOnly') }}
                    </span>
                    <span v-if="row.enabled === false" class="fe-conn__badge">
                      {{ t('conn.list.disabled') }}
                    </span>
                    <div v-if="summaryOf(row)" class="fe-conn__muted fe-conn__summary">
                      {{ summaryOf(row) }}
                    </div>
                  </div>
                  <div class="fe-conn__rowbtns">
                    <button
                      type="button"
                      class="fe-conn__btn"
                      :data-testid="`storage-edit-${row.name}`"
                      @click="openEdit(row)"
                    >
                      {{ t('conn.list.edit') }}
                    </button>
                    <button
                      type="button"
                      class="fe-conn__btn fe-conn__btn--danger"
                      @click="remove(row)"
                    >
                      {{ confirmDelete === row.id ? t('conn.list.confirm') : t('conn.list.remove') }}
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </template>
      </template>
    </section>

    <!-- ══ outward ═══════════════════════════════════════════════ -->
    <section v-else class="fe-conn__body" role="tabpanel">
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
.fe-conn__tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--fe-border);
}
.fe-conn__tab {
  font: inherit;
  font-size: 13.5px;
  border: 0;
  background: none;
  color: var(--fe-text-muted);
  padding: 7px 12px;
  cursor: pointer;
  border-bottom: 2px solid transparent;
}
.fe-conn__tab:hover {
  color: var(--fe-text);
}
.fe-conn__tab.is-active {
  color: var(--fe-primary);
  border-bottom-color: var(--fe-primary);
  font-weight: 600;
}
.fe-conn__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}
.fe-conn__card {
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg-elev);
  padding: 12px 14px;
}
.fe-conn__card--notice {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
}
.fe-conn__form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.fe-conn__formtitle {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}
.fe-conn__list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.fe-conn__listhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 4px;
}
.fe-conn__rowmain {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.fe-conn__rowtext {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.fe-conn__rowbtns {
  display: flex;
  gap: 6px;
  flex: 0 0 auto;
}
.fe-conn__name {
  font-size: 14px;
}
.fe-conn__summary {
  flex-basis: 100%;
  font-family: var(--fe-font-mono);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.fe-conn__badge {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  border: 1px solid var(--fe-border-strong);
  border-radius: 999px;
  padding: 1px 8px;
  color: var(--fe-text-muted);
}
.fe-conn__badge--warn {
  color: var(--fe-danger);
  border-color: var(--fe-danger);
}
.fe-conn__muted {
  color: var(--fe-text-muted);
  font-size: 12.5px;
  margin: 0;
  line-height: 1.5;
}
.fe-conn__empty {
  border: 1px dashed var(--fe-border-strong);
  border-radius: var(--fe-radius);
  padding: 16px;
  color: var(--fe-text-muted);
  font-size: 13px;
  margin: 0;
}
.fe-conn__error {
  margin: 0;
  color: var(--fe-danger);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.fe-conn__testresult {
  margin: 0;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.fe-conn__testresult.is-ok {
  color: #16a34a;
}
.fe-conn__testresult.is-bad {
  color: var(--fe-danger);
}
.fe-conn__actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
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
.fe-conn__btn--primary {
  background: var(--fe-primary);
  border-color: var(--fe-primary);
  color: #fff;
}
.fe-conn__btn--danger {
  color: var(--fe-danger);
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
