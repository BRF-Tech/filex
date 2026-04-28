<script setup lang="ts">
/**
 * UploadProgress — floating footer showing active + recently-done uploads.
 */
import { computed } from 'vue';
import type { UploadJob } from '../composables/useUploadChunked';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  jobs: UploadJob[];
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'cancel', job: UploadJob): void;
  (e: 'dismiss', job: UploadJob): void;
}>();

const { t, formatSize } = useLocale(() => props.locale);

const visible = computed(() => props.jobs);

function statusLabel(j: UploadJob): string {
  switch (j.status) {
    case 'done':
      return t('upload.done');
    case 'error':
      return `${t('upload.error')}: ${j.error ?? ''}`;
    case 'aborted':
      return t('upload.aborted');
    default:
      return t('upload.progress');
  }
}
</script>

<template>
  <transition name="fe-upload-slide">
    <div v-if="visible.length > 0" class="fe-upload">
      <div class="fe-upload__header">
        <strong>{{ t('upload.progress') }}</strong>
        <span class="fe-upload__count">{{ visible.length }}</span>
      </div>
      <ul class="fe-upload__list">
        <li
          v-for="j in visible"
          :key="j.id"
          class="fe-upload__item"
          :class="`is-${j.status}`"
        >
          <div class="fe-upload__name" :title="j.file.name">{{ j.file.name }}</div>
          <div class="fe-upload__bar" :aria-valuenow="j.percent" role="progressbar">
            <div class="fe-upload__bar-fill" :style="{ width: j.percent + '%' }" />
          </div>
          <div class="fe-upload__meta">
            <span>{{ statusLabel(j) }}</span>
            <span>{{ formatSize(j.uploadedBytes) }} / {{ formatSize(j.totalBytes) }}</span>
            <button
              v-if="j.status === 'uploading' || j.status === 'initializing'"
              type="button"
              class="fe-upload__cancel"
              @click="emit('cancel', j)"
            >
              {{ t('upload.cancel') }}
            </button>
            <button
              v-else
              type="button"
              class="fe-upload__cancel"
              @click="emit('dismiss', j)"
            >×</button>
          </div>
        </li>
      </ul>
    </div>
  </transition>
</template>
