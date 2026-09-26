<script setup lang="ts">
/**
 * PublicRequestBody — the drop box a file request opens (`/d/<token>`).
 *
 * The person on this page is usually not a filex user and will never be one:
 * they were asked for a document and sent a link. So the body is one thing —
 * a place to put files — with the limits stated BEFORE they pick (what may
 * be sent, how big, how many are left), because a rejection after a two
 * minute upload is the version of this screen that gets abandoned.
 *
 * ⚠ Progress is real progress, from the upload itself (`usePublicRequest`
 * uses XHR for exactly this). A spinner that does not move is what makes
 * somebody reload the page halfway through a transfer.
 *
 * ⚠ "Blind drop": the server never tells this page what is already in the
 * folder, and this page never asks. A file request is permission to ADD.
 */
import { computed, ref } from 'vue';
import type { LocaleCode } from '../../types/ExplorerConfig';
import type { PublicRequestInfo } from '../../types/Public';
import { useLocale } from '../../composables/useLocale';

const props = defineProps<{
  info: PublicRequestInfo | null;
  uploads: Array<{ name: string; size: number; percent: number; state: string; error?: string }>;
  locale: LocaleCode | string;
  disabled?: boolean;
  /**
   * Leave the folder's name out of the body. The public page's heading is
   * already that name (`PublicLinkPage` titles a request by its folder), and
   * printing it again under itself read as a stutter.
   */
  hideFolder?: boolean;
}>();

const emit = defineEmits<{
  /** The files, and the name the person gave (`''` when the link does not ask). */
  (e: 'files', files: File[], name: string): void;
}>();

/**
 * The uploader's name, when the link asks for it (`limits.ask_name`).
 *
 * ⚠⚠ The owner's "Ask uploader name" is ON by default, and this page never
 * asked (QA, 2026-09-21): the setting did nothing, and every submission folder
 * arrived as `<date>_anon`. Optional, as on the no-JavaScript page — a name
 * nobody wants to give is not a reason to refuse their document — and ABOVE
 * the drop area, because dropping a file sends it.
 */
const uploaderName = ref('');
const askName = computed(() => props.info?.limits?.ask_name === true);
function sendFiles(files: File[]): void {
  emit('files', files, askName.value ? uploaderName.value.trim() : '');
}

const { t, formatSize } = useLocale(() => props.locale);

const input = ref<HTMLInputElement | null>(null);
const over = ref(false);

const limitsOf = computed(() => props.info?.limits ?? {});

const accept = computed(() => {
  const list = limitsOf.value.allowed_ext ?? [];
  return list.length ? list.map((e) => `.${e.replace(/^\./, '')}`).join(',') : undefined;
});

const limits = computed(() => {
  const out: string[] = [];
  const mb = limitsOf.value.max_file_size_mb;
  // ⚠ The server states MEGABYTES; the screen says it in the same units the
  // rest of the product uses, so "5 MB" here and "5 MB" in the explorer are
  // the same number rather than two roundings of it.
  if (mb && mb > 0) out.push(t('public.max_size', { size: formatSize(mb * 1_000_000) }));
  const ext = limitsOf.value.allowed_ext ?? [];
  if (ext.length) out.push(t('public.accepts', { list: ext.map((e) => e.toUpperCase()).join(', ') }));
  const left = props.info?.uploads_left;
  if (left !== null && left !== undefined && Number.isFinite(left)) out.push(t('public.files_left', { n: left }));
  return out;
});

/** No room left: the box says so instead of taking a file the server will refuse. */
const full = computed(() => {
  const left = props.info?.uploads_left;
  return left !== null && left !== undefined && Number.isFinite(left) && left <= 0;
});

function pick(): void {
  if (props.disabled || full.value) return;
  input.value?.click();
}

function onPicked(e: Event): void {
  const el = e.target as HTMLInputElement;
  const files = Array.from(el.files ?? []);
  if (files.length) sendFiles(files);
  // Cleared so choosing the SAME file twice still fires a change event.
  el.value = '';
}

function onDrop(e: DragEvent): void {
  over.value = false;
  if (props.disabled || full.value) return;
  const files = Array.from(e.dataTransfer?.files ?? []);
  if (files.length) sendFiles(files);
}
</script>

<template>
  <div class="fe-pdrop" data-testid="public-request">
    <p v-if="info?.folder && !hideFolder" class="fe-surface__text" data-testid="public-request-folder">
      <strong>{{ info?.folder }}</strong>
    </p>

    <!-- ⚠ What this page IS, said before anything is asked of the person.
         The Go-rendered drop page opened on this sentence and the unified
         shell dropped it, so a stranger arrived at a dashed rectangle with no
         statement of what happens to what they put in it — and none of the
         reassurance that they are not being shown somebody's folder. -->
    <p class="fe-surface__text fe-surface__text--muted" data-testid="public-request-lead">
      {{ t('public.drop_sub') }}
    </p>

    <label v-if="askName && !full" class="fe-pdrop__who" data-testid="public-request-name">
      <span class="fe-pdrop__wholabel">{{ t('public.your_name') }}</span>
      <input
        v-model="uploaderName"
        class="fe-pdrop__whoinput"
        type="text"
        maxlength="60"
        autocomplete="name"
        :placeholder="t('public.your_name_ph')"
        :disabled="disabled"
        data-testid="public-request-name-input"
      />
    </label>

    <div
      class="fe-pdrop__zone"
      :class="{ 'is-over': over, 'is-disabled': disabled || full }"
      role="button"
      tabindex="0"
      data-testid="public-request-drop"
      @click="pick"
      @keydown.enter.prevent="pick"
      @keydown.space.prevent="pick"
      @dragover.prevent="over = true"
      @dragleave="over = false"
      @drop.prevent="onDrop"
    >
      <svg
        class="fe-pdrop__icon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M4 14.9A7 7 0 1 1 15.7 8h1.8a4.5 4.5 0 0 1 2.5 8.2" />
        <path d="M12 12v9" />
        <path d="m16 16-4-4-4 4" />
      </svg>
      <p class="fe-pdrop__lead">{{ full ? t('public.request_full') : t('public.drop_here') }}</p>
      <p v-if="!full" class="fe-surface__text fe-surface__text--muted">{{ t('public.or_choose') }}</p>
      <input
        ref="input"
        class="fe-pdrop__input"
        type="file"
        multiple
        :accept="accept"
        :disabled="disabled || full"
        data-testid="public-request-input"
        @change="onPicked"
      />
    </div>

    <!-- ⚠ The limits are separated by a dot, as every other list of facts on
         this page is. Butted against each other they read as one sentence:
         "At most 20 MB per file Accepted: PDF, PNG". -->
    <p v-if="limits.length" class="fe-ppage__meta" data-testid="public-request-limits">
      <template v-for="(l, i) in limits" :key="i">
        <span v-if="i" aria-hidden="true">·</span>
        <span>{{ l }}</span>
      </template>
    </p>

    <ul v-if="uploads.length" class="fe-pdrop__list" data-testid="public-request-uploads">
      <li v-for="(u, i) in uploads" :key="`${u.name}-${i}`" class="fe-pdrop__item" :data-state="u.state">
        <span class="fe-pdrop__name"><bdi>{{ u.name }}</bdi></span>
        <span class="fe-ppage__filesize">{{ formatSize(u.size) }}</span>
        <progress
          v-if="u.state === 'sending'"
          class="fe-pdrop__bar"
          :max="100"
          :value="u.percent >= 0 ? u.percent : undefined"
        ></progress>
        <!-- Every byte is sent; the server is writing it to the storage. The
             bar runs without a value: it is not stuck at 100%. -->
        <template v-else-if="u.state === 'saving'">
          <progress class="fe-pdrop__bar" :max="100"></progress>
          <span class="fe-pdrop__saving" role="status">{{ t('public.upload_saving') }}</span>
        </template>
        <span v-else-if="u.state === 'done'" class="fe-pdrop__ok">{{ t('public.upload_done') }}</span>
        <span v-else-if="u.state === 'unconfirmed'" class="fe-pdrop__unconfirmed" role="status">{{
          u.error || t('public.upload_unanswered')
        }}</span>
        <span v-else class="fe-surface__error">{{ u.error || t('public.upload_failed') }}</span>
      </li>
    </ul>
  </div>
</template>
