<script setup lang="ts">
/**
 * ONE control at the end of a row, and the row's actions behind it.
 *
 * The owner, 2026-09-20: "adminde aksiyonlar karma karışık; onlarda da aynı
 * explore'da olduğu gibi en dibe sabitli tek buton olmalı. Adminde 'Aksiyon'
 * diye yazmalı, explore'da ⋮ var, adminde aksiyon yazacak."
 *
 * What it replaces: a row that ended in two or three unlabelled icon buttons —
 * a pencil, a key, a bin — in a column that had to be 180-260px wide to hold
 * them, differently ordered in every table, and the first thing to fall off
 * the right-hand edge on a narrow window. Users had three unnamed buttons;
 * Webhooks had `test / pencil / bin`, Users `pencil / key / bin`, Shares
 * `link / ban / bin`, and no two agreed on what a ghost button meant.
 *
 * ⚠⚠ THE SAME MENU THE EXPLORER OPENS. This wraps `ContextMenu` — the one the
 * file list's ⋮ opens — rather than building a second popover, for the reason
 * filex lesson #67 records: a menu assembled beside the real one drifts from
 * it the first time an action is added. The only difference between the two
 * surfaces is the handle: the explorer's is the ⋮ glyph, the admin panel's is
 * a LABELLED button reading "Actions" / "Aksiyon", because an admin row's
 * verbs (revoke a token, reset a password, purge a version) are not guessable
 * from a glyph the way "open / rename / delete" are.
 *
 * ⚠ NOTHING MAY BE LOST IN THE MOVE. Every loose button a row used to draw
 * becomes one entry here, disabled entries included: a verb that is currently
 * impossible stays visible and greyed with its reason in `title`, because a
 * hidden verb reads as a missing feature (the same rule ContextMenu already
 * states for the explorer). Destructive verbs keep `danger: true`, which is
 * what paints them apart inside the menu, and keep whatever confirmation
 * dialog the page already put behind them — this control does not confirm
 * anything itself, it only says which verb was chosen.
 *
 * ⚠ `shortcutId: ''` on every entry. `ContextMenu` prints the keyboard hint
 * the shortcut registry holds for an action key, and that registry is the
 * EXPLORER's: a row offering "Delete" in the admin panel would otherwise
 * advertise the file manager's Del binding, which does nothing on that page.
 * An entry that genuinely has an admin shortcut can still name one.
 */
import { computed, ref } from 'vue';
import ContextMenu, { type ContextAction } from './ContextMenu.vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { inlineStartX } from '../lib/direction';

const props = withDefaults(
  defineProps<{
    /** The row's verbs, in the order they should be offered. */
    actions: ContextAction[];
    locale?: LocaleCode;
    /** Overrides the built-in "Actions" / "Aksiyon" label. */
    label?: string;
    /** The whole control is off (a row being saved, a read-only demo). */
    disabled?: boolean;
    theme?: ThemeMode;
    /** A test hook, so a spec can address one row's control without guessing. */
    testid?: string;
  }>(),
  { locale: 'en' as LocaleCode },
);

const emit = defineEmits<{
  (e: 'select', key: string, action: ContextAction): void;
}>();

const { t, dir } = useLocale(() => props.locale);

const menu = ref<{ show: (ev: { clientX: number; clientY: number }, nodes: []) => void } | null>(
  null,
);

const text = computed(() => props.label || t('row.actions'));

/**
 * ⚠ Every entry gets an address. The menu teleports to <body>, so an entry
 * cannot be reached through the component that opened it, and the verbs that
 * used to be loose buttons each had a `data-testid` of their own — losing
 * those would have made the move untestable and quietly un-automatable. An
 * entry's own `testid` wins; otherwise it is this control's testid plus the
 * action key, so `user-actions-3` yields `user-actions-3-delete`.
 */
const items = computed<ContextAction[]>(() =>
  props.actions
    .filter((a) => !a.hidden)
    .map((a) => ({
      ...a,
      shortcutId: a.shortcutId ?? '',
      testid: a.testid ?? (props.testid ? `${props.testid}-${a.key}` : undefined),
    })),
);

/** A control with nothing behind it is not a control. A row whose every verb
 *  is conditional (Queue: retry only when failed, cancel only when pending)
 *  can legitimately have none, and then the button is disabled rather than
 *  absent, so the column does not go ragged from row to row. */
const usable = computed(() => items.value.some((a) => !a.divider && !a.disabled));

function open(ev: MouseEvent) {
  // Anchored to the BUTTON's own corner, exactly as the explorer's ⋮ is
  // (ListView `onRowMenu`): the menu drops from the control the person
  // clicked, and ContextMenu flips it left/up itself when the viewport edge
  // is close — which, for a control pinned to the right edge, is always.
  const box = (ev.currentTarget as HTMLElement | null)?.getBoundingClientRect();
  menu.value?.show(
    // ⚠ RTL: the START corner (right in RTL), as ListView `onRowMenu` does.
    { clientX: box ? inlineStartX(box, dir.value) : ev.clientX, clientY: box ? box.bottom : ev.clientY },
    [],
  );
}

function pick(a: ContextAction) {
  emit('select', a.key, a);
}
</script>

<template>
  <!-- ⚠ `click.stop`: the shared table emits `row-click` from the cell, and a
       row that navigates would otherwise fire it under the menu. -->
  <div class="tbl-rowactions" @click.stop>
    <button
      type="button"
      class="tbl-rowactions__btn"
      :disabled="disabled || !usable"
      :data-testid="testid"
      data-fe-control
      aria-haspopup="menu"
      @click.stop="open"
    >
      <span>{{ text }}</span>
      <span class="tbl-rowactions__caret" aria-hidden="true">▾</span>
    </button>
    <ContextMenu ref="menu" :locale="locale" :theme="theme" :actions="items" @select="pick" />
  </div>
</template>
