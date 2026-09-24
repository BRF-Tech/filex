<script setup lang="ts">
/**
 * TokensPanel — mint and revoke the API tokens this account signs in with on
 * the protocols whose credential IS a token.
 *
 * ⚠⚠ Why this had to exist. FTPS, WebDAV and `filex mount` all take an API
 * token as the password — the guides next to this panel say so in as many
 * words. Until 2026-08-17 the only place to mint one was the admin panel's
 * `/api/admin/ai-tokens` screen, so a normal user read "use an API token" and
 * had nowhere to get one. The route it calls (`/api/tokens`) has always been
 * open to every account and caps what it hands out to the caller's own role
 * and grants; only the UI was missing.
 *
 * One component, every surface (see S3KeysPanel for the rule at length): the
 * admin panel, the web explorer and the desktop app render THIS.
 *
 * Two shapes, one implementation. `full` adds what a person managing their own
 * API keys needs — which scopes, confined to which folder, expiring when — and
 * without it the panel is the compact minter that sits inside a protocol guide,
 * byte for byte as before.
 *
 * ⚠ `full` exists because the rich version had been written a second time, in
 * `web/src/components/SelfTokensModal.vue`, against the same `/api/tokens`
 * route. That copy was reachable only from our own web app: an embedder
 * mounting the explorer got users with no way to mint the credential WebDAV,
 * FTPS and `filex mount` ask for. The copy is gone; this is the surface.
 */
import { computed, onMounted, ref } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import type { ApiToken } from '../types/Tokens';
import { useLocale } from '../composables/useLocale';
import { useTokens } from '../composables/useTokens';
import DataTable, { type DataColumn } from './DataTable.vue';
import type { ContextAction } from './ContextMenu.vue';
import { resolveLocale } from '../locales/resolve';

const props = defineProps<{
  config: ExplorerConfig;
  /**
   * Render the full self-service key manager (scopes, folder confinement,
   * expiry) instead of the one-field minter the guides embed.
   */
  full?: boolean;
  /**
   * Which protocol the surrounding guide is showing. It only changes the
   * default label — a token minted here works on all of them, and pretending
   * otherwise would have people mint one per protocol.
   */
  protocol?: string;
  /**
   * The host the surrounding guide tells people to connect to. Given by the
   * connection guides, which know the server's own public address; without
   * it the label falls back to where this panel was loaded from. ⚠ The
   * default label used to be built from apiBase / the page while the guide
   * right under it printed the server's address — one screen, two machines.
   */
  host?: string;
}>();

const emit = defineEmits<{
  (e: 'active', v: { hasToken: boolean }): void;
}>();

const locale = computed<LocaleCode>(() => resolveLocale(props.config.locale));
const { t, formatDate } = useLocale(locale);

const { tokens, loading, error, canMint, revealed, load, create, remove, dismiss } = useTokens(
  props.config,
);

const label = ref('');
const busy = ref(false);
const copied = ref(false);
const confirming = ref<number | null>(null);

/* ── full mode ──────────────────────────────────────────────────────────
 * ⚠ All four verbs are offered to everyone on purpose. The old copy of this
 * screen hid `write`/`delete` from viewer accounts by reading a store that
 * only the web app has — which is precisely the coupling that kept this
 * surface out of every embed. The server caps each scope against the caller's
 * own role and grants and answers in words worth showing ("scope 'write' is
 * not available here"), so asking is never granting and the refusal explains
 * itself. `admin` is not offered at all: the server rejects it outright. */
const FULL_SCOPES = ['read', 'write', 'delete', 'mcp'] as const;
const scopeState = ref<Record<string, boolean>>({
  read: true,
  write: false,
  delete: false,
  mcp: false,
});
const rootPath = ref('');
const expiresInDays = ref<number | null>(null);

/**
 * ⚠⚠ Nothing ticked is not a request (owner's decision, v0.43.0): no door
 * mints a token without an explicit list, and this form used to paper over
 * an empty one with `|| 'read'` — a silent default, the very thing the rule
 * removes. The button waits for a tick and says why.
 */
const noScope = computed(() => !FULL_SCOPES.some((s) => scopeState.value[s]));

function buildScopes(): string {
  const parts = FULL_SCOPES.filter((s) => scopeState.value[s]) as string[];
  const root = rootPath.value.trim();
  if (root) parts.push('root:' + root);
  return parts.join(',');
}

onMounted(async () => {
  await load();
  emit('active', { hasToken: tokens.value.length > 0 });
});

/**
 * The name a token gets when the field is left empty — one the user will
 * recognise in the list later.
 *
 * A protocol guide knows both halves: `WebDAV — files.example.com`, the host
 * being the PUBLIC address the guide resolved (connectionsOrigin). The API
 * keys page knows neither, and it used to make a host up from `apiBase`,
 * which is whatever the page happens to talk to: every key on a local
 * instance was called `127.0.0.1:5297`, and that address was printed as the
 * placeholder in the README screenshots. It names the key by the day it was
 * made instead.
 */
function defaultLabel(): string {
  const p = (props.protocol || '').toUpperCase();
  if (p) return props.host ? `${p} — ${props.host}` : p;
  return t('conn.tokens.defaultName', { date: new Date().toISOString().slice(0, 10) });
}

/** What the empty name field suggests: the saved default on a protocol guide,
 *  an example of a useful name on the API keys page. */
function labelPlaceholder(): string {
  return props.protocol ? defaultLabel() : t('conn.tokens.namePlaceholder');
}

async function mint(): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  copied.value = false;
  try {
    if (props.full) {
      if (noScope.value) return;
      const scopes = buildScopes();
      await create({
        label: label.value.trim() || defaultLabel(),
        scopes,
        expires_in_days: expiresInDays.value && expiresInDays.value > 0
          ? expiresInDays.value
          : undefined,
      });
      await load();
      emit('active', { hasToken: tokens.value.length > 0 });
      label.value = '';
      rootPath.value = '';
      return;
    }
    // ⚠ `read,write,delete` and nothing more. `share` is a web-surface verb
    // and `admin` is refused by the server anyway; a token for mounting a
    // drive should not be able to publish public links. The server caps this
    // again against the caller's own role, so asking is not granting.
    await create({ label: label.value.trim() || defaultLabel(), scopes: 'read,write,delete' });
    await load();
    emit('active', { hasToken: tokens.value.length > 0 });
    label.value = '';
  } finally {
    busy.value = false;
  }
}

/** The list's columns — the product's one table (DataTable). */
const columns = computed<DataColumn<ApiToken>[]>(() => [
  {
    id: 'label',
    label: t('conn.tokens.col.label'),
    sortable: true,
    width: 200,
    format: (row) => row.label || '—',
  },
  { id: 'scopes', label: t('conn.tokens.col.scopes'), sortable: true, width: 200 },
  {
    id: 'used',
    label: t('conn.tokens.col.used'),
    sortable: true,
    sortDir: 'desc',
    width: 140,
    format: usedLabel,
    sortValue: (row) => (row.last_used_at ? new Date(row.last_used_at).getTime() : null),
  },
]);

/** The row's one verb, behind its one `Actions` control. ⚠ It keeps the
 *  two-step confirmation it had as a loose button: the first pick arms it and
 *  the label becomes "Confirm", the second pick revokes the token — which
 *  also ends any session already open on it. */
function rowActions(row: ApiToken): ContextAction[] {
  return [
    {
      key: 'revoke',
      label: confirming.value === row.id ? t('conn.tokens.confirm') : t('conn.tokens.revoke'),
      icon: 'delete',
      danger: true,
      /* The whole control used to be `:disabled="busy"`; the table draws the
         control, so the verb carries it — RowActions greys a button with
         nothing usable behind it. */
      disabled: busy.value,
    },
  ];
}

function onRowAction(key: string, row: ApiToken) {
  if (key === 'revoke') void revoke(row);
}

async function revoke(row: ApiToken): Promise<void> {
  if (confirming.value !== row.id) {
    confirming.value = row.id;
    return;
  }
  confirming.value = null;
  busy.value = true;
  try {
    await remove(row.id);
    emit('active', { hasToken: tokens.value.length > 0 });
  } finally {
    busy.value = false;
  }
}

async function copySecret(): Promise<void> {
  if (!revealed.value?.token) return;
  try {
    await navigator.clipboard.writeText(revealed.value.token);
    copied.value = true;
    window.setTimeout(() => (copied.value = false), 1600);
  } catch {
    /* clipboard refused (insecure origin, no permission) — the value is on
       screen and selectable, which is the fallback that always works. */
  }
}

/* zaman:z1 — one date formatter for the package. This was a bare
 * `toLocaleDateString()`: the browser's locale and the browser's zone, so a
 * key minted at 23:30 in Istanbul was dated a day early for a viewer reading
 * UTC and came out in the wrong language besides. */
function fmtDate(v?: string | null): string {
  const ms = new Date(v ?? '').getTime();
  return Number.isNaN(ms) ? '' : formatDate(ms);
}

function usedLabel(row: ApiToken): string {
  return row.last_used_at ? fmtDate(row.last_used_at) : t('conn.tokens.neverUsed');
}
</script>

<template>
  <section class="fe-s3keys" data-testid="api-tokens">
    <header class="fe-s3keys__head">
      <h4 class="fe-s3keys__title">{{ t('conn.tokens.title') }}</h4>
      <p class="fe-s3keys__lead">{{ t('conn.tokens.lead') }}</p>
    </header>

    <p v-if="canMint === false" class="fe-s3keys__muted">{{ t('conn.tokens.cannotMint') }}</p>
    <p v-if="error" class="fe-s3keys__warn">{{ error }}</p>

    <div v-if="canMint && !full" class="fe-s3keys__form">
      <input
        v-model="label"
        class="fe-cfield__input"
        :placeholder="labelPlaceholder()"
        data-testid="token-label"
      />
      <button class="fe-s3keys__btn" :disabled="busy" data-testid="token-mint" @click="mint">
        {{ t('conn.tokens.mint') }}
      </button>
    </div>

    <!-- full — the self-service key manager. Same route, same composable, same
         list below; only the mint form is richer. -->
    <div v-else-if="canMint" class="fe-tokform" data-testid="token-form-full">
      <input
        v-model="label"
        class="fe-cfield__input"
        :placeholder="labelPlaceholder()"
        data-testid="token-label"
      />

      <fieldset class="fe-tokform__scopes">
        <legend class="fe-tokform__legend">{{ t('conn.tokens.scopes') }}</legend>
        <label v-for="s in FULL_SCOPES" :key="s" class="fe-tokform__scope">
          <input v-model="scopeState[s]" type="checkbox" :data-testid="`token-scope-${s}`" />
          <span>{{ s }}</span>
        </label>
        <p v-if="noScope" class="fe-s3keys__hint fe-tokform__required" role="alert" data-testid="token-scopes-required">
          {{ t('conn.tokens.scopesRequired') }}
        </p>
      </fieldset>

      <label class="fe-tokform__field">
        <span class="fe-tokform__label">{{ t('conn.tokens.root') }}</span>
        <input
          v-model="rootPath"
          class="fe-cfield__input"
          :placeholder="t('conn.tokens.rootPlaceholder')"
          data-testid="token-root"
        />
      </label>

      <div class="fe-tokform__row">
        <label class="fe-tokform__field fe-tokform__field--narrow">
          <span class="fe-tokform__label">{{ t('conn.tokens.expiry') }}</span>
          <input
            v-model.number="expiresInDays"
            type="number"
            min="0"
            class="fe-cfield__input"
            :placeholder="t('conn.tokens.expiryNever')"
            data-testid="token-expiry"
          />
        </label>
        <button class="fe-s3keys__btn" :disabled="busy || noScope" data-testid="token-mint" @click="mint">
          {{ t('conn.tokens.mint') }}
        </button>
      </div>

      <!-- Said before the refusal rather than after it: the server caps every
           scope against the account's own role and grants. -->
      <p class="fe-s3keys__hint">{{ t('conn.tokens.capNote') }}</p>
    </div>

    <!-- The secret, once. -->
    <div v-if="revealed" class="fe-s3keys__secret" data-testid="token-secret">
      <p class="fe-s3keys__once">{{ t('conn.tokens.once') }}</p>
      <div class="fe-s3keys__pair">
        <code>{{ revealed.token }}</code>
        <button class="fe-s3keys__copy" @click="copySecret">
          {{ copied ? t('conn.guide.copied') : t('conn.guide.copy') }}
        </button>
      </div>
      <button class="fe-s3keys__dismiss" @click="dismiss">
        {{ t('conn.tokens.dismiss') }}
      </button>
    </div>

    <!-- ⚠ THE table (DataTable — the explorer's own), not a table of its own.
         This panel is reachable from the admin panel's Connections menu and
         from the explorer, so its list resizes, sorts, hides and moves columns
         and remembers that on the account (`conn.tokens`), like every other
         table. The raised ground is the panel's own (`.fe-s3keys` sets
         `--tbl-bg` once). -->
    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <DataTable
      v-else
      table-id="conn.tokens"
      :columns="columns"
      :rows="tokens"
      row-key="id"
      :locale="locale"
      :empty="t('conn.tokens.empty')"
      :row-actions="rowActions"
      :row-actions-test-id="(row: ApiToken) => `api-token-actions-${row.id}`"
      @row-action="(key: string, row: ApiToken) => onRowAction(key, row)"
    >
      <template #cell-scopes="{ row }">
        <code class="tbl-mono">{{ row.scopes }}</code>
      </template>
    </DataTable>

    <!-- ⚠ Said next to the button rather than in a document nobody opens: a
         revoked token stops a session that is already open, not only the next
         login. That is a change in behaviour worth knowing before relying on
         it, and it is the honest answer to "how do I stop this machine". -->
    <p v-if="canMint" class="fe-s3keys__hint">{{ t('conn.tokens.revokeHint') }}</p>
  </section>
</template>
