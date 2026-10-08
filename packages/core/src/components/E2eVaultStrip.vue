<script setup lang="ts">
/**
 * E2eVaultStrip — what an open vault is doing, in one sentence (wiring:e2
 * vault; docs/E2E-VAULT-FORMAT.md → "The write lock", "The idle lock").
 *
 * The strip above the listing of an unlocked vault, in place of the
 * encrypted folder's strip. It answers the questions a vault raises and a
 * folder does not:
 *
 *   - who writes: you (with how long until it goes back to read-only), or
 *     somebody else (who, since when, and - for the folder's owner or an
 *     administrator - "Take over"), or nobody (read-only until you change
 *     something);
 *   - when the vault locks itself: fifteen minutes with nothing done in it
 *     after the write lock ended (or after it was opened), counted down;
 *   - why it went back to read-only, and what was not saved;
 *   - why it stays read-only (the newest state is damaged, the server offers
 *     an older one than this tab saw, a newer filex wrote it).
 *
 * The countdowns tick here, once a second, from deadlines the composable
 * keeps (composables/useE2eVault). Every action is the parent's.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { VaultStripState } from '../composables/useE2eVault';
import { localeTag, useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons';
import { VAULT_ENTRIES_WRITE_MAX } from '../lib/e2evault/consts';

// ⚠ withDefaults: an absent optional boolean prop is `false` in Vue, not
// undefined, so without the default the Settings button never showed (the
// explorer passes no `settings`).
const props = withDefaults(
  defineProps<{
    locale: LocaleCode;
    state: VaultStripState;
    /** Settings (password, escrow) are open to this account. */
    settings?: boolean;
  }>(),
  { settings: true },
);

const emit = defineEmits<{
  (e: 'settings'): void;
  (e: 'lock'): void;
  (e: 'take-over'): void;
  (e: 'continue'): void;
  (e: 'dismiss'): void;
}>();

const { t, formatDate, formatSize } = useLocale(() => props.locale);

const now = ref(Date.now());
let tick: ReturnType<typeof setInterval> | null = null;
onMounted(() => {
  tick = setInterval(() => (now.value = Date.now()), 1000);
});
onBeforeUnmount(() => {
  if (tick) clearInterval(tick);
});

/** A count in this surface's language. */
function count(n: number): string {
  try {
    return new Intl.NumberFormat(localeTag(props.locale)).format(n);
  } catch {
    return String(n);
  }
}

/** m:ss of what is left until `deadline` (0:00 once it passed). */
function left(deadline: number): string {
  const s = Math.max(0, Math.ceil((deadline - now.value) / 1000));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, '0')}`;
}

const holderName = computed(() => {
  const h = props.state.holder;
  if (!h) return '';
  return h.label ? `${h.name || t('e2e.vault.someone')} (${h.label})` : h.name || t('e2e.vault.someone');
});

/** The one sentence: what the vault is doing for this tab right now. */
const sentence = computed<string>(() => {
  const s = props.state;
  if (s.readOnly === 'unreadable') return t('e2e.vault.unreadable');
  if (s.readOnly === 'damaged') {
    return s.shownAt
      ? t('e2e.vault.ro_damaged', { when: formatDate(s.shownAt, { time: true }) })
      : t('e2e.vault.ro_damaged_short');
  }
  if (s.readOnly === 'rollback') return t('e2e.vault.ro_rollback');
  if (s.readOnly === 'newer') return t('e2e.vault.ro_newer');
  if (s.busy && s.progress && s.progress.total > 0) {
    return t('e2e.vault.saving_progress', { done: formatSize(s.progress.done), total: formatSize(s.progress.total) });
  }
  if (s.busy) return t('e2e.vault.saving');
  if (s.mode === 'write') return t('e2e.vault.writing', { left: left(s.idleDeadline) });
  // Who took over, as the server said it when the lock was lost.
  if (s.lost === 'taken' && s.lostTo) return t('e2e.vault.lost_taken_by', { name: s.lostTo });
  if (s.lost === 'broken' && s.lostTo) return t('e2e.vault.lost_broken_by', { name: s.lostTo });
  if (s.holder) {
    return t('e2e.vault.held_by', {
      name: holderName.value,
      since: s.holder.since ? formatDate(Date.parse(s.holder.since), { time: true }) : '',
    });
  }
  if (s.lost === 'idle') return t('e2e.vault.lost_idle', { n: s.lostMinutes || 1 });
  if (s.lost === 'taken' || s.lost === 'broken') return t('e2e.vault.lost_taken');
  if (s.lost === 'connection') return t('e2e.vault.lost_connection');
  if (s.lost === 'expired' || s.lost === 'released') return t('e2e.vault.lost_expired');
  return t('e2e.vault.read_only');
});

/** The second clock: when the vault locks itself. */
const lockLine = computed(() => {
  const s = props.state;
  if (!s.lockDeadline || s.mode === 'write' || s.busy) return '';
  return t('e2e.vault.lock_countdown', { left: left(s.lockDeadline) });
});

const tone = computed(() => {
  const s = props.state;
  if (s.readOnly) return 'warn';
  if (s.unsaved.length > 0 || s.error) return 'error';
  if (s.mode === 'write' || s.busy) return 'write';
  return 'read';
});
</script>

<template>
  <div
    class="fe-e2e-strip fe-vault-strip"
    :class="`fe-vault-strip--${tone}`"
    role="status"
    aria-live="polite"
    data-testid="e2e-vault-strip"
    :data-mode="state.mode"
    :data-readonly="state.readOnly || undefined"
  >
    <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
    <span class="fe-e2e-strip__icon" aria-hidden="true" v-html="actionIconSvg('lock')"></span>
    <span class="fe-e2e-strip__names" data-testid="e2e-names-status">{{ t('e2e.level.vault') }}</span>
    <span class="fe-vault-strip__text">
      <span class="fe-vault-strip__sentence" data-testid="e2e-vault-sentence">{{ sentence }}</span>
      <span v-if="lockLine" class="fe-vault-strip__lock" data-testid="e2e-vault-lock-countdown">{{ lockLine }}</span>
      <span v-if="state.unsaved.length" class="fe-vault-strip__unsaved" data-testid="e2e-vault-unsaved">
        {{ t('e2e.vault.unsaved', { names: state.unsaved.join(', ') }) }}
      </span>
      <span v-if="state.limit === 'warn'" class="fe-vault-strip__unsaved" data-testid="e2e-vault-limit">
        {{ t('e2e.vault.limit_warn', { held: count(state.entries), max: count(VAULT_ENTRIES_WRITE_MAX) }) }}
      </span>
    </span>
    <span class="fe-vault-strip__actions">
      <button
        v-if="state.holder && !state.readOnly"
        type="button"
        class="fe-btn fe-e2e-strip__btn"
        data-testid="e2e-vault-take-over"
        @click="emit('take-over')"
      >
        {{ t('e2e.vault.take_over') }}
      </button>
      <button
        v-if="state.readOnly === 'damaged'"
        type="button"
        class="fe-btn fe-e2e-strip__btn"
        data-testid="e2e-vault-continue"
        @click="emit('continue')"
      >
        {{ t('e2e.vault.continue') }}
      </button>
      <button
        v-if="state.lost || state.unsaved.length || state.error"
        type="button"
        class="fe-btn fe-e2e-strip__btn"
        data-testid="e2e-vault-dismiss"
        @click="emit('dismiss')"
      >
        {{ t('e2e.vault.dismiss') }}
      </button>
      <button
        v-if="settings !== false"
        type="button"
        class="fe-btn fe-e2e-strip__btn"
        data-testid="e2e-settings-open"
        :disabled="state.busy"
        @click="emit('settings')"
      >
        {{ t('e2e.settings.open') }}
      </button>
      <button type="button" class="fe-btn fe-e2e-strip__btn" data-testid="e2e-vault-lock" :disabled="state.busy" @click="emit('lock')">
        {{ t('e2e.strip.lock') }}
      </button>
    </span>
  </div>
</template>
