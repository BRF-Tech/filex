<script setup lang="ts">
/**
 * The thumbnail settings (docs/thumbnails.md): whether folders show their
 * pictures (the folder card, and the list on a resting pointer), and the two
 * limits an SVG thumbnail is drawn under (GitHub #79), a size and a time.
 * One card, drawn in two places (Settings, and Admin → Tools → Thumbnail
 * repair), so the two can never disagree about what is in force. An SVG over
 * either limit keeps its icon and is listed, with the reason, on the repair
 * tab; raising a limit draws the files it had skipped.
 *
 * The settings are the instance's: a tenant administrator reads them here and
 * cannot change them (`editable: false`). The folder switch reaches an
 * explorer the next time it loads (capabilities `folder_previews`).
 */
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Image as ImageIcon, Save } from 'lucide-vue-next';

import { toolsApi, type ThumbSettings } from '@/api/tools';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';

const emit = defineEmits<{ (e: 'saved', v: ThumbSettings): void }>();

const { t } = useI18n();
const toast = useToastStore();

const loaded = ref<ThumbSettings | null>(null);
const folderPreviews = ref(true);
const maxMB = ref<number | string | null>(null);
const timeoutS = ref<number | string | null>(null);
/* Office documents drawn by OnlyOffice (0.50): the largest one sent, and how
 * many at once. */
const officeMB = ref<number | string | null>(null);
const officeSlots = ref<number | string | null>(null);
const saving = ref(false);
const loadError = ref('');

function whole(v: number | string | null, min: number, max: number): number | null {
  const n = typeof v === 'number' ? v : Number(String(v ?? '').trim());
  return Number.isInteger(n) && n >= min && n <= max ? n : null;
}

async function load() {
  try {
    const s = await toolsApi.thumbSettings();
    loaded.value = s;
    folderPreviews.value = s.folder_previews !== false;
    maxMB.value = s.svg_max_mb;
    timeoutS.value = s.svg_timeout_seconds;
    officeMB.value = s.office_max_mb;
    officeSlots.value = s.office_slots;
    loadError.value = '';
  } catch (e: unknown) {
    loadError.value = extractError(e, t('errors.actionFailed'));
  }
}

async function save() {
  const s = loaded.value;
  if (!s || saving.value) return;
  const mb = whole(maxMB.value, s.svg_max_mb_min, s.svg_max_mb_max);
  const sec = whole(timeoutS.value, s.svg_timeout_seconds_min, s.svg_timeout_seconds_max);
  const omb = whole(officeMB.value, s.office_max_mb_min, s.office_max_mb_max);
  const slots = whole(officeSlots.value, s.office_slots_min, s.office_slots_max);
  if (mb === null || sec === null || omb === null || slots === null) return;
  saving.value = true;
  try {
    const next = await toolsApi.saveThumbSettings({
      folder_previews: folderPreviews.value,
      svg_max_mb: mb,
      svg_timeout_seconds: sec,
      office_max_mb: omb,
      office_slots: slots,
    });
    loaded.value = next;
    toast.success(t('tools.thumbs.limits.saved'));
    emit('saved', next);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <form class="card card-body space-y-3" data-testid="thumb-limits" @submit.prevent="save">
    <h2 class="text-base font-semibold flex items-center gap-2">
      <ImageIcon class="h-4 w-4 text-brand-600 dark:text-brand-400" />
      {{ t('tools.thumbs.limits.title') }}
    </h2>
    <p v-if="loadError" class="text-sm text-rose-600 dark:text-rose-400">{{ loadError }}</p>
    <template v-if="loaded">
      <Toggle
        v-model="folderPreviews"
        name="thumb-folder-previews"
        :label="t('tools.thumbs.limits.folder_previews')"
        :description="t('tools.thumbs.limits.folder_previews_desc')"
        :disabled="!loaded.editable"
        data-testid="thumb-folder-previews"
      />
      <h3 class="pt-1 text-sm font-semibold">{{ t('tools.thumbs.limits.svg_title') }}</h3>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tools.thumbs.limits.desc') }}</p>
      <div class="grid gap-3 sm:grid-cols-2">
        <Input
          v-model="maxMB"
          type="number"
          :min="loaded.svg_max_mb_min"
          :max="loaded.svg_max_mb_max"
          :label="t('tools.thumbs.limits.size')"
          :hint="t('tools.thumbs.limits.size_hint', { min: loaded.svg_max_mb_min, max: loaded.svg_max_mb_max })"
          :error="whole(maxMB, loaded.svg_max_mb_min, loaded.svg_max_mb_max) === null ? t('tools.thumbs.limits.invalid', { min: loaded.svg_max_mb_min, max: loaded.svg_max_mb_max }) : null"
          :disabled="!loaded.editable"
          data-testid="thumb-limits-size"
        />
        <Input
          v-model="timeoutS"
          type="number"
          :min="loaded.svg_timeout_seconds_min"
          :max="loaded.svg_timeout_seconds_max"
          :label="t('tools.thumbs.limits.time')"
          :hint="t('tools.thumbs.limits.time_hint', { min: loaded.svg_timeout_seconds_min, max: loaded.svg_timeout_seconds_max })"
          :error="whole(timeoutS, loaded.svg_timeout_seconds_min, loaded.svg_timeout_seconds_max) === null ? t('tools.thumbs.limits.invalid', { min: loaded.svg_timeout_seconds_min, max: loaded.svg_timeout_seconds_max }) : null"
          :disabled="!loaded.editable"
          data-testid="thumb-limits-time"
        />
      </div>
      <h3 class="pt-1 text-sm font-semibold">{{ t('tools.thumbs.limits.office_title') }}</h3>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tools.thumbs.limits.office_desc') }}</p>
      <div class="grid gap-3 sm:grid-cols-2">
        <Input
          v-model="officeMB"
          type="number"
          :min="loaded.office_max_mb_min"
          :max="loaded.office_max_mb_max"
          :label="t('tools.thumbs.limits.office_size')"
          :hint="t('tools.thumbs.limits.office_size_hint', { min: loaded.office_max_mb_min, max: loaded.office_max_mb_max })"
          :error="whole(officeMB, loaded.office_max_mb_min, loaded.office_max_mb_max) === null ? t('tools.thumbs.limits.invalid', { min: loaded.office_max_mb_min, max: loaded.office_max_mb_max }) : null"
          :disabled="!loaded.editable"
          data-testid="thumb-limits-office-size"
        />
        <Input
          v-model="officeSlots"
          type="number"
          :min="loaded.office_slots_min"
          :max="loaded.office_slots_max"
          :label="t('tools.thumbs.limits.office_slots')"
          :hint="t('tools.thumbs.limits.office_slots_hint', { min: loaded.office_slots_min, max: loaded.office_slots_max })"
          :error="whole(officeSlots, loaded.office_slots_min, loaded.office_slots_max) === null ? t('tools.thumbs.limits.invalid', { min: loaded.office_slots_min, max: loaded.office_slots_max }) : null"
          :disabled="!loaded.editable"
          data-testid="thumb-limits-office-slots"
        />
      </div>
      <p v-if="!loaded.editable" class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="thumb-limits-readonly">
        {{ t('tools.thumbs.limits.readonly') }}
      </p>
      <div v-else class="flex justify-end">
        <Button type="submit" size="sm" :loading="saving" data-testid="thumb-limits-save">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
      </div>
    </template>
  </form>
</template>
