<script setup lang="ts">
/**
 * PluginPageView — an app plugin's `page` view, drawn as a whole page.
 *
 * ⚠⚠ The SAME conversation a `modal` view has. `usePluginSurface` is the
 * shared half (the surface, the values, the debounced `change`, the footer
 * press, the `{op}` answer) and this file is only the frame: a title bar of
 * its own, one column at reading width, the footer buttons at the bottom,
 * and no dialog scroll inside a dialog. Writing a second conversation here
 * is how a `page` wizard and a `modal` wizard start disagreeing about what
 * `submit` sends.
 *
 * Its own two states, which a modal has no use for:
 *   • QUEUED — a `page` action never goes through `…/run`, so the job is
 *     born from this screen's submit. A tab that just closed itself would
 *     leave the person with no idea whether anything happened; instead the
 *     page says the job is queued and offers the way back to the operations
 *     tray that is now tracking it.
 *   • DONE — the plugin ended the conversation (`done`), with nothing queued.
 *
 * ⚠ It is in the package, not in the admin SPA: fm.example.com, the desktop shell
 * and the embeds all reach `page` views through the same explorer, so a copy
 * per host would be a copy per host to keep in step.
 */
import { computed, onMounted, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import { useLocale } from '../../composables/useLocale';
import { usePluginSurface } from '../../composables/usePluginSurface';
import { labelOf } from '../../lib/pluginLabel';
import SurfaceConversation from './SurfaceConversation.vue';
import SurfaceFooterButtons from './SurfaceFooterButtons.vue';
import SurfaceSections from './SurfaceSections.vue';
import { walkNodes } from '../../lib/surfaceValues';
import { openTargetFor } from '../../lib/surfaceOpen';

const props = defineProps<{
  locale: LocaleCode;
  theme?: ThemeMode;
  api: FileApi;
  plugin: string;
  view: string;
  /** Adapter-qualified path the page was opened on; echoed on every event. */
  path?: string;
  /** Storage names a file-chooser may span; where its picker opens. */
  storages?: string[];
  startAt?: string;
  /** Where "back to the files" goes. Absent: the button is not drawn. */
  backHref?: string;
  /** Where the operations tray lives, for the queued state. */
  opsHref?: string;
  /**
   * The prefix the SPA is served from (`/admin/`, `/drive/`) — what a
   * `Surface.open` address is built against (v3 §3.0). Absent: the request
   * is dropped, because a wrong address is worse than none.
   */
  mountBase?: string;
  /**
   * The CHROME around the conversation; the conversation itself is the same
   * either way.
   *
   * `page` (the default) is a browser tab of its own: a title bar, one column
   * at reading width, the viewport's height, a footer stuck to its bottom.
   * `embedded` is the same screen drawn INSIDE a host's page — the admin
   * panel's Apps section, an embed that already has a header — and brings
   * none of those three, because the host has already said where the screen
   * begins, how wide it is and what it is called.
   *
   * ⚠ A step carrying a document keeps the INLINE layout when embedded. The
   * `page` rule fits a PDF to the VIEWPORT (v3 §3.2); inside a host's page
   * the viewport is not this frame's box, so the same rule would fit the
   * document to a height the frame does not have.
   */
  frame?: 'page' | 'embedded';
  /**
   * The section of a home page to open (`surface.sections`), as the host's
   * address says. ⚠ The HOST owns it: choosing a section emits `section`
   * and the host puts it in its URL, which comes back here — so Back walks
   * the sections, and a link names one.
   */
  section?: string;
}>();

const emit = defineEmits<{
  /** The server enqueued a job for this page; the raw ops row. */
  (e: 'op', op: Record<string, unknown>): void;
  /**
   * The person chose another section of a home page — or, with
   * `{replace: true}`, a page opened with NO section learned which one the
   * plugin shows first: the address should name it without adding a step
   * to history.
   */
  (e: 'section', id: string, how?: { replace?: boolean }): void;
}>();

const { t } = useLocale(() => props.locale);

/** Drawn inside a host's page rather than in a tab of its own. */
const embedded = computed(() => props.frame === 'embedded');

type PageState = 'loading' | 'surface' | 'queued' | 'done' | 'error';
const state = ref<PageState>('loading');
const loadError = ref('');
const toast = ref('');

const conv = usePluginSurface(
  {
    api: props.api,
    plugin: props.plugin,
    view: props.view,
    path: () => props.path,
    locale: () => props.locale,
    errorText: () => t('plugin.view.error'),
  },
  {
    // ⚠ `onOp` fires BEFORE `onDone` (usePluginSurface posts them in that
    // order), so the queued state has to be set after both — otherwise
    // `onDone` would overwrite it with "finished" and the person would never
    // see that a job is running.
    /**
     * v3 §3.0 — "go to this file, and start that screen on it".
     *
     * ⚠ A real navigation, not a tab: this IS a whole page, and the person
     * asked to be taken to a document. The address is the explorer deep link
     * a notification already uses, so the placement question is answered
     * where it is answerable — the explorer knows which of the plugin's
     * views is a `page` and opens that one in its own tab.
     */
    onOpen: (req) => {
      const target = openTargetFor(req, { plugin: props.plugin, base: props.mountBase });
      if (!target.href) return;
      try {
        if (target.newTab) {
          const win = window.open(target.href, '_blank');
          if (win) win.opener = null;
          else window.location.assign(target.href);
        } else {
          window.location.assign(target.href);
        }
      } catch {
        /* a blocked navigation leaves the screen as it is */
      }
    },
    onOp: (op) => {
      emit('op', op);
      state.value = 'queued';
    },
    onDone: () => {
      if (state.value !== 'queued') state.value = 'done';
    },
    onToast: (m) => (toast.value = m),
  },
);

const current = conv.current;
const title = computed(() => labelOf(current.value?.title, props.locale) || props.view);

/**
 * The section being shown. It starts from the host's address and follows
 * it; a choice made here moves it at once (so the menu answers the click
 * even in a host that does not keep an address), then tells the host.
 */
const wanted = ref(props.section ?? '');
const sections = computed(() => current.value?.sections ?? []);
/** Which entry is lit: what the plugin says it drew, else what was asked. */
const activeSection = computed(() => current.value?.section || wanted.value);

function chooseSection(id: string): void {
  if (!id || id === activeSection.value) return;
  wanted.value = id;
  emit('section', id);
  void load();
}

watch(
  () => props.section,
  (s) => {
    const next = s ?? '';
    // The host echoing the choice made above is not a new request.
    if (next === wanted.value) return;
    wanted.value = next;
    void load();
  },
);

async function load(): Promise<void> {
  // ⚠ A section change keeps the page standing (menu and all) while the
  // next table is fetched: a switch that blanked the screen to "Loading…"
  // made the menu itself flash away under the pointer that chose it.
  if (!current.value) state.value = 'loading';
  loadError.value = '';
  try {
    // The section only when there is one: a page that has no menu asks the
    // same question it always asked.
    const res = wanted.value
      ? await props.api.pluginView(props.plugin, props.view, props.path, wanted.value)
      : await props.api.pluginView(props.plugin, props.view, props.path);
    if (!res?.surface) {
      loadError.value = t('plugin.view.error');
      state.value = 'error';
      return;
    }
    conv.setSurface(res.surface);
    state.value = 'surface';
    // ⚠ Opened without a section, the page shows the plugin's first choice
    // — and the address has to say which, or choosing that same entry from
    // the menu changes nothing (measured in the e2e round: a click on the
    // lit "requested" left the URL bare), and a copied link opens wherever
    // the plugin's default happens to be that day.
    const shown = res.surface.section ?? '';
    if (!wanted.value && shown) {
      wanted.value = shown;
      emit('section', shown, { replace: true });
    }
  } catch (e) {
    // ⚠ An app's own error text never met the catalogue — isolate it.
    loadError.value = t.foreign?.(String((e as Error)?.message ?? '')) || t('plugin.view.error');
    state.value = 'error';
  }
}

onMounted(() => void load());
defineExpose({ reload: load });
/**
 * Is this step showing a DOCUMENT?
 *
 * ⚠⚠ v3 §3.2 — "in `page` placement the node takes the whole viewport
 * minus the step header and footer, and fits the page to it". The complaint
 * that started the round was a signature page you had to scroll in two
 * directions: the document was fitted to a 960px text column inside a page
 * that scrolled, so a portrait page ran off the bottom and the boxes were
 * found by hunting.
 *
 * So the FRAME changes shape when the surface carries one: the header and
 * the footer keep their size, the body becomes the remainder, and the node
 * fits the page into it (`SurfacePdfFields.fitWidth`). Only then — an
 * ordinary form step is still a readable column, and locking every step to
 * the viewport would turn a four-field form into a tall empty screen.
 */
/**
 * ⚠⚠ ...and only the step that REALLY shows one. A `pdf-fields` node in
 * `define` mode draws no document at all — it is a column of cards, one per
 * box — so shaping the frame to the window for it locked a growing list
 * inside `height: 100vh; overflow: hidden` with no scroller of its own: the
 * fourth box and everything after it sat below the fold, unreachable (the
 * owner, 2026-09-23: "kutucukları oluşturduğumuz sayfada overflow hidden
 * olduğu için aşağıda kalan ekstra kutucuklara kaydırarak gidemiyoruz").
 * `place` and `edit` do carry the document and keep the window shape.
 */
const showsDocument = computed(() => {
  let found = false;
  walkNodes(conv.current.value?.nodes, (n) => {
    if (n.type !== 'pdf-fields') return;
    if ((n.props as { mode?: unknown } | undefined)?.mode === 'define') return;
    found = true;
  });
  return found;
});

</script>

<template>
  <div
    class="fe fe-apppage"
    :class="[
      theme === 'dark' ? 'fe--theme-dark' : theme === 'light' ? 'fe--theme-light' : '',
      { 'is-doc': showsDocument && !embedded, 'fe-apppage--embedded': embedded },
    ]"
    data-testid="plugin-page"
  >
    <header v-if="!embedded" class="fe-apppage__bar">
      <h1 class="fe-apppage__title" data-testid="plugin-page-title">{{ title }}</h1>
      <p v-if="path" class="fe-apppage__path" :title="path">{{ path }}</p>
      <a v-if="backHref" class="fe-btn fe-apppage__back" :href="backHref" data-testid="plugin-page-back">
        {{ t('plugin.page_view.back') }}
      </a>
    </header>

    <main class="fe-apppage__body">
      <SurfaceSections
        v-if="state === 'surface' && sections.length"
        :sections="sections"
        :active="activeSection"
        :locale="locale"
        :label="title"
        :disabled="conv.busy.value"
        @select="chooseSection"
      />
      <p v-if="state === 'loading'" class="fe-surface__text fe-surface__text--muted" data-testid="plugin-page-loading">
        {{ t('plugin.view.loading') }}
      </p>

      <SurfaceConversation
        v-else-if="state === 'surface'"
        :conv="conv"
        :locale="locale"
        :theme="theme"
        :api="api"
        :plugin="plugin"
        :storages="storages"
        :start-at="startAt"
        :toast="toast"
        :layout="embedded ? 'inline' : 'page'"
      />

      <div v-else-if="state === 'queued'" class="fe-apppage__state" data-testid="plugin-page-queued">
        <h2 class="fe-apppage__state-title">{{ t('plugin.page_view.queued_title') }}</h2>
        <p class="fe-surface__text">{{ t('plugin.page_view.queued_text') }}</p>
        <div class="fe-apppage__state-actions">
          <a v-if="opsHref" class="fe-btn fe-btn--primary" :href="opsHref" data-testid="plugin-page-ops">
            {{ t('plugin.page_view.open_ops') }}
          </a>
          <a v-if="backHref" class="fe-btn" :href="backHref">{{ t('plugin.page_view.back') }}</a>
        </div>
      </div>

      <div v-else-if="state === 'done'" class="fe-apppage__state" data-testid="plugin-page-done">
        <h2 class="fe-apppage__state-title">{{ t('plugin.page_view.done_title') }}</h2>
        <p class="fe-surface__text">{{ t('plugin.page_view.done_text') }}</p>
        <div class="fe-apppage__state-actions">
          <a v-if="backHref" class="fe-btn fe-btn--primary" :href="backHref">{{ t('plugin.page_view.back') }}</a>
        </div>
      </div>

      <div v-else class="fe-apppage__state" data-testid="plugin-page-error">
        <h2 class="fe-apppage__state-title">{{ t('plugin.page_view.error_title') }}</h2>
        <p v-if="loadError" class="fe-surface__error">{{ loadError }}</p>
        <div class="fe-apppage__state-actions">
          <button type="button" class="fe-btn" @click="load()">{{ t('plugin.page_view.retry') }}</button>
          <a v-if="backHref" class="fe-btn" :href="backHref">{{ t('plugin.page_view.back') }}</a>
        </div>
      </div>
    </main>

    <footer v-if="state === 'surface' && conv.footer.value.length" class="fe-apppage__foot">
      <SurfaceFooterButtons :conv="conv" :locale="locale" testid-prefix="plugin-page" />
    </footer>
  </div>
</template>
