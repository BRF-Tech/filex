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
 *     page says the job is queued, FOLLOWS it, and says how it ended - or,
 *     when the job's result names a screen on what it made (`surface.open`),
 *     goes there in this tab (filex #78: a job carries its person on to
 *     what it made).
 *   • DONE — the plugin ended the conversation (`done`), with nothing queued.
 *
 * ⚠ It is in the package, not in the admin SPA: fm.example.com, the desktop shell
 * and the embeds all reach `page` views through the same explorer, so a copy
 * per host would be a copy per host to keep in step.
 *
 * v4 — a view that is the app's OWN interface (its row carries `ui`) is drawn
 * by AppFrame instead of a conversation: this file asks the actions list
 * which it is, so every host (the new-tab page, the app's home page, the
 * admin panel's Apps section) gets the frame without knowing about it.
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
import { isPagePlacement } from '../../lib/pluginPage';
import { opFailure } from '../../lib/errorWords';
import { jobOpenOf } from '../../lib/jobOpen';
import { usePendingOps, type PendingOp } from '../../composables/usePendingOps';
import AppFrame from './AppFrame.vue';
import type { PluginUIRef, SurfaceOpenRequest } from '../../types/Plugins';
import type { ExplorerConfig } from '../../types/ExplorerConfig';

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

/**
 * Where the job this page queued has got to. `running` until it ends; then
 * `done`, `failed`, or `opening` (the job asked for a screen on what it
 * made, and the page is on its way there).
 *
 * ⚠⚠ The page FOLLOWS its own job (filex #78). It used to stop at "the job
 * is queued … you will be told when it lands", and nothing told anybody: the
 * explorer in the other tab announces only the jobs IT queued, so a job born
 * here ended in silence. The signing app's "Convert to PDF" (filex-sign 0.2;
 * the app signs PDFs only since 0.3) was the case that read as a broken
 * wizard: the PDF landed, the page still said "queued", and the person had to
 * find the PDF and ask for signatures again. Now the page
 * says how the job ended in the person's words, and when the job's result
 * names a screen on one of its outputs (`surface.open`), it goes there.
 *
 * ⚠ The card keeps its `plugin-page-queued` place for the job's whole life:
 * it is the card about THE job, whose title and words change as the job does.
 */
type JobPhase = 'running' | 'done' | 'failed' | 'opening';
const jobPhase = ref<JobPhase>('running');
const jobWords = ref('');

/** The views of this app that are whole pages, learned from the actions list
 *  (a `page` view is listed only through an action that opens it). */
const pageViews = ref<Set<string>>(new Set());

/**
 * Go where a finished job sent the person - in THIS tab. The page's work is
 * done, and a tab opened by a job's end rather than by a click is a pop-up
 * the browser blocks.
 */
function followJobOpen(req: SurfaceOpenRequest): boolean {
  const target = openTargetFor(req, {
    plugin: props.plugin,
    base: props.mountBase,
    placementOf: (id) => (pageViews.value.has(id) ? 'page' : undefined),
  });
  if (!target.href) return false;
  try {
    window.location.assign(target.href);
    return true;
  } catch {
    return false;
  }
}

function onJobSettled(op: PendingOp): void {
  if (op.status === 'done') {
    jobWords.value = op.message ?? '';
    const go = jobOpenOf(op);
    if (go && followJobOpen(go.open)) {
      jobPhase.value = 'opening';
      return;
    }
    jobPhase.value = 'done';
    return;
  }
  jobWords.value = opFailure(op, t).text;
  jobPhase.value = 'failed';
}

// The same follower the explorer's tray uses: one poll of `/api/files/ops`,
// announcing only what was registered here.
const jobs = usePendingOps({} as ExplorerConfig, props.api, { onSettled: onJobSettled });

/** Follow the job this page queued, where the client can ask the queue. */
function followJob(op: Record<string, unknown>): void {
  jobPhase.value = 'running';
  jobWords.value = '';
  if (!props.api.endpoints?.opsList) return;
  jobs.register(op);
}

const jobTitle = computed(() => {
  switch (jobPhase.value) {
    case 'done':
      return t('plugin.page_view.finished_title');
    case 'failed':
      return t('plugin.page_view.failed_title');
    case 'opening':
      return t('plugin.page_view.opening_title');
    default:
      return t('plugin.page_view.queued_title');
  }
});
const jobText = computed(() => {
  if (jobPhase.value === 'running') return t('plugin.page_view.queued_text');
  if (jobPhase.value === 'done') return jobWords.value || t('plugin.page_view.finished_text');
  return jobWords.value;
});

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
      followJob(op);
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

/**
 * v4 — the app's own interface for this view, when it is one: found in the
 * actions list (a `home`/`viewer` row, or an action whose view it is).
 */
const uiRef = ref<PluginUIRef | null>(null);
const uiPlacement = ref<'page' | 'home'>('page');
const uiChecked = ref(false);
const uiFiles = computed(() =>
  props.path ? [{ path: props.path, name: props.path.split('/').pop() || props.path }] : [],
);

async function findUI(): Promise<void> {
  try {
    const list = await props.api.pluginActions();
    // Which of this app's screens are whole pages: where a finished job's
    // `open` lands on its own address rather than through the explorer.
    pageViews.value = new Set(
      (list.actions ?? [])
        .filter((a) => a.plugin === props.plugin && a.view && isPagePlacement(a.view_placement))
        .map((a) => a.view as string),
    );
    const row = list.views.find((v) => v.plugin === props.plugin && v.id === props.view && v.ui);
    if (row?.ui) {
      uiRef.value = row.ui;
      uiPlacement.value = row.placement === 'home' ? 'home' : 'page';
      return;
    }
    const act = list.actions.find((a) => a.plugin === props.plugin && a.view === props.view && a.ui);
    if (act?.ui) uiRef.value = act.ui;
  } catch {
    /* no list: the conversation below says what the server says */
  } finally {
    uiChecked.value = true;
  }
}

onMounted(async () => {
  await findUI();
  if (!uiRef.value) void load();
});
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

    <main v-if="uiRef" class="fe-apppage__body fe-apppage__body--ui">
      <AppFrame
        :api="api"
        :app="plugin"
        :version="uiRef.version"
        :view="view"
        :placement="uiPlacement"
        :ui="uiRef"
        :files="uiFiles"
        :storages="storages"
        :start-at="startAt"
        :locale="locale"
        :theme="theme"
        :title="title"
        @toast="(m) => (toast = m.text)"
        @op="(op) => emit('op', op)"
      />
      <p v-if="toast" class="fe-surface__text" role="status">{{ toast }}</p>
    </main>
    <main v-else-if="!uiChecked" class="fe-apppage__body">
      <p class="fe-surface__text fe-surface__text--muted" data-testid="plugin-page-loading">
        {{ t('plugin.view.loading') }}
      </p>
    </main>
    <main v-else class="fe-apppage__body">
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

      <div
        v-else-if="state === 'queued'"
        class="fe-apppage__state"
        data-testid="plugin-page-queued"
        :data-job="jobPhase"
      >
        <h2 class="fe-apppage__state-title" data-testid="plugin-page-job-title">{{ jobTitle }}</h2>
        <p
          :class="jobPhase === 'failed' ? 'fe-surface__error' : 'fe-surface__text'"
          :role="jobPhase === 'failed' ? 'alert' : 'status'"
          data-testid="plugin-page-job-text"
        >
          {{ jobText }}
        </p>
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
