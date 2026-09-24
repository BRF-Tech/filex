<script setup lang="ts">
/**
 * SurfacePreview — a plugin surface's `preview` node: the explorer's own
 * look at a storage file. The bytes come through `api.fetchBlob`, the same
 * authenticated preview fetch every rich viewer uses (bearer / CSRF /
 * credentials mode all honoured), so an embed on another origin sees the
 * picture too. Pictures, video and audio are shown as themselves; anything
 * else gets the listing's own type tile and the name.
 *
 * ⚠ No `<img src="/api/…">` for a PATH: a plain URL cannot carry the auth
 * header and resolves against the HOST page's origin in an embed (see
 * useThumbs). A public page's exposed copy (`ref`, M3) is the exception by
 * design: there is no auth header, the PIN cookie rides with the same-origin
 * URL the host's `fileUrl` resolver hands out, and the browser's own
 * viewer draws it (`<img>` for a picture, `<embed>` for a PDF or anything
 * else) — so the page never buffers a document in memory.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import type { FileApi } from '../../../composables/useFileApi';
import type { FileRefResolver } from '../../../types/Plugins';
import { useLocale } from '../../../composables/useLocale';
import { labelOfWire } from '../../../lib/destinationTree';
import { fileIconTile } from '../../../lib/fileIcons';

const props = defineProps<{
  /** Adapter-qualified path of a storage file. */
  path: string;
  /** A public page's exposed copy (`pub:N`), shown from `fileUrl(ref)`. */
  fileRef?: string;
  fileUrl?: FileRefResolver;
  locale: LocaleCode;
  /** Absent: the node says the preview is unavailable. */
  api?: Pick<FileApi, 'fetchBlob'>;
}>();

const { t } = useLocale(() => props.locale);

const url = ref<string | null>(null);
const mime = ref('');
const state = ref<'loading' | 'ok' | 'error'>('loading');
let seq = 0;

/** The exposed copy, when the node names one and the host can resolve it. */
const exposed = computed(() => (props.fileRef && props.fileUrl ? props.fileUrl(props.fileRef) : null));

const name = computed(() => exposed.value?.name || labelOfWire(props.path, '') || props.path || props.fileRef || '');
const kind = computed<'image' | 'video' | 'audio' | 'other'>(() => {
  const m = mime.value;
  if (m.startsWith('image/')) return 'image';
  if (m.startsWith('video/')) return 'video';
  if (m.startsWith('audio/')) return 'audio';
  return 'other';
});
const tile = computed(() => fileIconTile({ type: 'file', basename: name.value }));

function release() {
  // Only a blob URL this node minted is ours to revoke; an exposed copy's
  // URL is the server's.
  if (url.value && url.value.startsWith('blob:')) URL.revokeObjectURL(url.value);
  url.value = null;
}

async function load() {
  const mine = ++seq;
  release();
  mime.value = '';
  if (props.fileRef) {
    if (!exposed.value) {
      state.value = 'error';
      return;
    }
    url.value = exposed.value.url;
    mime.value = exposed.value.mime || '';
    state.value = 'ok';
    return;
  }
  if (!props.api || !props.path) {
    state.value = 'error';
    return;
  }
  state.value = 'loading';
  try {
    const r = await props.api.fetchBlob(props.path);
    if (mine !== seq) {
      URL.revokeObjectURL(r.url);
      return;
    }
    url.value = r.url;
    mime.value = r.mime || '';
    state.value = 'ok';
  } catch {
    if (mine !== seq) return;
    state.value = 'error';
  }
}

watch([() => props.path, () => props.fileRef], () => void load(), { immediate: true });
onBeforeUnmount(() => {
  seq++;
  release();
});
</script>

<template>
  <figure class="fe-spreview" data-testid="surface-preview" :data-state="state">
    <p v-if="state === 'loading'" class="fe-surface__text fe-surface__text--muted">{{ t('plugin.view.loading') }}</p>
    <p v-else-if="state === 'error'" class="fe-surface__text fe-surface__text--muted">{{ t('plugin.view.preview_failed') }}</p>
    <template v-else>
      <img v-if="kind === 'image' && url" class="fe-spreview__media" :src="url" :alt="name" />
      <video v-else-if="kind === 'video' && url" class="fe-spreview__media" :src="url" controls preload="metadata"></video>
      <audio v-else-if="kind === 'audio' && url" class="fe-spreview__audio" :src="url" controls preload="metadata"></audio>
      <embed v-else-if="fileRef && url" class="fe-spreview__media fe-spreview__embed" :src="url" :type="mime || undefined" />
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
      <span v-else class="fe-spreview__tile" aria-hidden="true" v-html="tile"></span>
    </template>
    <figcaption class="fe-spreview__name" :title="path">{{ name }}</figcaption>
  </figure>
</template>
