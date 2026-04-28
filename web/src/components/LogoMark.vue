<script setup lang="ts">
// Inline SVG logo so we don't need an asset pipeline. A tiny folder + checkmark
// metaphor — recognizable as a file manager. `class` falls through to root.
</script>

<template>
  <svg viewBox="0 0 32 32" aria-hidden="true">
    <defs>
      <linearGradient id="filex-grad" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stop-color="#6366f1" />
        <stop offset="100%" stop-color="#4338ca" />
      </linearGradient>
    </defs>
    <rect width="32" height="32" rx="7" fill="url(#filex-grad)" />
    <path
      d="M7 11a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V11z"
      fill="none"
      stroke="#fff"
      stroke-width="1.8"
      stroke-linejoin="round"
    />
    <path
      d="M11.5 17.5l3 2.5 5.5-6"
      fill="none"
      stroke="#fff"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>
