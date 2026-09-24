<script setup lang="ts">
/**
 * SurfaceRenderer — draws a plugin's declarative screen with filex's own
 * components. The plugin sends data (`wire.Surface.nodes`); nothing it sends
 * is ever markup, a script or a frame — a node type this file does not know
 * is shown AS unknown, not skipped and not interpreted.
 *
 * M1 drew `text` (and layout `row` / `divider`). M2 adds the rest of the
 * v1 catalogue — form, steps, list, progress, people-picker, pin-input,
 * file-chooser, preview — one `<template v-else-if>` each, each a small
 * component under `./nodes/`. M3 (the sign track) adds `signature-pad` and
 * `pdf-fields`, and lets `preview` show a public page's exposed copy by
 * `ref` through the host's `fileUrl` resolver.
 *
 * Values: nodes that hold one read and write the ONE map the host owns
 * (`values`, v-model style), keyed by node id — a `form`'s fields flat by
 * field key. Events up: `update:values` on any edit, `change` on a form
 * edit (the host debounces it into the contract's `change` event), `action`
 * for a list row's button. Nested rows forward all three.
 */
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import type {
  FileChooserNodeProps,
  FileRefResolver,
  FormNodeProps,
  ListNodeProps,
  PdfFieldsNodeProps,
  PeoplePickerNodeProps,
  PinInputNodeProps,
  PluginPerson,
  PluginText,
  PreviewNodeProps,
  ProgressNodeProps,
  SignaturePadNodeProps,
  StepsNodeProps,
  SurfaceNode,
  TextNodeProps,
} from '../../types/Plugins';
import { isSignatureValue } from '../../lib/signaturePad';
import { useLocale } from '../../composables/useLocale';
import { labelOf } from '../../lib/pluginLabel';
import SurfaceForm from './nodes/SurfaceForm.vue';
import SurfaceSteps from './nodes/SurfaceSteps.vue';
import SurfaceList from './nodes/SurfaceList.vue';
import SurfaceProgress from './nodes/SurfaceProgress.vue';
import SurfacePeoplePicker from './nodes/SurfacePeoplePicker.vue';
import SurfacePinInput from './nodes/SurfacePinInput.vue';
import SurfaceFileChooser from './nodes/SurfaceFileChooser.vue';
import SurfacePreview from './nodes/SurfacePreview.vue';
import SurfaceSignaturePad from './nodes/SurfaceSignaturePad.vue';
import SurfacePdfFields from './nodes/SurfacePdfFields.vue';

/** What the value-holding and file-reading nodes need from the explorer's API. */
export type SurfaceApi = Pick<FileApi, 'index' | 'pluginUsers' | 'fetchBlob'>;

const props = defineProps<{
  nodes: SurfaceNode[];
  locale: LocaleCode;
  theme?: ThemeMode;
  /** Field-level errors the surface carries, keyed by node id / field key. */
  errors?: Record<string, PluginText>;
  /** The surface's values (node id → value; form fields flat). */
  values?: Record<string, unknown>;
  /** Absent in a bare render: choosers and previews say so instead of failing. */
  api?: SurfaceApi;
  /** The plugin the surface belongs to (the people-picker's lookup asks for it). */
  plugin?: string;
  /** Storage names a file-chooser may span; where its picker opens. */
  storages?: string[];
  startAt?: string;
  /** The host is talking to the server: inputs and buttons stay put. */
  disabled?: boolean;
  /** A public page's exposed copies (`pub:N`) → a URL the browser may load (M3). */
  fileUrl?: FileRefResolver;
  /** Where pdf.js fetches its worker from; the pinned CDN copy when absent. */
  pdfWorkerUrl?: string | null;
  /**
   * v3 §2.1 — field keys a refused submit is waiting for. Passed down to the
   * form so the star and the "required" line land on the FIELD, not in a
   * banner the person has to map back onto a box themselves.
   */
  invalidKeys?: string[];
  /**
   * v3 §3.2 — `page` means this surface owns the viewport, so a `pdf-fields`
   * node fits the document to the screen instead of to the text column. The
   * frame says which; a node never measures the window itself.
   */
  layout?: 'inline' | 'page';
  /** The box a `page` layout's document may fill (a CSS length). */
  pageHeight?: string;
}>();

const emit = defineEmits<{
  (e: 'update:values', v: Record<string, unknown>): void;
  /** A form field was edited (the host debounces into `event: "change"`). */
  (e: 'change'): void;
  (e: 'action', p: { action_id: string; row_id: string }): void;
}>();

const { t } = useLocale(() => props.locale);

const vals = () => props.values ?? {};

function set(id: string, v: unknown) {
  emit('update:values', { ...vals(), [id]: v });
}

function onFormValues(v: Record<string, unknown>) {
  emit('update:values', v);
  emit('change');
}

/** A `pdf-fields` edit is authoring: it posts `change` like a form does. Filling rides on the next submit. */
function onPdfValue(n: SurfaceNode, v: unknown) {
  set(n.id as string, v);
  if (p<PdfFieldsNodeProps>(n).mode !== 'fill') emit('change');
}

function p<T>(n: SurfaceNode): T {
  return (n.props ?? {}) as unknown as T;
}

/** A `text` node's words: `props.text` (a Text or a string), else `props.value`. */
function textOf(n: SurfaceNode): string {
  const tp = p<TextNodeProps>(n);
  return labelOf(tp.text ?? tp.value ?? '', props.locale);
}

function toneOf(n: SurfaceNode): string {
  const tone = String(p<TextNodeProps>(n).tone ?? '');
  return tone === 'muted' || tone === 'danger' || tone === 'info' ? `fe-surface__text--${tone}` : '';
}

function errorOf(n: SurfaceNode): string {
  if (!n.id || !props.errors) return '';
  return labelOf(props.errors[n.id], props.locale);
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function people(v: unknown): PluginPerson[] {
  return Array.isArray(v) ? (v as PluginPerson[]) : [];
}
</script>

<template>
  <div class="fe-surface">
    <template v-for="(n, i) in nodes" :key="n.id || i">
      <p
        v-if="n.type === 'text'"
        class="fe-surface__text"
        :class="[toneOf(n), p<TextNodeProps>(n).heading ? 'fe-surface__text--heading' : '']"
        :data-node="n.id || undefined"
      >{{ textOf(n) }}</p>
      <hr v-else-if="n.type === 'divider'" class="fe-surface__divider" />
      <div v-else-if="n.type === 'row'" class="fe-surface__row">
        <SurfaceRenderer
          :nodes="n.children ?? []"
          :locale="locale"
          :theme="theme"
          :errors="errors"
          :values="values"
          :api="api"
          :plugin="plugin"
          :storages="storages"
          :start-at="startAt"
          :disabled="disabled"
          :file-url="fileUrl"
          :pdf-worker-url="pdfWorkerUrl"
          :invalid-keys="invalidKeys"
          :layout="layout"
          :page-height="pageHeight"
          @update:values="(v) => emit('update:values', v)"
          @change="emit('change')"
          @action="(a) => emit('action', a)"
        />
      </div>
      <SurfaceForm
        v-else-if="n.type === 'form'"
        :fields="p<FormNodeProps>(n).fields ?? []"
        :values="vals()"
        :locale="locale"
        :errors="errors"
        :invalid="invalidKeys"
        :disabled="disabled"
        @update:values="onFormValues"
      />
      <SurfaceSteps v-else-if="n.type === 'steps'" :items="p<StepsNodeProps>(n).items ?? []" :locale="locale" />
      <SurfaceList
        v-else-if="n.type === 'list'"
        :columns="p<ListNodeProps>(n).columns ?? []"
        :rows="p<ListNodeProps>(n).rows ?? []"
        :empty="p<ListNodeProps>(n).empty"
        :locale="locale"
        :disabled="disabled"
        :table-id="plugin ? `app.${plugin}.${n.id || 'list'}` : undefined"
        @action="(a) => emit('action', a)"
      />
      <SurfaceProgress
        v-else-if="n.type === 'progress'"
        :value="p<ProgressNodeProps>(n).value"
        :label="p<ProgressNodeProps>(n).label"
        :locale="locale"
      />
      <SurfacePeoplePicker
        v-else-if="n.type === 'people-picker' && n.id"
        :id="n.id"
        :model-value="people(vals()[n.id])"
        :multi="p<PeoplePickerNodeProps>(n).multi === true"
        :allow-external="p<PeoplePickerNodeProps>(n).allow_external === true"
        :api="api"
        :plugin="plugin"
        :locale="locale"
        :disabled="disabled"
        :invalid="!!errorOf(n)"
        @update:model-value="(v) => set(n.id as string, v)"
      />
      <SurfacePinInput
        v-else-if="n.type === 'pin-input' && n.id"
        :id="n.id"
        :length="p<PinInputNodeProps>(n).length"
        :model-value="str(vals()[n.id])"
        :locale="locale"
        :disabled="disabled"
        :invalid="!!errorOf(n)"
        @update:model-value="(v) => set(n.id as string, v)"
      />
      <SurfaceFileChooser
        v-else-if="n.type === 'file-chooser' && n.id"
        :id="n.id"
        :kind="p<FileChooserNodeProps>(n).kind"
        :model-value="str(vals()[n.id])"
        :api="api"
        :storages="storages"
        :start-at="startAt"
        :locale="locale"
        :theme="theme"
        :disabled="disabled"
        :invalid="!!errorOf(n)"
        @update:model-value="(v) => set(n.id as string, v)"
      />
      <SurfacePreview
        v-else-if="n.type === 'preview'"
        :path="str(p<PreviewNodeProps>(n).path)"
        :file-ref="str(p<PreviewNodeProps>(n).ref)"
        :file-url="fileUrl"
        :api="api"
        :locale="locale"
      />
      <SurfaceSignaturePad
        v-else-if="n.type === 'signature-pad' && n.id"
        :id="n.id"
        :modes="p<SignaturePadNodeProps>(n).modes"
        :width="p<SignaturePadNodeProps>(n).width"
        :height="p<SignaturePadNodeProps>(n).height"
        :font="p<SignaturePadNodeProps>(n).font"
        :fonts="p<SignaturePadNodeProps>(n).fonts"
        :label="labelOf(p<SignaturePadNodeProps>(n).label ?? '', locale)"
        :required="p<SignaturePadNodeProps>(n).required === true"
        :model-value="isSignatureValue(vals()[n.id]) ? (vals()[n.id] as never) : null"
        :locale="locale"
        :disabled="disabled"
        :invalid="!!errorOf(n)"
        @update:model-value="(v) => set(n.id as string, v)"
      />
      <SurfacePdfFields
        v-else-if="n.type === 'pdf-fields' && n.id"
        :id="n.id"
        :src="p<PdfFieldsNodeProps>(n).src"
        :mode="p<PdfFieldsNodeProps>(n).mode"
        :fields="p<PdfFieldsNodeProps>(n).fields"
        :layout="layout"
        :page-height="pageHeight"
        :signers="p<PdfFieldsNodeProps>(n).signers"
        :signer="p<PdfFieldsNodeProps>(n).signer"
        :types="p<PdfFieldsNodeProps>(n).types"
        :formats="p<PdfFieldsNodeProps>(n).formats"
        :date-labels="p<PdfFieldsNodeProps>(n).date_labels"
        :stamp-lines="p<PdfFieldsNodeProps>(n).stamp_lines"
        :model-value="vals()[n.id]"
        :api="api"
        :file-url="fileUrl"
        :pdf-worker-url="pdfWorkerUrl"
        :locale="locale"
        :theme="theme"
        :disabled="disabled"
        :invalid="!!errorOf(n)"
        @update:model-value="(v) => onPdfValue(n, v)"
      />
      <p v-else class="fe-surface__unknown" :data-node-type="n.type">
        {{ t('plugin.view.unsupported', { type: n.type }) }}
      </p>
      <p v-if="errorOf(n) && n.type !== 'form'" class="fe-surface__error">{{ errorOf(n) }}</p>
    </template>
  </div>
</template>
