<script setup lang="ts">
/**
 * E2eRequestModal — "Request encryption…" (wiring:e2 policy). Where the
 * tenant's policy wants an administrator's approval before anything new is
 * encrypted (backend internal/e2epolicy, policy `approval`), the explorer
 * offers this instead of encrypting: from the New folder dialog (for the
 * folder the encrypted one would be made in), from a folder's menu and from a
 * file's menu. One dialog for all three.
 *
 * It asks one thing — why — and sends `POST /api/files/e2e/requests`. The
 * tenant's administrators are told (`e2e.request_created`) and answer under
 * Admin → Encryption; the person hears the answer (`e2e.request_decided`). The
 * server keeps one waiting request per person and folder or file: asking again
 * answers with the one already there (`created: false`), which the host says
 * as such, not as a second request.
 *
 * ⚠ The reason field carries a visible <label>: a placeholder vanishes the
 * moment anything is typed (web/tests/components/fieldLabels.test.ts).
 * ⚠ A refusal is said HERE, in the reader's language (lib/errorWords): the
 * dialog is what is on screen, and a toast behind it is a toast nobody reads.
 */
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { E2eRequestDto } from '../types/FileNode';
import { useLocale } from '../composables/useLocale';
import { sayFailure } from '../lib/errorWords';
import Modal from '../modals/Modal.vue';

/** The one call this dialog makes — `useFileApi().e2eRequest`, or a test's. */
interface RequestSender {
  e2eRequest(body: {
    path: string;
    kind: 'folder' | 'file';
    reason: string;
  }): Promise<{ request: E2eRequestDto; created: boolean }>;
}

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  api: RequestSender;
  /** Wire path (`<storage>://<rel>`): the folder, or the one file. */
  path: string;
  kind: 'folder' | 'file';
  /** What it is, as people read it — the dialog's title. */
  name: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'sent', answer: { request: E2eRequestDto; created: boolean }): void;
}>();

const { t } = useLocale(() => props.locale);

/** The server cuts a reason at this length; the box stops there instead. */
const REASON_MAX = 2000;

const reason = ref('');
const busy = ref(false);
const err = ref<string | null>(null);

watch(
  () => props.open,
  (v) => {
    if (v) {
      reason.value = '';
      err.value = null;
    }
  },
);

async function submit() {
  if (busy.value) return;
  const said = reason.value.trim();
  if (!said) {
    err.value = t('e2e.request.reason_required');
    return;
  }
  busy.value = true;
  err.value = null;
  try {
    const answer = await props.api.e2eRequest({ path: props.path, kind: props.kind, reason: said });
    emit('sent', answer);
  } catch (e) {
    // The sentence only: an administrator's second line (the raw refusal) has
    // no place in a dialog this small.
    err.value = sayFailure(e, t('e2e.request.failed'), { t }).text;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal
    :open="open"
    :locale="locale"
    :title="t('e2e.request.title', { name })"
    size="sm"
    :busy="busy"
    @close="emit('close')"
  >
    <form class="fe-e2e-form" data-testid="e2e-request" @submit.prevent="submit">
      <p class="fe-e2e-names__hint">{{ kind === 'file' ? t('e2e.request.lead_file') : t('e2e.request.lead') }}</p>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('e2e.request.reason') }}</span>
        <textarea
          v-model="reason"
          class="fe-input"
          rows="4"
          :maxlength="REASON_MAX"
          :placeholder="t('e2e.request.reason_placeholder')"
          :disabled="busy"
          data-testid="e2e-request-reason"
        ></textarea>
      </label>
      <p v-if="err" class="fe-form__error" role="alert" data-testid="e2e-request-error">{{ err }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="busy"
        :aria-busy="busy ? 'true' : undefined"
        data-testid="e2e-request-send"
        @click="submit"
      >
        {{ busy ? t('e2e.request.sending') : t('e2e.request.send') }}
      </button>
    </template>
  </Modal>
</template>
