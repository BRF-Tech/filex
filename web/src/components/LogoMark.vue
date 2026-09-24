<script setup lang="ts">
/**
 * The filex mark, re-exported.
 *
 * ⚠⚠ The drawing itself lives in `@brftech/filex-core`
 * (`packages/core/src/components/LogoMark.vue`) and NOT here, because the
 * public shell draws it too and `packages/core` cannot import out of the web
 * app. This file stays so the five call sites in `web/` (the sidebar, About,
 * Explore's empty state, the sign-in page twice) keep their import path and
 * their `class` — attributes still fall through to the `<svg>`.
 *
 * ⚠ Do not paste the path data back in. `scripts/dup-scan.mjs` → `brand-mark`
 * names the core file as the concept's one home and
 * `web/tests/quality/duplication.test.ts` fails on a second copy — the public
 * share page shipped the pre-rebrand indigo for a whole release because its
 * mark had been re-typed instead of generated from the one home.
 */
import { LogoMark as CoreLogoMark } from '@brftech/filex-core';
</script>

<template>
  <CoreLogoMark />
</template>
