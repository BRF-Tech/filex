<script setup lang="ts">
/**
 * UploadProgress — renderless upload publisher (wiring:c3).
 *
 * Historically this drew its own floating bottom-right footer. Since the
 * unified operations center it renders NOTHING: it reconciles the host's
 * `jobs` list into the shared useOperations store, and the visible UI is
 * OperationsCenter's badge + panel. The original emit contract is kept —
 * cancel/dismiss (and the new retry) still bubble to the host, which owns
 * the actual upload machinery. Mount it exactly as before; just pass the
 * store via `center`.
 */
import { watch } from 'vue';
import type { UploadJob } from '../composables/useUploadChunked';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { OperationsStore, OperationStatus } from '../composables/useOperations';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  jobs: UploadJob[];
  /** The row's words while the server writes a file (`upload.saving`); the
   *  rest of an upload's strings render in OperationsCenter. */
  locale: LocaleCode;
  center: OperationsStore;
}>();

const { t } = useLocale(() => props.locale);

/**
 * Every byte is in filex and the server is writing the file to its storage:
 * the commit, then the queue's transfer. ⚠ That is not "100%": on a slow
 * uplink it is minutes, and the row read "X MB / X MB" over a full bar, the
 * badge "100%", for all of it.
 */
function saving(status: UploadJob['status']): boolean {
  return status === 'committing' || status === 'transferring';
}

const emit = defineEmits<{
  (e: 'cancel', job: UploadJob): void;
  (e: 'dismiss', job: UploadJob): void;
  (e: 'retry', job: UploadJob): void;
}>();

function mapStatus(s: UploadJob['status']): OperationStatus {
  if (s === 'done') return 'done';
  if (s === 'error') return 'error';
  if (s === 'aborted') return 'aborted';
  // pending | initializing | uploading | committing | transferring — all of
  // them are still in flight as far as the tray is concerned.
  return 'running';
}

watch(
  () => props.jobs,
  (jobs) => {
    props.center.sync(
      'upload',
      jobs.map((j) => ({
        input: {
          id: j.id,
          kind: 'upload' as const,
          name: j.file.name,
          percent: saving(j.status) ? null : j.totalBytes > 0 || j.status === 'done' ? j.percent : null,
          message: saving(j.status) ? t('upload.saving') : null,
          status: mapStatus(j.status),
          error: j.error ?? null,
          uploadedBytes: j.uploadedBytes,
          totalBytes: j.totalBytes,
          // Cancellable only while chunks are still moving: once the commit is
          // accepted the bytes are filex's and the ops worker owns the rest.
          cancellable: j.status === 'uploading' || j.status === 'initializing',
          retryable: j.status === 'error',
        },
        actions: {
          cancel: () => emit('cancel', j),
          dismiss: () => emit('dismiss', j),
          retry: () => emit('retry', j),
        },
      })),
    );
  },
  { immediate: true },
);
</script>

<template>
  <i v-if="false" />
</template>
