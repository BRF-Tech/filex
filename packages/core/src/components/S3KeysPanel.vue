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
import DataTable, { type DataColumn } from './DataTable.vue';
import type { ContextAction } from './ContextMenu.vue';
import { resolveLocale } from '../locales/resolve';

const props = defineProps<{
  config: ExplorerConfig;
  /** Storage names the caller may see, for the confinement picker. */
  storages: string[];
}>();

const emit = defineEmits<{
  /** The key a guide should render with, and its one-time secret. */
  (e: 'active', v: { accessKeyID: string; secret?: string; endpoint: string; pathStyle: boolean }): void;
}>();

const locale = computed<LocaleCode>(() => resolveLocale(props.config.locale));
const { t, formatDate } = useLocale(locale);

const {
  keys,
  connection,
  loading,
  error,
  errorHint,
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

/**
 * The row's verbs. They used to be two loose text buttons in the last cell;
 * they are now the entries behind the row's one `Actions` control
 * (RowActions → ContextMenu, the menu the explorer's ⋮ opens).
 *
 * ⚠ `revoke` keeps its two-step confirmation exactly as it was: the first
 * pick arms it and the label becomes "Confirm", the second pick revokes.
 * Nothing about a credential's destruction got easier in the move.
 */
function rowActions(k: S3AccessKey): ContextAction[] {
  /* The whole control used to be `:disabled="busy"`; the table draws the
     control now, so each verb carries it and RowActions greys a button with
     nothing usable behind it. */
  return [
    {
      key: 'toggle',
      label: k.disabled_at ? t('conn.s3keys.enable') : t('conn.s3keys.disable'),
      icon: k.disabled_at ? 'check' : 'lock',
      disabled: busy.value,
    },
    {
      key: 'revoke',
      label: confirmRevoke.value === k.id ? t('conn.s3keys.confirm') : t('conn.s3keys.revoke'),
      icon: 'delete',
      danger: true,
      disabled: busy.value,
    },
  ];
}

/** The list's columns — the product's one table (DataTable). */
const columns = computed<DataColumn<S3AccessKey>[]>(() => [
  {
    id: 'label',
    label: t('conn.s3keys.col.label'),
    sortable: true,
    width: 180,
    format: (k) => k.label || t('conn.s3keys.noLabel'),
  },
  {
    id: 'key',
    label: t('conn.s3keys.col.key'),
    sortable: true,
    width: 200,
    sortValue: (k) => k.access_key_id,
    title: (k) => k.access_key_id,
  },
  { id: 'scope', label: t('conn.s3keys.col.scope'), sortable: true, width: 160, format: scopeOf },
  {
    id: 'lastUsed',
    label: t('conn.s3keys.col.lastUsed'),
    sortable: true,
    sortDir: 'desc',
    width: 140,
    format: (k) => (k.last_used_at ? shortDate(k.last_used_at) : t('conn.s3keys.neverUsed')),
    sortValue: (k) => (k.last_used_at ? new Date(k.last_used_at).getTime() : null),
  },
]);

function onRowAction(key: string, k: S3AccessKey) {
  if (key === 'toggle') void toggle(k);
  else if (key === 'revoke') void revoke(k);
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

/* zaman:z1 — one date formatter for the package. This was a bare
 * `toLocaleDateString()`: the browser's locale and the browser's zone, so a
 * key minted at 23:30 in Istanbul was dated a day early for a viewer reading
 * UTC and came out in the wrong language besides. */
function shortDate(v?: string | null): string {
  const ms = new Date(v ?? '').getTime();
  return Number.isNaN(ms) ? '' : formatDate(ms);
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
    <!-- The fix, for an administrator the server chose to tell (admin_hint). -->
    <p v-if="error && errorHint" class="fe-s3keys__hint" data-testid="s3keys-admin-hint">{{ errorHint }}</p>

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
    <!-- ⚠ THE table (DataTable — the explorer's own), not a table of its own:
         reachable from the admin panel's Connections menu and from the
         explorer, so it resizes, sorts, hides and moves columns and remembers
         that on the account (`conn.s3keys`), like every other table. -->
    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <DataTable
      v-else
      table-id="conn.s3keys"
      :columns="columns"
      :rows="keys"
      row-key="id"
      :locale="locale"
      :empty="t('conn.s3keys.empty')"
      :row-class="(k: S3AccessKey) => (k.disabled_at ? 'is-muted' : undefined)"
      :row-actions="rowActions"
      :row-actions-test-id="(k: S3AccessKey) => `s3-key-actions-${k.id}`"
      @row-action="(key: string, k: S3AccessKey) => onRowAction(key, k)"
    >
      <template #cell-key="{ row }">
        <code class="tbl-mono">{{ row.access_key_id }}</code>
      </template>
    </DataTable>
  </section>
</template>

<style>
/* ⚠ Only tokens that packages/core/src/styles/variables.css declares. This
   block shipped with `--fe-surface`, `--fe-muted`, `--fe-accent`,
   `--fe-surface-2` and `--fe-mono` — none of them exist — so the box had no
   ground, the muted text was full-strength, and the mint button wore a
   hardcoded blue that ignored the dark palette while the button beside it
   used --fe-primary. web/tests/api/themeTokens.test.ts now refuses a token
   that variables.css does not declare. */
.fe-s3keys {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--fe-border);
  border-radius: 10px;
  background: var(--fe-bg-elev);
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
  color: var(--fe-text-muted);
}
.fe-s3keys__warn {
  margin: 0;
  font-size: 12px;
  color: var(--fe-danger);
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
  background: var(--fe-primary);
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
  border: 1px solid var(--fe-primary);
  background: var(--fe-bg-selected);
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
  font-family: var(--fe-font-mono);
}
.fe-s3keys__k {
  min-width: 96px;
  color: var(--fe-text-muted);
}
.fe-s3keys__copy,
.fe-s3keys__dismiss {
  background: none;
  border: none;
  padding: 0 4px;
  color: var(--fe-primary);
  font-size: 12px;
  cursor: pointer;
}
.fe-s3keys__dismiss {
  align-self: flex-start;
}
/* ⚠ The key table's own look USED to be written here — 12px type, 4-6px
   padding, `--fe-border` instead of `--fe-border-soft`, its own disabled-row
   opacity and its own right-aligned actions cell. All of it is deleted rather
   than tuned: the panel now draws `.tbl`, and the one place that says what an
   admin table looks like is packages/core/src/styles/base.css ("THE ADMIN
   TABLE"). Four panels shared this block, so four tables drifted together;
   sharing the real stylesheet is what stops the next drift. */
</style>
