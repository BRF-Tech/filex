<script setup lang="ts">
// A "full" trash-can icon in lucide's stroke style — a bin with an overflowing
// mound on top + contents inside. Pairs with lucide `Trash2` (the empty bin).
withDefaults(defineProps<{ size?: number | string }>(), { size: 24 });
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M3 6h18" />
    <path d="M5 6v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V6" />
    <path d="M8 6c0-1.6 1.6-2.6 4-2.6s4 1 4 2.6" />
    <path d="M10 11v6M14 11v6" />
  </svg>
</template>
