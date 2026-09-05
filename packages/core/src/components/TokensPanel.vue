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
}>();

const emit = defineEmits<{
  (e: 'active', v: { hasToken: boolean }): void;
}>();

const locale = computed<LocaleCode>(() => resolveLocale(props.config.locale));
const { t } = useLocale(locale);

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

function defaultLabel(): string {
  const p = (props.protocol || '').toUpperCase();
  return p ? `${p} — ${hostLabel()}` : hostLabel();
}

/** A name the user will recognise in the list later. */
function hostLabel(): string {
  try {
    return new URL(props.config.apiBase || window.location.origin).host;
  } catch {
    return 'filex';
  }
}

async function mint(): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  copied.value = false;
  try {
    if (props.full) {
      // At least one verb, or the token can do nothing and the server's
      // refusal would be about the wrong thing.
      const scopes = buildScopes() || 'read';
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

function fmtDate(v?: string | null): string {
  if (!v) return '';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString();
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
        :placeholder="defaultLabel()"
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
        :placeholder="defaultLabel()"
        data-testid="token-label"
      />

      <fieldset class="fe-tokform__scopes">
        <legend class="fe-tokform__legend">{{ t('conn.tokens.scopes') }}</legend>
        <label v-for="s in FULL_SCOPES" :key="s" class="fe-tokform__scope">
          <input v-model="scopeState[s]" type="checkbox" :data-testid="`token-scope-${s}`" />
          <span>{{ s }}</span>
        </label>
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
        <button class="fe-s3keys__btn" :disabled="busy" data-testid="token-mint" @click="mint">
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

    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <table v-else-if="tokens.length" class="fe-s3keys__table">
      <thead>
        <tr>
          <th>{{ t('conn.tokens.col.label') }}</th>
          <th>{{ t('conn.tokens.col.scopes') }}</th>
          <th>{{ t('conn.tokens.col.used') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in tokens" :key="row.id">
          <td>{{ row.label || '—' }}</td>
          <td><code>{{ row.scopes }}</code></td>
          <td>{{ usedLabel(row) }}</td>
          <td class="fe-s3keys__actions">
            <button class="fe-s3keys__link is-danger" :disabled="busy" @click="revoke(row)">
              {{ confirming === row.id ? t('conn.tokens.confirm') : t('conn.tokens.revoke') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="canMint" class="fe-s3keys__muted">{{ t('conn.tokens.empty') }}</p>

    <!-- ⚠ Said next to the button rather than in a document nobody opens: a
         revoked token stops a session that is already open, not only the next
         login. That is a change in behaviour worth knowing before relying on
         it, and it is the honest answer to "how do I stop this machine". -->
    <p v-if="canMint" class="fe-s3keys__hint">{{ t('conn.tokens.revokeHint') }}</p>
  </section>
</template>
