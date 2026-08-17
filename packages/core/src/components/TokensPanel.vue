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
 */
import { computed, onMounted, ref } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import type { ApiToken } from '../types/Tokens';
import { useLocale } from '../composables/useLocale';
import { useTokens } from '../composables/useTokens';

const props = defineProps<{
  config: ExplorerConfig;
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

const locale = computed<LocaleCode>(() => props.config.locale ?? 'tr');
const { t } = useLocale(locale);

const { tokens, loading, error, canMint, revealed, load, create, remove, dismiss } = useTokens(
  props.config,
);

const label = ref('');
const busy = ref(false);
const copied = ref(false);
const confirming = ref<number | null>(null);

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

    <div v-if="canMint" class="fe-s3keys__form">
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
