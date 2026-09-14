<script setup lang="ts">
/**
 * TimeZoneDialog — zaman:z3, the embed's own time-zone setting.
 *
 * The owner, 2026-09-14: *"gömme için ayar bırakmamız lazım; basitçe tarayıcıda
 * tutulan bir ayar olması lazım."* An embed has no user-settings dialog — its
 * "⋯" menu (theme, density, shortcuts) is where its settings live — so the menu
 * opens this, and a pick is stored in THIS browser (`lib/timezone`, tier
 * `viewer`). Nothing is sent anywhere: an embed's credential may be shared by
 * every visitor, and there is no account of the visitor's to write to.
 *
 * ⚠ The picker is `TimeZonePicker`, the same component the web app's settings
 * modal mounts. Not a second one.
 *
 * ⚠ The first row is "use the default", and it names what the default IS on
 * this page — the zone the embedder configured, the account's zone, or this
 * device's — by asking the resolver with the viewer tier blanked. A reset
 * whose outcome you have to guess is not a reset.
 */
import { computed, inject, onBeforeUnmount, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { formatInstant, useLocale } from '../composables/useLocale';
import {
  EXPLORER_CLOCK,
  deviceTimeZone,
  resolveTimeZone,
  resolvedTimeZoneFor,
  setViewerTimeZone,
  timeZoneSourcesFor,
  viewerTimeZone,
} from '../lib/timezone';
import Modal from '../modals/Modal.vue';
import TimeZonePicker from './TimeZonePicker.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
}>();

const emit = defineEmits<{ (e: 'close'): void }>();

const { t } = useLocale(() => props.locale);

const inputId = `fe-tzdialog-${Math.random().toString(36).slice(2, 9)}`;

// This explorer's clock, not the page's newest (lib/timezone, timeZoneSourcesFor).
const clock = inject(EXPLORER_CLOCK, undefined);
const fallback = computed(() => resolveTimeZone({ ...timeZoneSourcesFor(clock), viewer: '' }));
const inForce = computed(() => resolvedTimeZoneFor(clock));
const inForceZone = computed(() => inForce.value.zone ?? deviceTimeZone());

/** "Right now in Asia/Tokyo: 14:05:09" — the fastest way to see a wrong pick. */
const now = ref('');
let timer: ReturnType<typeof setInterval> | undefined;
function refresh(): void {
  // Through the shared formatter, which reads the RESOLVED zone — the readout
  // must say what every date on the page is actually drawn in, not what this
  // dialog thinks it chose.
  now.value = formatInstant(
    new Date(),
    props.locale,
    {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    },
    clock,
  );
}
watch(
  () => props.open,
  (open) => {
    if (timer) clearInterval(timer);
    timer = undefined;
    if (!open) return;
    refresh();
    timer = setInterval(refresh, 1000);
  },
  { immediate: true },
);
watch(inForce, refresh);
onBeforeUnmount(() => {
  if (timer) clearInterval(timer);
});
</script>

<template>
  <Modal :open="open" :title="t('tz.title')" size="md" :theme="theme" @close="emit('close')">
    <div class="fe-tzdialog" data-testid="fe-tz-dialog">
      <label class="fe-tzdialog__label" :for="inputId">{{ t('tz.label') }}</label>
      <TimeZonePicker
        :input-id="inputId"
        :locale="locale"
        :model-value="viewerTimeZone()"
        :fallback="fallback"
        :label="t('tz.label')"
        testid="fe-tz"
        @update:model-value="setViewerTimeZone"
      />
      <p class="fe-tzdialog__hint">{{ t('tz.hint') }}</p>
      <p class="fe-tzdialog__now" data-testid="fe-tz-now" :data-tier="inForce.tier">
        {{ t('tz.now', { zone: inForceZone, time: now }) }}
      </p>
    </div>
  </Modal>
</template>

<!-- Not scoped — see TimeZonePicker.vue / ThemePalette.vue. -->
<style>
/* Tall enough that the zone list opens downward inside the dialog instead of
   being clipped by the body's own scroll box. */
.fe-tzdialog {
  display: flex;
  flex-direction: column;
  gap: var(--fe-gap-sm);
  min-height: 340px;
}
.fe-tzdialog__label {
  font-size: var(--fe-text-sm);
  font-weight: 500;
  color: var(--fe-text);
}
.fe-tzdialog__hint,
.fe-tzdialog__now {
  margin: 0;
  font-size: var(--fe-text-xs);
  line-height: 1.45;
  color: var(--fe-text-muted);
}
.fe-tzdialog__now {
  font-size: var(--fe-text-sm);
  font-variant-numeric: tabular-nums;
}
</style>
