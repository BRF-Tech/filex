<script setup lang="ts">
/**
 * ItemCheck — the checkbox on a listing item: the list row's tick column and
 * the grid and gallery cards' corner (issue #26, fourth round: "only clicking
 * on checkbox selects it"). One element for the three views, so the tick a
 * card shows cannot drift from the one a row shows.
 *
 * The view binds the click on the wrapper around it and routes it through
 * `checkMod` — the same `click-row` emit and the same `useSelection.click`
 * every selection goes through, so shift ranges and toggles are one
 * implementation, not two.
 *
 * ⚠ A button with role="checkbox", NOT an <input type="checkbox">. The input
 * owns a `checked` state of its own: the browser flips it before the click
 * handler runs and, if the default is prevented, flips it back AFTER Vue has
 * patched — so the row went selected while the box stayed empty (measured:
 * rowSelected=true, domChecked=false). Drawing the state from the selection
 * leaves nothing to drift. Space and Enter still tick it, because a button
 * fires `click` for both.
 *
 * ⚠ A mouse press does not move focus onto it (`mousedown.prevent`). The tick
 * is now the ONLY click that selects, so "tick, then Space" is the way to
 * quick-look a file — and Space on a focused button is that button's own
 * activation, which the shortcut layer rightly leaves alone: the peek would
 * never open and the tick would flip back off. Tab still reaches it, and Space
 * still ticks it from the keyboard.
 */
defineProps<{
  /** Whether the item is selected. */
  on: boolean;
  /** The item's display name — the checkbox's accessible name. */
  label: string;
}>();
</script>

<template>
  <button
    type="button"
    class="fe-list__check"
    :class="{ 'is-on': on }"
    role="checkbox"
    :aria-checked="on ? 'true' : 'false'"
    :aria-label="label"
    :title="label"
    @mousedown.prevent
  ></button>
</template>
