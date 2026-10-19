<script setup lang="ts">
/**
 * LanguageDialog — #275, an embed's own language setting.
 *
 * The owner, 2026-10-19: an embed follows the browser's language, and the
 * person can pick another one inside the embed, kept in the browser. An embed
 * has no user-settings dialog — its "⋯" menu is where its settings live, the
 * time zone's among them (TimeZoneDialog) — so the menu opens this, and a pick
 * is stored in THIS browser (lib/explorerLocale). In a personal embed (a person
 * behind the credential) the explorer also writes it to the account; that is
 * the explorer's `pickEmbedLocale`, this dialog only says so.
 *
 * ⚠ The first row is "use the default", and it names what the default IS on
 * this page — the account's language, the browser's, the site's or English —
 * as the explorer resolves it with no pick (`fallback`). A reset whose outcome
 * you have to guess is not a reset (the time zone dialog's rule).
 *
 * ⚠ Every language by its own name (`availableLocales`, lib/uiLocales): somebody
 * looking for their language is looking for their own word for it. A language
 * pack's language is offered the moment the server's list arrives.
 *
 * ⚠ ChoiceSelect, not a native select (filex draws none) and not a segmented
 * strip: how many languages there are is the server's to say.
 */
import { computed } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { availableLocales, hasLocale, localeLabel, normalizeLocaleCode } from '../lib/uiLocales';
import type { ResolvedExplorerLocale } from '../lib/explorerLocale';
import Modal from '../modals/Modal.vue';
import ChoiceSelect, { type SelectOption } from './ChoiceSelect.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** The pick made in this browser; `''` = none. */
  chosen: string;
  /** What the explorer speaks with no pick, and which tier said it. */
  fallback: ResolvedExplorerLocale;
  /** A person is behind the credential: a pick also goes to their account. */
  personal?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** A language code, or `''` to go back to the default. */
  (e: 'pick', code: string): void;
}>();

const { t } = useLocale(() => props.locale);

const inputId = `fe-langdialog-${Math.random().toString(36).slice(2, 9)}`;

const DEFAULT_ROW = {
  chosen: 'lang.default.default',
  account: 'lang.default.account',
  browser: 'lang.default.browser',
  host: 'lang.default.host',
  default: 'lang.default.default',
} as const;

const options = computed<SelectOption[]>(() => [
  {
    value: '',
    label: t(DEFAULT_ROW[props.fallback.tier], { lang: nameOf(props.fallback.locale) }),
  },
  ...availableLocales().map((o) => ({ value: o.code, label: o.label || localeLabel(o.code) })),
]);

/** A language's name as the list prints it (a pack's own label when it gave one). */
function nameOf(code: string): string {
  return availableLocales().find((o) => o.code === code)?.label || localeLabel(code);
}

/** The pick, when it is one of the offered rows; otherwise the default row. */
const value = computed(() => (props.chosen && hasLocale(props.chosen) ? normalizeLocaleCode(props.chosen) : ''));
</script>

<template>
  <Modal :open="open" :title="t('lang.title')" size="md" :theme="theme" @close="emit('close')">
    <div class="fe-langdialog" data-testid="fe-lang-dialog" :data-tier="fallback.tier">
      <label class="fe-langdialog__label" :for="inputId">{{ t('lang.label') }}</label>
      <ChoiceSelect
        :id="inputId"
        :model-value="value"
        :options="options"
        testid="fe-lang"
        @update:model-value="(v: string | number) => emit('pick', String(v))"
      />
      <p class="fe-langdialog__hint" data-testid="fe-lang-hint">
        {{ personal ? t('lang.hint_account') : t('lang.hint') }}
      </p>
    </div>
  </Modal>
</template>

<!-- Not scoped — see TimeZoneDialog.vue. -->
<style>
/* Tall enough that the language list opens downward inside the dialog
   instead of being clipped by the body's own scroll box. */
.fe-langdialog {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-sm);
  min-height: 280px;
}
.fe-langdialog__label {
  font-size: var(--fe-text-sm);
  font-weight: 500;
  color: var(--fe-text);
}
.fe-langdialog__hint {
  margin: 0;
  font-size: var(--fe-text-xs);
  line-height: 1.45;
  color: var(--fe-text-muted);
}
</style>
