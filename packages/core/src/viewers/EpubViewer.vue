<script setup lang="ts">
import { requestFailure } from '../lib/errorWords';
/**
 * EpubViewer — flowable EPUB reader via `epubjs`.
 *
 * Lazy-imports `epubjs` (~80 KB gzipped). epub.js renders the publication
 * inside an iframe (default `flow: 'paginated'`), but we use it in
 * scrolled-doc mode only when the user toggles the "fit-width" flag — by
 * default we keep paginated since that matches reader-app expectations.
 *
 * UX:
 *   - prev/next page buttons (also keyboard arrow keys via composable)
 *   - collapsible TOC sidebar (built from `book.loaded.navigation`)
 *   - font size +/- (Themes API)
 *   - graceful fallback if peer not installed
 *
 * A chapter asks no other server for anything until the person says so
 * (lib/epubRemote): before epub.js draws it, its head gets a policy that lets
 * its frame load only the book's own files, and no referrer. When it names
 * something from elsewhere the reader says so, and the question
 * (composables/useRemoteImages, about the whole book: one frame, one answer)
 * or the person's setting draws the chapters again under the allowed policy.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { fileIconTile } from '../lib/fileIcons'; /* ikon:emoji */
import { allowChapter, remoteReferencesOf, sealChapter } from '../lib/epubRemote';
import { injectRemoteImages, useRemoteImages } from '../composables/useRemoteImages';
import RemoteImageDialog from '../modals/RemoteImageDialog.vue';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  t?: (key: string) => string;
  /** The reader's language, for the question held pictures ask on their own. */
  locale?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
}>();

/* Pictures from elsewhere (lib/epubRemote): inside the preview its gate
   asks, on its own this viewer asks itself. */
const parentGate = injectRemoteImages();
const remoteImages = parentGate ?? useRemoteImages(() => props.locale);
const ownGate = !parentGate;
/** The addresses from elsewhere the chapters drawn so far name, while held. */
const heldRemote = ref<string[]>([]);
/** The held policies of the chapters epub.js keeps, to switch once allowed. */
const sealed = new Set<Element>();

/** epub.js's content hook: every chapter, before it is drawn. */
function onChapter(doc: Document): void {
  const allowed = remoteImages.allLoaded.value;
  const remote = allowed ? [] : remoteReferencesOf(doc);
  const csp = sealChapter(doc, allowed);
  if (csp && !allowed) sealed.add(csp);
  if (remote.length) heldRemote.value = [...new Set([...heldRemote.value, ...remote])];
}

const RENDITION_OPTS = {
  width: '100%',
  height: '100%',
  flow: 'paginated',
  manager: 'default',
  // epub.js draws each chapter in a sandboxed frame; scripts in a book
  // stay off (its default, said out loud so it cannot drift).
  allowScriptedContent: false,
};

interface TocNode {
  href: string;
  label: string;
  subitems?: TocNode[];
}

const root = ref<HTMLDivElement | null>(null);
const containerRef = ref<HTMLDivElement | null>(null);
const error = ref<string | null>(null);
const toc = ref<TocNode[]>([]);
const tocOpen = ref(false);
const fontSize = ref(100);
const ready = ref(false);
const loading = ref(true);

let book: any = null;
let rendition: any = null;

/**
 * How long an epub.js step may hang before we call it a failure.
 *
 * Generous on purpose: a large publication on a slow disk legitimately takes
 * seconds, and a false "cannot open" on a book that would have rendered is a
 * worse bug than the one this guards against.
 */
const EPUB_STEP_TIMEOUT_MS = 15000;

/** Reject if `p` has not settled within `EPUB_STEP_TIMEOUT_MS`. */
function withTimeout<T>(p: Promise<T>, label: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout>;
  return Promise.race([
    p,
    new Promise<never>((_, reject) => {
      timer = setTimeout(
        () => reject(new Error(`EPUB ${label} timed out after ${EPUB_STEP_TIMEOUT_MS}ms`)),
        EPUB_STEP_TIMEOUT_MS,
      );
    }),
  ]).finally(() => clearTimeout(timer)) as Promise<T>;
}

async function load(): Promise<void> {
  loading.value = true;
  ready.value = false;
  error.value = null;
  let mod: any = null;
  try {
    mod = await import(/* @vite-ignore */ 'epubjs');
  } catch {
    error.value = tt('viewer.peer_not_installed', 'This kind of file cannot be shown here. Download it to open it on your device.');
    loading.value = false;
    return;
  }
  try {
    const Epub = mod.default ?? mod;
    // epub.js wants a URL (it then fetches with XHR). When the host
    // site requires auth headers we have to load the file ourselves
    // and pass an ArrayBuffer instead.
    let source: string | ArrayBuffer = props.url;
    if (props.authHeaders) {
      const headers = await props.authHeaders();
      const res = await fetch(props.url, {
        headers,
        credentials: props.authCredentials || 'same-origin',
      });
      if (!res.ok) throw requestFailure(res.status, await res.text().catch(() => ''), undefined);
      source = await res.arrayBuffer();
    }
    book = Epub(source);
    heldRemote.value = [];
    sealed.clear();
    book.spine?.hooks?.content?.register?.(onChapter);
    if (!containerRef.value) {
      throw new Error('EPUB mount target missing');
    }
    rendition = book.renderTo(containerRef.value, RENDITION_OPTS);
    // ⚠⚠ Both of these have to be raced against a clock. epub.js resolves
    // `display()` and `loaded.navigation` from its own internal promises, and
    // when the archive is not a readable EPUB it simply NEVER settles —
    // neither resolve nor reject — so the `catch` below can't fire and
    // `finally` never runs. Measured 2026-09-13 on an 8-byte `handbook.epub`:
    // the pane sat on "Yükleniyor…" forever with no error anywhere.
    await withTimeout(rendition.display(), 'display');
    rendition.themes.fontSize(fontSize.value + '%');
    // `<any>` explicitly: `book` is `any`, so without it `T` infers as `{}`
    // and `nav.toc` stops type-checking.
    const nav = await withTimeout<any>(book.loaded.navigation, 'navigation');
    toc.value = (nav?.toc ?? []) as TocNode[];
    ready.value = true;
  } catch (err) {
    // The reader gets the localized sentence; epub.js's own wording (and the
    // timeout marker) goes to the console for whoever is debugging.
    console.warn('[filex] EPUB load failed:', err);
    error.value = tt('viewer.failed_to_load', 'Failed to load file');
  } finally {
    loading.value = false;
  }
}

/**
 * The person allowed the book's pictures from elsewhere (the question, or
 * their setting): the chapters epub.js keeps get the allowed policy, and the
 * one on screen is drawn again, where it was.
 */
async function drawAgain(): Promise<void> {
  if (!book || !rendition || !containerRef.value) return;
  for (const el of sealed) allowChapter(el);
  sealed.clear();
  heldRemote.value = [];
  const where = rendition.location?.start?.cfi ?? rendition.currentLocation?.()?.start?.cfi;
  try {
    rendition.destroy();
  } catch {
    /* ignore */
  }
  rendition = book.renderTo(containerRef.value, RENDITION_OPTS);
  try {
    await withTimeout(rendition.display(where), 'display');
    rendition.themes.fontSize(fontSize.value + '%');
  } catch (err) {
    console.warn('[filex] EPUB redraw failed:', err);
  }
}
watch(
  () => remoteImages.allLoaded.value,
  (now) => {
    if (now && sealed.size) void drawAgain();
  },
);

function next(): void {
  rendition?.next?.();
}
function prev(): void {
  rendition?.prev?.();
}
function gotoHref(href: string): void {
  rendition?.display?.(href);
  tocOpen.value = false;
}
function bigger(): void {
  fontSize.value = Math.min(200, fontSize.value + 10);
  rendition?.themes?.fontSize(fontSize.value + '%');
}
function smaller(): void {
  fontSize.value = Math.max(60, fontSize.value - 10);
  rendition?.themes?.fontSize(fontSize.value + '%');
}

function onKey(ev: KeyboardEvent): void {
  if (ev.key === 'ArrowRight' || ev.key === 'PageDown') next();
  else if (ev.key === 'ArrowLeft' || ev.key === 'PageUp') prev();
}

onMounted(() => {
  load();
  window.addEventListener('keydown', onKey);
});

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey);
  try {
    rendition?.destroy?.();
  } catch {
    /* ignore */
  }
  try {
    book?.destroy?.();
  } catch {
    /* ignore */
  }
  rendition = null;
  book = null;
});

watch(
  () => props.url,
  () => {
    if (rendition) {
      try {
        rendition.destroy();
      } catch {
        /* ignore */
      }
    }
    if (book) {
      try {
        book.destroy();
      } catch {
        /* ignore */
      }
    }
    book = null;
    rendition = null;
    load();
  },
);

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}

/* === ikon:emoji — the fallback screen's mark ==========================
 * Every viewer opened its "cannot show this" / "still loading" screen with a
 * 48px colour emoji, one per format, each from whatever emoji font the OS
 * shipped. The format mark is `lib/fileIcons`'s tile — the SAME tile the row
 * the person just clicked is wearing, so the fallback is recognisably about
 * that file — and "loading" is the stroked ring, spun by CSS, because no
 * still picture can say "still going". */
const typeTile = computed(() => fileIconTile({ type: 'file', extension: props.ext }));
</script>

<template>
  <div ref="root" class="filex-viewer-epub">
    <div v-if="error" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="typeTile"></span>
      <p>{{ error }}</p>
    </div>
    <template v-else>
      <div
        v-if="heldRemote.length && !remoteImages.allLoaded.value"
        class="filex-viewer-epub__held"
        role="status"
        data-testid="epub-remote-notice"
      >
        <span>{{ tt('viewer.remote_image.book_held', 'Pictures from other servers in this book are not loaded.') }}</span>
        <button type="button" class="filex-viewer-btn" data-testid="epub-remote-load" @click="remoteImages.askWhole(heldRemote)">
          {{ tt('viewer.remote_image.book_load', 'Load remote images…') }}
        </button>
      </div>
      <div ref="containerRef" class="filex-viewer-epub__rendition" />
      <div v-if="ready" class="filex-viewer-epub__nav">
        <button type="button" class="filex-viewer-btn" @click="prev" :disabled="!ready">‹</button>
        <button type="button" class="filex-viewer-btn" @click="next" :disabled="!ready">›</button>
      </div>
      <div v-if="loading" class="filex-viewer-epub__loading">
        {{ tt('viewer.loading', 'Loading…') }}
      </div>
    </template>
    <RemoteImageDialog v-if="ownGate" :gate="remoteImages" :locale="locale" />
  </div>
</template>

<style>
/* ⚠ NOT `scoped`, deliberately. Vue's scoped styles compile to
   `.cls[data-v-HASH]`, and in the web-component build the hash baked into
   this CSS does not match the one Vue stamps onto the DOM — so every rule
   here silently stopped applying. Measured in the desktop app: the share
   dialog had `position: static`, no background and no radius, i.e. raw
   unstyled HTML, in EVERY embedded surface.
   Safe to drop: every selector below is prefixed (fx-/fe-/filex-), so
   there is nothing here that can leak into a host page. */
.filex-viewer-epub__held {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 6px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  color: var(--fe-text-muted, #5b6474);
  font-size: 13px;
}
.filex-viewer-epub__nav {
  position: absolute;
  bottom: 12px;
  left: 50%; /* rtl-physical: centred by translate(-50%) — the same place in both directions */
  transform: translateX(-50%);
  display: flex;
  gap: 8px;
  padding: 4px 8px;
  background: var(--fe-bg-elev, rgba(255, 255, 255, 0.9));
  border: 1px solid var(--fe-border, #e2e6ed);
  border-radius: 6px;
  backdrop-filter: blur(4px);
}
.filex-viewer-epub {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
  position: relative;
}
.filex-viewer-epub__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 13px;
}
.filex-viewer-spacer { flex: 1; }
.filex-viewer-epub__fs {
  display: inline-block;
  min-width: 40px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-epub__main {
  flex: 1;
  min-height: 0;
  display: flex;
}
.filex-viewer-epub__rendition {
  flex: 1;
  min-height: 0;
}
.filex-viewer-epub__toc {
  width: 260px;
  border-inline-end: 1px solid var(--fe-border, #e2e6ed);
  overflow-y: auto;
  padding: 12px 0;
  background: var(--fe-bg-elev, #f7f8fa);
}
.filex-viewer-epub__toc ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.filex-viewer-epub__toc-link {
  display: block;
  width: 100%;
  text-align: start;
  background: transparent;
  border: 0;
  padding: 6px 14px;
  font: inherit;
  color: inherit;
  cursor: pointer;
}
.filex-viewer-epub__toc-link:hover {
  background: var(--fe-bg-hover, #edf0f5);
}
.filex-viewer-epub__toc-link.is-child {
  padding-inline-start: 28px;
  color: var(--fe-text-muted, #5a6475);
  font-size: 12px;
}
.filex-viewer-epub__loading {
  position: absolute;
  inset: auto 0 0 0;
  text-align: center;
  padding: 6px;
  background: rgba(0, 0, 0, 0.05);
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-fallback {
  text-align: center;
  padding: 32px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-fallback__icon {
  font-size: 48px;
  display: block;
  margin-bottom: 12px;
}
.filex-viewer-btn {
  border: 1px solid var(--fe-border, #e2e6ed);
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
  padding: 4px 10px;
  border-radius: 4px;
  cursor: pointer;
  font: inherit;
  font-size: 12px;
}
.filex-viewer-btn:hover:not(:disabled) {
  background: var(--fe-bg-hover, #edf0f5);
}
.filex-viewer-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
