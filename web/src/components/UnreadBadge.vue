<script setup lang="ts">
/**
 * UnreadBadge — the count, drawn ON an icon. THE one of them.
 *
 * Owner's ruling, 2026-09-20, verbatim: *"Uygulamada bildirim gelince köşe
 * ikonda bildirim sayısını gösterelim; mobilde de ikon üstünde; 99 üzeri
 * 99+."*
 *
 * ⚠⚠ It is a component rather than a `<span>` copied into each bell because
 * there is more than one bell: the admin panel's top nav draws one, the
 * explorer's header cluster draws one, the full-list screen draws one in its
 * own heading, and the desktop app draws the same number on its dock icon
 * (through `lib/unreadBadge.ts`, which this component is the DOM half of).
 * Four copies of "99+" is four chances for one of them to say `100`.
 *
 * ⚠ It renders NOTHING at zero. A badge showing `0` is a badge that says
 * "there is news" in the exact shape of the thing that says there is none.
 *
 * ⚠ `aria-hidden`: the count belongs in the host control's accessible NAME
 * (the bell button says "Notifications — 7 unread"), not as a second stray
 * number a screen reader meets on its own. The host owns the sentence; this
 * owns the picture.
 */
import { computed } from 'vue';
import { unreadBadgeLabel } from '@/lib/unreadBadge';

const props = defineProps<{
  /** The unread count as the server last reported it. */
  count: number | null | undefined;
}>();

const label = computed(() => unreadBadgeLabel(props.count));
</script>

<template>
  <span
    v-if="label"
    class="fx-badge"
    aria-hidden="true"
    data-testid="unread-badge"
  >{{ label }}</span>
</template>

<style>
/* ⚠ NOT scoped, and every rule is `fx-badge`-prefixed. The badge is drawn
   inside the explorer's header — teleported panels and custom-element
   boundaries included — where a `[data-v-…]` attribute would not survive the
   trip. Same decision, same reason, as NotificationBell's own styles.

   ⚠ Declared `--fe-*` tokens only: the badge sits on controls the viewer may
   have re-themed, so its ink has to be the pairing the theme contrast test
   pins in every palette (primary on text-on-primary), and its ring has to be
   the ground it happens to sit on. */
.fx-badge {
  position: absolute;
  top: 2px;
  inset-inline-end: 1px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 999px;
  background: var(--fe-primary);
  color: var(--fe-text-on-primary);
  box-shadow: 0 0 0 2px var(--fe-bg);
  font-family: var(--fe-font);
  font-size: 10px;
  font-weight: 600;
  line-height: 16px;
  text-align: center;
  /* The badge is a picture of a number drawn over a control; a click on it is
     a click on the control underneath, never on the badge. */
  pointer-events: none;
}
</style>
