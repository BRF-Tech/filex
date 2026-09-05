<script setup lang="ts">
/**
 * NFSExportsPanel — mint, disable and revoke the NFS exports this account can
 * be mounted through.
 *
 * ⚠⚠ The path this hands back IS the credential, and that is unusual enough to
 * say twice. NFSv3 cannot authenticate a request without Kerberos, so filex
 * binds the identity to a high-entropy export path: whoever knows it mounts as
 * this account. It goes into an /etc/fstab line or a NAS admin page — places
 * nobody treats as a password store — so the panel says so where the path is
 * shown, not in a document somebody will not read.
 *
 * One component, every surface (see S3KeysPanel for the rule at length).
 */
import { computed, onMounted, ref } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import type { NFSExport } from '../types/NFSExports';
import { useLocale } from '../composables/useLocale';
import { useNFSExports } from '../composables/useNFSExports';
import { resolveLocale } from '../locales/resolve';

const props = defineProps<{
  config: ExplorerConfig;
  /** Storage names the caller may see, for the confinement picker. */
  storages: string[];
}>();

const emit = defineEmits<{
  (e: 'active', v: { host: string; port: number; enabled: boolean; path?: string; readOnly: boolean }): void;
}>();

const locale = computed<LocaleCode>(() => resolveLocale(props.config.locale));
const { t } = useLocale(locale);

const {
  exports,
  connection,
  loading,
  error,
  canMint,
  revealed,
  guideExport,
  load,
  create,
  setDisabled,
  remove,
  dismissPath,
} = useNFSExports(props.config);

const label = ref('');
const storage = ref('');
const prefix = ref('');
const readOnly = ref(true);
const allowCidrs = ref('');
const busy = ref(false);
const confirmRemove = ref<number | null>(null);
const copied = ref(false);
let copyTimer: ReturnType<typeof setTimeout> | null = null;

function publish() {
  const c = connection.value;
  if (!c) return;
  emit('active', {
    host: c.host,
    port: c.port,
    enabled: c.enabled,
    // Only the freshly minted path — the stored ones cannot be recovered, and
    // printing a placeholder in a mount line somebody copies would produce a
    // command that fails with no clue why.
    path: revealed.value?.path,
    readOnly: guideExport.value?.read_only ?? readOnly.value,
  });
}

onMounted(async () => {
  await load();
  publish();
});

async function mint() {
  busy.value = true;
  try {
    await create({
      label: label.value.trim() || t('conn.nfs.defaultLabel'),
      storage: storage.value.trim() || undefined,
      prefix: prefix.value.trim() || undefined,
      read_only: readOnly.value,
      allow_cidrs: allowCidrs.value.trim() || undefined,
    });
    label.value = '';
    publish();
  } finally {
    busy.value = false;
  }
}

async function toggle(e: NFSExport) {
  busy.value = true;
  try {
    await setDisabled(e.id, !e.disabled_at);
    publish();
  } finally {
    busy.value = false;
  }
}

async function drop(e: NFSExport) {
  if (confirmRemove.value !== e.id) {
    confirmRemove.value = e.id;
    return;
  }
  confirmRemove.value = null;
  busy.value = true;
  try {
    await remove(e.id);
    publish();
  } finally {
    busy.value = false;
  }
}

/** The mount line for the path just minted — the thing to copy. */
const mountLine = computed(() => {
  const c = connection.value;
  const p = revealed.value?.path;
  if (!c || !p) return '';
  const ro = revealed.value?.export?.read_only ? ',ro' : '';
  return `sudo mount -t nfs -o nfsvers=3,tcp,port=${c.port},mountport=${c.port},nolock${ro} ${c.host}:${p} /mnt/filex`;
});

async function copyLine() {
  const text = mountLine.value;
  if (!text) return;
  let ok = false;
  try {
    await navigator.clipboard.writeText(text);
    ok = true;
  } catch {
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      ok = document.execCommand('copy');
      document.body.removeChild(ta);
    } catch {
      ok = false;
    }
  }
  if (!ok) return;
  copied.value = true;
  if (copyTimer) clearTimeout(copyTimer);
  copyTimer = setTimeout(() => (copied.value = false), 1600);
}

function shortDate(v?: string | null): string {
  if (!v) return '';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString();
}

function scopeOf(e: NFSExport): string {
  if (!e.storage_name) return t('conn.nfs.scopeAll');
  return e.prefix ? `${e.storage_name}/${e.prefix}` : e.storage_name;
}
</script>

<template>
  <section class="fe-s3keys" data-testid="nfs-exports">
    <header class="fe-s3keys__head">
      <h4 class="fe-s3keys__title">{{ t('conn.nfs.title') }}</h4>
      <p class="fe-s3keys__lead">{{ t('conn.nfs.lead') }}</p>
    </header>

    <!-- ⚠⚠ The one thing somebody must read before they mint one. -->
    <p class="fe-s3keys__warn">{{ t('conn.nfs.pathIsSecret') }}</p>
    <p v-if="connection && !connection.enabled" class="fe-s3keys__warn">
      {{ t('conn.nfs.disabled') }}
    </p>
    <p v-if="canMint === false" class="fe-s3keys__muted">{{ t('conn.nfs.cannotMint') }}</p>
    <p v-if="error" class="fe-s3keys__warn">{{ error }}</p>

    <div v-if="canMint" class="fe-s3keys__form fe-nfs__form">
      <input
        v-model="label"
        class="fe-cfield__input"
        :placeholder="t('conn.nfs.label')"
        data-testid="nfs-label"
      />
      <select v-model="storage" class="fe-cfield__input" data-testid="nfs-storage">
        <option value="">{{ t('conn.nfs.everyStorage') }}</option>
        <option v-for="s in storages" :key="s" :value="s">{{ s }}</option>
      </select>
      <input
        v-model="prefix"
        class="fe-cfield__input"
        :placeholder="t('conn.nfs.prefix')"
        :disabled="!storage"
        data-testid="nfs-prefix"
      />
      <input
        v-model="allowCidrs"
        class="fe-cfield__input"
        :placeholder="t('conn.nfs.allowCidrs')"
        data-testid="nfs-cidrs"
      />
      <label class="fe-nfs__ro">
        <input type="checkbox" v-model="readOnly" data-testid="nfs-readonly" />
        {{ t('conn.nfs.readOnly') }}
      </label>
      <button class="fe-s3keys__btn" :disabled="busy" data-testid="nfs-mint" @click="mint">
        {{ t('conn.nfs.mint') }}
      </button>
    </div>
    <p v-if="canMint" class="fe-s3keys__hint">{{ t('conn.nfs.readOnlyHint') }}</p>

    <!-- The path, once. -->
    <div v-if="revealed" class="fe-s3keys__secret" data-testid="nfs-path">
      <p class="fe-s3keys__once">{{ t('conn.nfs.once') }}</p>
      <div class="fe-s3keys__pair">
        <code>{{ mountLine }}</code>
        <button class="fe-s3keys__copy" @click="copyLine">
          {{ copied ? t('conn.guide.copied') : t('conn.guide.copy') }}
        </button>
      </div>
      <button class="fe-s3keys__dismiss" @click="dismissPath">
        {{ t('conn.nfs.dismiss') }}
      </button>
    </div>

    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <table v-else-if="exports.length" class="fe-s3keys__table">
      <thead>
        <tr>
          <th>{{ t('conn.nfs.col.label') }}</th>
          <th>{{ t('conn.nfs.col.scope') }}</th>
          <th>{{ t('conn.nfs.col.mode') }}</th>
          <th>{{ t('conn.nfs.col.lastUsed') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="e in exports" :key="e.id" :class="{ 'is-off': !!e.disabled_at }">
          <td>{{ e.label || t('conn.nfs.noLabel') }}</td>
          <td>{{ scopeOf(e) }}</td>
          <td>{{ e.read_only ? t('conn.nfs.modeRead') : t('conn.nfs.modeWrite') }}</td>
          <td>{{ e.last_used_at ? shortDate(e.last_used_at) : t('conn.nfs.neverUsed') }}</td>
          <td class="fe-s3keys__actions">
            <button class="fe-s3keys__link" :disabled="busy" @click="toggle(e)">
              {{ e.disabled_at ? t('conn.nfs.enable') : t('conn.nfs.disable') }}
            </button>
            <button class="fe-s3keys__link is-danger" :disabled="busy" @click="drop(e)">
              {{ confirmRemove === e.id ? t('conn.nfs.confirm') : t('conn.nfs.revoke') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="canMint" class="fe-s3keys__muted">{{ t('conn.nfs.empty') }}</p>
  </section>
</template>

<style>
/* Shares the access-key panel's layout; only the form is wider, because an
   export has one more field than a key does. */
.fe-nfs__form {
  grid-template-columns: 1.2fr 1fr 1fr 1fr auto auto;
}
@media (max-width: 900px) {
  .fe-nfs__form {
    grid-template-columns: 1fr;
  }
}
.fe-nfs__ro {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  white-space: nowrap;
}
</style>
