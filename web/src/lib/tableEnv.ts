/**
 * The admin app's half of `@brftech/filex-core` → lib/tableEnv: the panel's
 * language and light/dark mode, handed to every table ONCE from App.vue.
 *
 * ⚠ Why the tables need to be told at all: every table in the panel is the
 * core `DataTable` (the explorer's own table — there is no other), and its
 * column menu and Actions menu are teleported to <body>, outside the `.dark`
 * class the admin chrome paints with. Before this, a wrapper
 * (`components/ui/RowActions.vue`) fed the two values to each Actions button
 * by hand, and the column menu — which the imitation table never had — would
 * have had no way to know at all.
 *
 * ⚠ The light/dark ref is MODULE-scoped with one observer for the whole page:
 * a page of forty rows must not mean forty MutationObservers on <html>.
 */
import { computed, ref } from 'vue';
import { provideTableEnv, type LocaleCode } from '@brftech/filex-core';
import { i18n } from '@/i18n';
import { effectiveTheme } from '@/lib/theme';

const theme = ref<'light' | 'dark'>(typeof document === 'undefined' ? 'light' : effectiveTheme());

if (typeof document !== 'undefined' && typeof MutationObserver !== 'undefined') {
  new MutationObserver(() => {
    theme.value = effectiveTheme();
  }).observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
}

/** Call once, in App.vue's setup. */
export function installTableEnv(): void {
  provideTableEnv({
    locale: computed(() => String(i18n.global.locale.value) as LocaleCode),
    theme,
  });
}
