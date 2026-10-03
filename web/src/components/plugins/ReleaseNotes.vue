<script setup lang="ts">
/**
 * A release's notes as the source wrote them - Markdown, a GitHub release's
 * body - on an update's review (#122: `**bold**` used to reach the reader as
 * asterisks).
 *
 * Drawn through the explorer preview's own pipeline, `markdownToSafeHtml`
 * from @brftech/filex-core: markdown-it, then the document sanitizer
 * (sanitizeHtml 'document', DOMPurify). Only its answer reaches v-html. While
 * it loads, or where markdown-it cannot be loaded, the notes are plain text -
 * never the raw string as markup.
 */
import { ref, watch } from 'vue';
import { markdownToSafeHtml } from '@brftech/filex-core';

const props = defineProps<{
  notes: string;
  /** data-testid of the rendered notes. */
  testid?: string;
}>();

const html = ref<string | null>(null);
let seq = 0;
watch(
  () => props.notes,
  async (notes) => {
    const mine = ++seq;
    html.value = null;
    if (!notes.trim()) return;
    const out = await markdownToSafeHtml(notes);
    if (mine === seq) html.value = out === null ? null : linksOutward(out);
  },
  { immediate: true },
);

/**
 * A link in the notes (a changelog, a compare view) opens in a new tab, with
 * no handle back to this page (`noopener noreferrer`): followed in place it
 * would take the administrator off the review half-way. Only http(s) links -
 * the sanitizer has already removed every other scheme that could run.
 */
function linksOutward(markup: string): string {
  // An inert document: DOMParser runs nothing and loads nothing it parses.
  const doc = new DOMParser().parseFromString(`<body>${markup}</body>`, 'text/html');
  for (const a of Array.from(doc.body.querySelectorAll('a[href]'))) {
    if (/^https?:\/\//i.test(a.getAttribute('href') ?? '')) {
      a.setAttribute('target', '_blank');
      a.setAttribute('rel', 'noopener noreferrer');
    }
  }
  return doc.body.innerHTML;
}
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- sanitized by markdownToSafeHtml (sanitizeHtml 'document') -->
  <div v-if="html !== null" class="fx-release-notes mt-1 max-h-40 overflow-auto break-words text-xs text-zinc-600 dark:text-zinc-400" :data-testid="testid" v-html="html"></div>
  <p v-else class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-words text-xs text-zinc-600 dark:text-zinc-400">{{ notes }}</p>
</template>

<style scoped>
/* Tailwind's reset leaves lists, headings and code unmarked: give the few
   elements a release body uses their shape back, inside the notes only. */
.fx-release-notes :deep(p),
.fx-release-notes :deep(ul),
.fx-release-notes :deep(ol),
.fx-release-notes :deep(pre) {
  margin: 0.25rem 0;
}
.fx-release-notes :deep(ul) {
  list-style: disc;
  padding-inline-start: 1.25rem;
}
.fx-release-notes :deep(ol) {
  list-style: decimal;
  padding-inline-start: 1.25rem;
}
.fx-release-notes :deep(h1),
.fx-release-notes :deep(h2),
.fx-release-notes :deep(h3),
.fx-release-notes :deep(h4) {
  margin: 0.5rem 0 0.25rem;
  font-weight: 600;
}
.fx-release-notes :deep(code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.95em;
}
.fx-release-notes :deep(pre) {
  overflow-x: auto;
  white-space: pre;
}
.fx-release-notes :deep(a) {
  text-decoration: underline;
}
</style>
