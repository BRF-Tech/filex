<script setup lang="ts">
// The scroll container every admin table sits in — the horizontal scroll and
// the "is something scrolled underneath the pinned column" flag, in ONE place.
// The rules it drives are in src/styles/table.css (`.tbl-scroll`,
// `.tbl-actions`, `.is-scrolled-x`); mark the actions <th>/<td> with
// `tbl-actions` and this wrapper does the rest. ui/Table.vue uses it too, so a
// hand-rolled <table> and the shared one behave identically.
// The flag itself is the core's `useScrolledX` — the same five lines the
// explorer-side key tables use, imported rather than restated.
import { useScrolledX } from '@brftech/filex-core/src/composables/useScrolledX';

const { scrolledX, onScroll } = useScrolledX();
</script>

<template>
  <div class="tbl-scroll" :class="{ 'is-scrolled-x': scrolledX }" @scroll.passive="onScroll">
    <slot />
  </div>
</template>
