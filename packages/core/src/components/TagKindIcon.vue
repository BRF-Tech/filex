<script setup lang="ts">
/**
 * TagKindIcon — the one glyph that says which KIND a tag is.
 *
 * ⚠⚠ Why this exists (v0.43.0): tags used to be one kind, silently shared with
 * every account on the server while the code called them per-user; a tester's
 * "müşteri teklifi" showed up in other people's panels and they could remove
 * it, and nothing on screen had ever said a tag was shared. With two kinds the
 * screen has to say it EVERYWHERE a tag appears — the picker, the chips on a
 * file, the navigation panel, the tag view's heading, the search filter — and
 * one component is the only way those five cannot draw it five ways.
 *
 *   personal — one figure: yours, like a star.
 *   team     — two figures: everyone in your team who can see the file.
 *
 * Decorative (`aria-hidden`): every caller also puts the kind into words
 * (`tags.kind.*` in the title / accessible name), because an icon alone is
 * not an explanation.
 */
import type { TagKind } from '../lib/tags';

defineProps<{
  kind: TagKind;
  /** Edge length in px. */
  size?: number;
}>();
</script>

<template>
  <svg
    class="fe-tagkind"
    :class="`fe-tagkind--${kind}`"
    :width="size ?? 12"
    :height="size ?? 12"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    :data-tag-kind="kind"
  >
    <template v-if="kind === 'team'">
      <circle cx="9" cy="8" r="3.2" />
      <path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6" />
      <circle cx="17" cy="9" r="2.6" />
      <path d="M16.5 14.2c2.6.3 4.5 2.6 4.5 5.3" />
    </template>
    <template v-else>
      <circle cx="12" cy="8" r="3.6" />
      <path d="M5 20c0-3.9 3.1-7 7-7s7 3.1 7 7" />
    </template>
  </svg>
</template>
