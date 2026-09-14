<script setup lang="ts">
// Inline SVG logo so we don't need an asset pipeline. A tiny folder + checkmark
// metaphor — recognizable as a file manager. `class` falls through to root.
//
// gorunum:v1 — flat product blue, not the old indigo→violet gradient ("maviye
// çekelim"). The fill is the palette's own `--fe-primary`, so the mark is
// #2f6ceb on a light page and #5b8cff on a dark one, and it follows any theme
// the product ever ships rather than being a colour no theme can reach.
//
// ⚠ The literal is a floor, not the value in use: `fill` has no inherited
// value to fall back to, so an unstyled context (core's stylesheet missing)
// would paint the mark BLACK rather than blue. Measured in the browser on
// /admin/login, /admin/dashboard and /admin/about: the computed fill is the
// token in all three, in both variants — the literal never renders today.
//
// The same blue is written out by hand in three static files that have no
// token scope at all: web/public/favicon.svg, web/public/icons/icon.svg and
// the `theme-color` meta in web/index.html (plus the PWA manifest's
// theme_color in web/vite.config.ts). Change one, change all four.
</script>

<template>
  <svg viewBox="0 0 32 32" aria-hidden="true">
    <rect width="32" height="32" rx="7" fill="var(--fe-primary, #2f6ceb)" />
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
