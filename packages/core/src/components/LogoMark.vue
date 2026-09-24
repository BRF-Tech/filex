<script setup lang="ts">
// The filex mark — the ONE copy of it in the product's own code.
//
// A tiny folder + checkmark: recognisable as a file manager, inline SVG so it
// needs no asset pipeline. `class` falls through to the root element, so a
// caller sizes it.
//
// ⚠⚠ It lives in `packages/core` and not in `web/` because the public shell
// (`components/public/PublicShell.vue`) draws it too, and `packages/core`
// cannot import out of the web app. `web/src/components/LogoMark.vue` is a
// re-export of this file; every existing call site keeps working.
// `scripts/dup-scan.mjs` names THIS path as the concept's home
// (`brand-mark`), and `web/tests/quality/duplication.test.ts` fails on a
// second hand-typed copy — for a reason with a date on it: the public share
// page carried the pre-rebrand indigo for a whole release because its mark
// was re-typed in Go rather than generated from here.
//
// gorunum:v1 — flat product blue, not the old indigo→violet gradient ("maviye
// çekelim"). The fill is the palette's own `--fe-primary`, so the mark is
// #2f6ceb on a light page and #5b8cff on a dark one, and it follows any theme
// the product ever ships rather than being a colour no theme can reach. On a
// public link with an instance accent set the same token carries the
// operator's colour (`accentStyleOf`), which is the behaviour a brandable
// mark should have.
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
