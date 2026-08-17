<script setup lang="ts">
/**
 * S3KeysPanel — mint, list and revoke the caller's own S3 access keys.
 *
 * It sits above the S3 guide because the guide is only worth anything with a
 * real key in it: the commands below fill in the access key id automatically,
 * and the secret right after minting, which is the difference between a
 * document and a working paste.
 *
 * ⚠⚠ One component, every surface. The desktop app and the web app mount this
 * same file; neither owns a copy of the form or the revoke button. A credential
 * surface is the worst possible place for two implementations — one of them
 * would eventually hand out keys the other cannot revoke.
 *
 * ⚠ The secret leaves the server exactly once. Nothing here re-fetches it,
 * nothing stores it, and it is dropped from memory when the user dismisses it
 * or revokes the key — so the panel cannot become a place where a credential
 * quietly persists.
 */
import { computed, onMounted, ref } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import type { S3AccessKey } from '../types/S3Keys';
import { useLocale } from '../composables/useLocale';
import { useS3Keys } from '../composables/useS3Keys';

const props = defineProps<{
  config: ExplorerConfig;
  /** Storage names the caller may see, for the confinement picker. */
  storages: string[];
}>();

const emit = defineEmits<{
  /** The key a guide should render with, and its one-time secret. */
  (e: 'active', v: { accessKeyID: string; secret?: string; endpoint: string; pathStyle: boolean }): void;
}>();

const locale = computed<LocaleCode>(() => props.config.locale ?? 'tr');
const { t } = useLocale(locale);

const {
  keys,
  connection,
  loading,
  error,
  canMint,
  revealed,
  guideKey,
  load,
  create,
  setDisabled,
  remove,
  dismissSecret,
} = useS3Keys(props.config);

const label = ref('');
const bucket = ref('');
const prefix = ref('');
const busy = ref(false);
const confirmRevoke = ref<number | null>(null);
const copied = ref<string | null>(null);
let copyTimer: ReturnType<typeof setTimeout> | null = null;

/** Publish whatever the guide should render with, whenever it changes. */
function publish() {
  const conn = connection.value;
  if (!conn) return;
  emit('active', {
    accessKeyID: guideKey.value?.access_key_id ?? '',
    // Only the freshly minted one. Re-emitting a stale secret would put a
    // dead credential into the commands.
    secret: revealed.value?.key?.id === guideKey.value?.id ? revealed.value?.secret : undefined,
    endpoint: conn.endpoint,
    pathStyle: conn.path_style,
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
      label: label.value.trim() || t('conn.s3keys.defaultLabel'),
      bucket: bucket.value.trim() || undefined,
      prefix: prefix.value.trim() || undefined,
    });
    label.value = '';
    publish();
  } finally {
    busy.value = false;
  }
}

async function toggle(k: S3AccessKey) {
  busy.value = true;
  try {
    await setDisabled(k.id, !k.disabled_at);
    publish();
  } finally {
    busy.value = false;
  }
}

async function revoke(k: S3AccessKey) {
  if (confirmRevoke.value !== k.id) {
    confirmRevoke.value = k.id;
    return;
  }
  confirmRevoke.value = null;
  busy.value = true;
  try {
    await remove(k.id);
    publish();
  } finally {
    busy.value = false;
  }
}

/**
 * Copy, with the same fallback the guide uses: `navigator.clipboard` needs a
 * secure context, and an embed on plain http is a real deployment.
 */
async function copy(text: string, id: string) {
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
  copied.value = id;
  if (copyTimer) clearTimeout(copyTimer);
  copyTimer = setTimeout(() => (copied.value = null), 1600);
}

function shortDate(v?: string | null): string {
  if (!v) return '';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString();
}

/** What a key is limited to, in one line. */
function scopeOf(k: S3AccessKey): string {
  if (!k.bucket) return t('conn.s3keys.scopeAll');
  return k.prefix ? `${k.bucket}/${k.prefix}` : k.bucket;
}
</script>

<template>
  <section class="fe-s3keys" data-testid="s3-keys">
    <header class="fe-s3keys__head">
      <h4 class="fe-s3keys__title">{{ t('conn.s3keys.title') }}</h4>
      <p class="fe-s3keys__lead">{{ t('conn.s3keys.lead') }}</p>
    </header>

    <!-- The operator turned the endpoint off. Saying so beats handing out a
         key that authenticates against a 404. -->
    <p v-if="connection && !connection.enabled" class="fe-s3keys__warn">
      {{ t('conn.s3keys.disabled') }}
    </p>

    <p v-if="canMint === false" class="fe-s3keys__muted">{{ t('conn.s3keys.cannotMint') }}</p>
    <p v-if="error" class="fe-s3keys__warn">{{ error }}</p>

    <!-- ── mint ─────────────────────────────────────────────────── -->
    <div v-if="canMint" class="fe-s3keys__form">
      <input
        v-model="label"
        class="fe-cfield__input"
        :placeholder="t('conn.s3keys.label')"
        data-testid="s3-key-label"
      />
      <select v-model="bucket" class="fe-cfield__input" data-testid="s3-key-bucket">
        <option value="">{{ t('conn.s3keys.everyBucket') }}</option>
        <option v-for="s in storages" :key="s" :value="s">{{ s }}</option>
      </select>
      <input
        v-model="prefix"
        class="fe-cfield__input"
        :placeholder="t('conn.s3keys.prefix')"
        :disabled="!bucket"
        data-testid="s3-key-prefix"
      />
      <button class="fe-s3keys__btn" :disabled="busy" data-testid="s3-key-mint" @click="mint">
        {{ t('conn.s3keys.mint') }}
      </button>
    </div>
    <p v-if="canMint" class="fe-s3keys__hint">{{ t('conn.s3keys.inheritNote') }}</p>

    <!-- ── the one-time secret ──────────────────────────────────── -->
    <div v-if="revealed" class="fe-s3keys__secret" data-testid="s3-key-secret">
      <p class="fe-s3keys__once">{{ t('conn.s3keys.once') }}</p>
      <div class="fe-s3keys__pair">
        <span class="fe-s3keys__k">{{ t('conn.s3keys.accessKeyID') }}</span>
        <code>{{ revealed.key.access_key_id }}</code>
        <button class="fe-s3keys__copy" @click="copy(revealed.key.access_key_id, 'akid')">
          {{ copied === 'akid' ? t('conn.guide.copied') : t('conn.guide.copy') }}
        </button>
      </div>
      <div class="fe-s3keys__pair">
        <span class="fe-s3keys__k">{{ t('conn.s3keys.secret') }}</span>
        <code>{{ revealed.secret }}</code>
        <button class="fe-s3keys__copy" @click="copy(revealed.secret, 'secret')">
          {{ copied === 'secret' ? t('conn.guide.copied') : t('conn.guide.copy') }}
        </button>
      </div>
      <button class="fe-s3keys__dismiss" @click="dismissSecret">
        {{ t('conn.s3keys.dismiss') }}
      </button>
    </div>

    <!-- ── the keys ─────────────────────────────────────────────── -->
    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <table v-else-if="keys.length" class="fe-s3keys__table">
      <thead>
        <tr>
          <th>{{ t('conn.s3keys.col.label') }}</th>
          <th>{{ t('conn.s3keys.col.key') }}</th>
          <th>{{ t('conn.s3keys.col.scope') }}</th>
          <th>{{ t('conn.s3keys.col.lastUsed') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="k in keys" :key="k.id" :class="{ 'is-off': !!k.disabled_at }">
          <td>{{ k.label || t('conn.s3keys.noLabel') }}</td>
          <td><code>{{ k.access_key_id }}</code></td>
          <td>{{ scopeOf(k) }}</td>
          <td>{{ k.last_used_at ? shortDate(k.last_used_at) : t('conn.s3keys.neverUsed') }}</td>
          <td class="fe-s3keys__actions">
            <button class="fe-s3keys__link" :disabled="busy" @click="toggle(k)">
              {{ k.disabled_at ? t('conn.s3keys.enable') : t('conn.s3keys.disable') }}
            </button>
            <button class="fe-s3keys__link is-danger" :disabled="busy" @click="revoke(k)">
              {{ confirmRevoke === k.id ? t('conn.s3keys.confirm') : t('conn.s3keys.revoke') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="canMint" class="fe-s3keys__muted">{{ t('conn.s3keys.empty') }}</p>
  </section>
</template>

<style>
.fe-s3keys {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--fe-border);
  border-radius: 10px;
  background: var(--fe-surface);
}
.fe-s3keys__title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}
.fe-s3keys__lead,
.fe-s3keys__hint,
.fe-s3keys__muted {
  margin: 0;
  font-size: 12px;
  color: var(--fe-muted);
}
.fe-s3keys__warn {
  margin: 0;
  font-size: 12px;
  color: var(--fe-danger, #c0392b);
}
.fe-s3keys__form {
  display: grid;
  grid-template-columns: 1.4fr 1fr 1fr auto;
  gap: 8px;
  align-items: center;
}
@media (max-width: 640px) {
  .fe-s3keys__form {
    grid-template-columns: 1fr;
  }
}
.fe-s3keys__btn {
  padding: 7px 12px;
  border-radius: 8px;
  border: 1px solid transparent;
  background: var(--fe-accent, #2f6df6);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
}
.fe-s3keys__btn:disabled {
  opacity: 0.55;
  cursor: default;
}
.fe-s3keys__secret {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  border-radius: 8px;
  border: 1px solid var(--fe-accent, #2f6df6);
  background: var(--fe-surface-2, rgba(47, 109, 246, 0.06));
}
.fe-s3keys__once {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
}
.fe-s3keys__pair {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  min-width: 0;
}
.fe-s3keys__pair code {
  flex: 1;
  overflow-wrap: anywhere;
  font-family: var(--fe-mono, ui-monospace, monospace);
}
.fe-s3keys__k {
  min-width: 96px;
  color: var(--fe-muted);
}
.fe-s3keys__copy,
.fe-s3keys__dismiss,
.fe-s3keys__link {
  background: none;
  border: none;
  padding: 0 4px;
  color: var(--fe-accent, #2f6df6);
  font-size: 12px;
  cursor: pointer;
}
.fe-s3keys__link.is-danger {
  color: var(--fe-danger, #c0392b);
}
.fe-s3keys__dismiss {
  align-self: flex-start;
}
.fe-s3keys__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.fe-s3keys__table th {
  text-align: left;
  font-weight: 500;
  color: var(--fe-muted);
  padding: 4px 6px;
}
.fe-s3keys__table td {
  padding: 5px 6px;
  border-top: 1px solid var(--fe-border);
  overflow-wrap: anywhere;
}
.fe-s3keys__table tr.is-off {
  opacity: 0.55;
}
.fe-s3keys__actions {
  white-space: nowrap;
  text-align: right;
}
</style>
