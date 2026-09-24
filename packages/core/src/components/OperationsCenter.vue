<script setup lang="ts">
/**
 * OperationsCenter — the single visible surface for long-running work
 * (wiring:c3). Compact bottom-right badge (active count + aggregate
 * progress ring; hidden when idle) that expands into a panel: active
 * operations on top, session history below.
 *
 * Renders whatever the useOperations store aggregates — uploads, ops-queue
 * jobs, future convert/archive kinds — with one uniform row template.
 * Cancel / retry / dismiss route back through the store's action registry
 * to the owning publisher.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { Operation, OperationsStore } from '../composables/useOperations';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  center: OperationsStore;
  locale: LocaleCode;
  narrow?: boolean;
}>();

const { t, formatSize } = useLocale(() => props.locale);

const open = ref(false);
const rootEl = ref<HTMLElement | null>(null);

const active = computed(() => props.center.active.value);
const history = computed(() => props.center.history.value);
const hasError = computed(() => props.center.hasError.value);
const overall = computed(() => props.center.overallPercent.value);
const runningCount = computed(() => props.center.runningCount.value);

// Badge visible while anything is active; once the panel is open it stays
// up (showing history) until the user closes it. Idle + closed → hidden.
const visible = computed(() => active.value.length > 0 || (open.value && history.value.length > 0));

watch(visible, (v) => {
  if (!v) open.value = false;
});

// ---- progress ring ----
const RING_R = 8.5;
const RING_C = 2 * Math.PI * RING_R;
const ringOffset = computed(() => {
  const p = overall.value;
  if (p === null) return RING_C * 0.72; // spinner arc
  return RING_C * (1 - Math.min(100, Math.max(0, p)) / 100);
});
const ringSpin = computed(() => overall.value === null && runningCount.value > 0);

const pctText = computed(() => {
  if (runningCount.value === 0 || overall.value === null) return '';
  return t('opc.percent', { n: overall.value });
});

const badgeAria = computed(() => t('opc.aria_badge', { n: active.value.length }));

// ---- row helpers ----
const KIND_ICONS: Record<Operation['kind'], string> = {
  upload: 'M12 16V5M7 9.5 12 5l5 4.5M5 19h14',
  copy: 'M9 9h11v11H9zM5 15V4h11',
  move: 'M4 12h13M12 6l6 6-6 6',
  delete: 'M5 7h14M9 7V5h6v2M8 7l1 13h6l1-13',
  convert: 'M20 8A8 8 0 0 0 6 6L4 8M4 16a8 8 0 0 0 14 2l2-2M20 3v5h-5M4 21v-5h5',
  archive: 'M4 8V5h16v3zM5 8h14v12H5zM10 12h4',
  /* "Empty the trash" — the bin, with the lines of what is going. */
  trash: 'M5 7h14M9 7V5h6v2M8 7l1 13h6l1-13M10.5 11v5M13.5 11v5',
  /* An app-plugin job — the puzzle piece its menu row wears (lib/actionIcons). */
  plugin:
    'M9.5 4.5a2 2 0 1 1 4 0h3a1.5 1.5 0 0 1 1.5 1.5v3a2 2 0 1 1 0 4v3a1.5 1.5 0 0 1-1.5 1.5h-3a2 2 0 1 1-4 0h-3A1.5 1.5 0 0 1 5 16v-3a2 2 0 1 1 0-4V6a1.5 1.5 0 0 1 1.5-1.5z',
  'archive-create': 'M4 8V5h16v3zM5 8h14v12H5zM10 12h4',
  'archive-extract': 'M4 8V5h16v3zM5 8h14v12H5zM10 12h4',
};

function kindLabel(o: Operation): string {
  return t(`opc.kind.${o.kind}`);
}

function rowTitle(o: Operation): string {
  return o.name || kindLabel(o);
}

/**
 * The words for an app job's output mode, or `''`. ⚠ Only the two WRITING
 * modes get a chip: `none` is the normal case for a job that reports rather
 * than produces (a probe, a notification), and labelling it "no output"
 * would put a badge on almost every row for no information.
 */
function outputModeLabel(o: Operation): string {
  if (o.outputMode === 'sibling') return t('plugin.output.sibling');
  if (o.outputMode === 'version') return t('plugin.output.version');
  return '';
}

function outputModeTitle(o: Operation): string {
  if (o.outputMode === 'sibling') return t('plugin.output.sibling_title');
  if (o.outputMode === 'version') return t('plugin.output.version_title');
  return '';
}

function statusText(o: Operation): string {
  if (o.status === 'running') {
    if (o.cancelling) return t('opc.cancelling');
    if (o.queued) return t('opc.queued');
    // A plugin job says what it is doing in its own words (`job_progress`
    // message); that beats a bare count.
    if (o.message) return o.message;
    if (o.totalBytes && o.totalBytes > 0) {
      return `${formatSize(o.uploadedBytes ?? 0)} / ${formatSize(o.totalBytes)}`;
    }
    if ((o.kind === 'archive-create' || o.kind === 'archive-extract') && o.percent !== null) {
      return t('opc.percent', { n: o.percent });
    }
    if (o.totalCount && o.totalCount > 0) {
      return `${o.doneCount ?? 0}/${o.totalCount}`;
    }
    return t('opc.status.running');
  }
  if (o.status === 'done') return o.message || t('opc.status.done');
  if (o.status === 'aborted') return t('opc.status.aborted');
  return o.error || t('opc.status.error');
}

/** A finished job that produced files can be opened from its row. */
function canOpen(o: Operation): boolean {
  return o.status === 'done' && o.outputs.length > 0;
}

function chipText(o: Operation): string {
  return t(`opc.status.${o.status === 'running' ? 'running' : o.status}`);
}

// ---- outside click + Escape ----
function onDocPointerDown(ev: MouseEvent) {
  if (!open.value) return;
  const el = rootEl.value;
  if (el && ev.target instanceof Node && !el.contains(ev.target)) open.value = false;
}
function onDocKeydown(ev: KeyboardEvent) {
  if (open.value && ev.key === 'Escape') open.value = false;
}
onMounted(() => {
  document.addEventListener('mousedown', onDocPointerDown);
  document.addEventListener('keydown', onDocKeydown);
});
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocPointerDown);
  document.removeEventListener('keydown', onDocKeydown);
});
</script>

<template>
  <div
    v-if="visible"
    ref="rootEl"
    class="fe-opc"
    :class="{ 'fe-opc--narrow': narrow, 'fe-opc--open': open }"
  >
    <transition name="fe-opc-pop">
      <section v-if="open" class="fe-opc__panel" role="dialog" :aria-label="t('opc.title')">
        <header class="fe-opc__head">
          <strong>{{ t('opc.title') }}</strong>
          <button
            type="button"
            class="fe-opc__x"
            :aria-label="t('opc.close')"
            @click="open = false"
          >×</button>
        </header>
        <div class="fe-opc__body">
          <div v-if="active.length > 0" class="fe-opc__section" aria-live="polite">
            <h3 class="fe-opc__section-title">{{ t('opc.active') }}</h3>
            <ul class="fe-opc__list">
              <li
                v-for="o in active"
                :key="o.key"
                class="fe-opc__item"
                :class="`is-${o.status}`"
              >
                <svg
                  class="fe-opc__kicon"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.8"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                  focusable="false"
                >
                  <path :d="KIND_ICONS[o.kind]" />
                </svg>
                <div class="fe-opc__mid">
                  <div class="fe-opc__name" :title="rowTitle(o)">{{ rowTitle(o) }}</div>
                  <div class="fe-opc__sub">
                    <span class="fe-opc__kind">{{ kindLabel(o) }}</span>
                    <template v-if="outputModeLabel(o)">
                      <span class="fe-opc__dot" aria-hidden="true">·</span>
                      <span class="fe-opc__outmode" :title="outputModeTitle(o)" data-testid="opc-output-mode">
                        {{ outputModeLabel(o) }}
                      </span>
                    </template>
                    <span class="fe-opc__dot" aria-hidden="true">·</span>
                    <span class="fe-opc__state">{{ statusText(o) }}</span>
                  </div>
                  <!-- An administrator's second line (lib/errorWords): the raw
                       words behind the sentence above. Nobody else gets them. -->
                  <div
                    v-if="o.status === 'error' && o.errorDetail"
                    class="fe-opc__sub fe-opc__sub--detail"
                    data-testid="opc-error-detail"
                  >
                    {{ o.errorDetail }}
                  </div>
                  <div
                    v-if="o.status === 'running' && o.percent !== null"
                    class="fe-opc__bar"
                    role="progressbar"
                    :aria-valuenow="o.percent"
                    aria-valuemin="0"
                    aria-valuemax="100"
                  >
                    <div class="fe-opc__bar-fill" :style="{ width: o.percent + '%' }" />
                  </div>
                </div>
                <span
                  v-if="o.status === 'running' && o.percent === null && !o.queued"
                  class="fe-opc__spin"
                  aria-hidden="true"
                />
                <div class="fe-opc__acts">
                  <button
                    v-if="o.status === 'running' && o.cancellable"
                    type="button"
                    class="fe-opc__act"
                    @click="center.cancel(o.key)"
                  >{{ t('opc.cancel') }}</button>
                  <button
                    v-if="canOpen(o)"
                    type="button"
                    class="fe-opc__act"
                    data-testid="opc-open"
                    @click="center.open(o.key)"
                  >{{ t('opc.open') }}</button>
                  <button
                    v-if="o.status === 'error' && o.retryable"
                    type="button"
                    class="fe-opc__act fe-opc__act--retry"
                    @click="center.retry(o.key)"
                  >{{ t('opc.retry') }}</button>
                  <button
                    v-if="o.status !== 'running'"
                    type="button"
                    class="fe-opc__x"
                    :aria-label="t('opc.dismiss')"
                    @click="center.dismiss(o.key)"
                  >×</button>
                </div>
              </li>
            </ul>
          </div>

          <div v-if="history.length > 0" class="fe-opc__section">
            <h3 class="fe-opc__section-title">
              {{ t('opc.history') }}
              <button type="button" class="fe-opc__act" @click="center.clearHistory()">
                {{ t('opc.clear') }}
              </button>
            </h3>
            <ul class="fe-opc__list fe-opc__list--history">
              <li
                v-for="o in history"
                :key="'h-' + o.key"
                class="fe-opc__item fe-opc__item--history"
                :class="`is-${o.status}`"
              >
                <svg
                  class="fe-opc__kicon"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.8"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                  focusable="false"
                >
                  <path :d="KIND_ICONS[o.kind]" />
                </svg>
                <div class="fe-opc__mid">
                  <div class="fe-opc__name" :title="rowTitle(o)">{{ rowTitle(o) }}</div>
                  <!-- ⚠ The history row keeps the chip: in `version` mode
                       "Open" lands on the file the job started from, and
                       without these words that reads as "it did nothing". -->
                  <div v-if="outputModeLabel(o)" class="fe-opc__sub">
                    <span class="fe-opc__outmode" :title="outputModeTitle(o)" data-testid="opc-output-mode">
                      {{ outputModeLabel(o) }}
                    </span>
                  </div>
                  <div v-if="o.status === 'error' && o.error" class="fe-opc__sub fe-opc__sub--err">
                    {{ o.error }}
                  </div>
                  <!-- An administrator's second line: the raw words behind the
                       sentence (lib/errorWords). Nobody else is given them. -->
                  <div
                    v-if="o.status === 'error' && o.errorDetail"
                    class="fe-opc__sub fe-opc__sub--detail"
                    data-testid="opc-error-detail"
                  >
                    {{ o.errorDetail }}
                  </div>
                </div>
                <button
                  v-if="canOpen(o)"
                  type="button"
                  class="fe-opc__act"
                  @click="center.open(o.key)"
                >{{ t('opc.open') }}</button>
                <span class="fe-opc__chip" :class="`is-${o.status}`">{{ chipText(o) }}</span>
              </li>
            </ul>
          </div>

          <p v-if="active.length === 0 && history.length === 0" class="fe-opc__empty">
            {{ t('opc.empty') }}
          </p>
        </div>
      </section>
    </transition>

    <button
      type="button"
      class="fe-opc__badge"
      :class="{ 'is-error': hasError }"
      :aria-expanded="open"
      :aria-label="badgeAria"
      @click="open = !open"
    >
      <svg
        class="fe-opc__ring"
        :class="{ 'fe-opc__ring--spin': ringSpin }"
        viewBox="0 0 22 22"
        aria-hidden="true"
        focusable="false"
      >
        <circle class="fe-opc__ring-track" cx="11" cy="11" :r="RING_R" fill="none" stroke-width="2.5" />
        <circle
          class="fe-opc__ring-fill"
          cx="11"
          cy="11"
          :r="RING_R"
          fill="none"
          stroke-width="2.5"
          stroke-linecap="round"
          :stroke-dasharray="RING_C"
          :stroke-dashoffset="ringOffset"
          transform="rotate(-90 11 11)"
        />
      </svg>
      <span class="fe-opc__count">{{ active.length }}</span>
      <span v-if="pctText" class="fe-opc__pct">{{ pctText }}</span>
    </button>
  </div>
</template>
