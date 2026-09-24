<script setup lang="ts">
/**
 * ProductVersion — `filex 0.43.0`, as a quiet line (lib/productVersion).
 *
 * The one piece every place that shows the version draws: the admin panel's
 * account menu, the explorer's avatar menu, the user settings dialog. Written
 * once here so the desktop shell (which draws the web app) and an embedding
 * host (which draws its own chrome and can mount this) print the same line.
 *
 * ⚠ `<bdi>`: a Latin name and a dotted number inside a right-to-left menu
 * must keep their own order, and the line still starts on the menu's start
 * side. `dir="ltr"` on the element would flip its alignment as well.
 *
 * ⚠ Its rules are `.fe-version` in styles/base.css, not a `<style>` block
 * here: a component's own styles reach only a bundle that imports it, and
 * the two shipped stylesheets drift (lesson #416).
 */
import { computed } from 'vue';
import { productVersionLine } from '../lib/productVersion';

const props = defineProps<{
  /** The server's version string (capabilities). Unknown → nothing drawn. */
  version?: string | null;
}>();

const line = computed(() => productVersionLine(props.version));
</script>

<template>
  <p v-if="line" class="fe-version" data-testid="product-version"><bdi>{{ line }}</bdi></p>
</template>
