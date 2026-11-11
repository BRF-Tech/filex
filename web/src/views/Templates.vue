<script setup lang="ts">
/**
 * Templates — every mail filex sends and every page it shows a stranger, as
 * Liquid templates an administrator can change (backend internal/templates,
 * docs/TEMPLATES.md).
 *
 * Two layers, on purpose:
 *   1. the theme - colours, logo, footer - is the Branding page's, and every
 *      template wears it (the note at the top says where);
 *   2. here, the full HTML/CSS of each template, with a live preview made by
 *      the SERVER (the sandbox the real renders run in, with sample data) in a
 *      sandboxed frame: no script of the preview runs in this page.
 *
 * ⚠ Whose templates these are is the server's to say (the session's
 * tenant): the install's administrator edits the install's, a tenant's
 * administrator their tenant's. "Back to the default" removes only the
 * caller's own text; a tenant then gets the install's again.
 *
 * ⚠ The text is checked on save, on the server: a template that does not
 * render with the sample data, names a variable it does not have, or lost an
 * element its page's script needs is refused with the engine's own words.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import axios from 'axios';
import { FileCode2, RotateCcw, Save, Send, AlertTriangle, Palette, FileInput } from 'lucide-vue-next';
import { ChoiceButtons, CodeEditor, type ChoiceOption } from '@brftech/filex-core';

import { TemplatesApi, errorLineOf, type TemplateList, type TemplatePreview, type TemplateRow, type TemplateTexts } from '@/api/templates';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Modal from '@/components/ui/Modal.vue';
import Select from '@/components/ui/Select.vue';
import { liveTheme } from '@/lib/theme';

const { t, locale } = useI18n();
const toast = useToastStore();

const list = ref<TemplateList | null>(null);
const loading = ref(true);
const selected = ref<string>('');
const row = ref<TemplateRow | null>(null);
const texts = ref<TemplateTexts>({ subject: '', body: '', text: '' });
const saved = ref<TemplateTexts>({ subject: '', body: '', text: '' });
const defaults = ref<TemplateTexts>({ subject: '', body: '', text: '' });
const part = ref<'body' | 'subject' | 'text'>('body');
const previewLang = ref<string>(String(locale.value));
const preview = ref<TemplatePreview | null>(null);
const previewView = ref<'html' | 'text'>('html');
const previewError = ref('');
const busy = ref(false);
const previewing = ref(false);
const confirmReset = ref(false);
/** The variables' section is open: its chips exist only then (a closed
 *  section's buttons are still boxes a layout measure trips over). */
const varsOpen = ref(false);
const pendingKey = ref('');

/** A dotted key as an i18n key: `mail.share` → `templates.name.mail_share`. */
const nameOf = (key: string) => t(`templates.name.${key.replace(/\./g, '_')}`);

const groups = computed(() => {
  const all = list.value?.templates ?? [];
  return [
    { id: 'mail', label: t('templates.group.mail'), items: all.filter((r) => r.key.startsWith('mail.')) },
    { id: 'page', label: t('templates.group.page'), items: all.filter((r) => r.key.startsWith('page.')) },
    { id: 'slot', label: t('templates.group.slot'), items: all.filter((r) => r.key.startsWith('slot.')) },
  ];
});

const ownSource = computed(() => (list.value?.scope.tenant ? 'tenant' : 'instance'));
const isOwn = computed(() => row.value?.source === ownSource.value);
const dirty = computed(
  () => texts.value.body !== saved.value.body || texts.value.subject !== saved.value.subject || texts.value.text !== saved.value.text,
);
const isMail = computed(() => !!row.value && (row.value.kind === 'mail' || row.value.key === 'mail.layout'));

const partOptions = computed<ChoiceOption[]>(() => {
  const opts: ChoiceOption[] = [{ value: 'body', label: t('templates.part.body') }];
  if (row.value?.subject) opts.push({ value: 'subject', label: t('templates.part.subject') });
  if (row.value?.text) opts.push({ value: 'text', label: t('templates.part.text'), help: t('templates.part.textHelp') });
  return opts;
});

const langOptions = computed(() => {
  const langs = new Set<string>(list.value?.languages ?? ['en', 'tr']);
  langs.add(String(locale.value));
  return [...langs].map((l) => ({ value: l, label: t(`templates.lang.${l}`) === `templates.lang.${l}` ? l : t(`templates.lang.${l}`) }));
});

const sourceLabel = (r: TemplateRow) => t(`templates.source.${r.source}`);
const sourceTone = (r: TemplateRow) => (r.source === 'default' ? 'zinc' : r.source === ownSource.value ? 'emerald' : 'sky');

function current(): string {
  return texts.value[part.value];
}
function setCurrent(v: string) {
  texts.value = { ...texts.value, [part.value]: v };
}

async function load() {
  loading.value = true;
  try {
    list.value = await TemplatesApi.list();
    if (!selected.value && list.value.templates.length) await open(list.value.templates[0].key);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function open(key: string) {
  if (dirty.value && key !== selected.value && pendingKey.value !== key) {
    pendingKey.value = key;
    return;
  }
  pendingKey.value = '';
  try {
    const d = await TemplatesApi.get(key);
    selected.value = key;
    row.value = d.template;
    texts.value = { ...d.current };
    saved.value = { ...d.current };
    defaults.value = { ...d.default };
    part.value = 'body';
    previewView.value = 'html';
    await runPreview();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

function discardAndOpen() {
  const key = pendingKey.value;
  texts.value = { ...saved.value };
  pendingKey.value = '';
  if (key) void open(key);
}

/** The engine's own words for a refused draft (they carry the line). */
function refusal(e: unknown): string {
  if (axios.isAxiosError(e)) {
    const d = e.response?.data as { detail?: string; missing?: string[] } | undefined;
    const said = extractError(e, t('errors.generic'));
    if (d?.missing?.length) return `${said} (${d.missing.join(', ')})`;
    if (d?.detail) return `${said}\n${d.detail}`;
    return said;
  }
  return t('errors.generic');
}

let timer: ReturnType<typeof setTimeout> | null = null;
async function runPreview() {
  if (!selected.value) return;
  previewing.value = true;
  try {
    preview.value = await TemplatesApi.preview(selected.value, texts.value, previewLang.value);
    previewError.value = '';
  } catch (e) {
    previewError.value = refusal(e);
  } finally {
    previewing.value = false;
  }
}
watch([texts, previewLang], () => {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => void runPreview(), 500);
}, { deep: true });
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer);
});

async function save() {
  if (!row.value) return;
  busy.value = true;
  try {
    const r = await TemplatesApi.save(selected.value, texts.value, isOwn.value ? row.value.revision : 0, previewLang.value);
    row.value = r.template;
    saved.value = { ...texts.value };
    preview.value = r.preview;
    previewError.value = '';
    await refreshRow();
    toast.success(t('templates.saved'));
  } catch (e) {
    previewError.value = refusal(e);
    toast.error(t('templates.notSaved'));
  } finally {
    busy.value = false;
  }
}

async function reset() {
  confirmReset.value = false;
  busy.value = true;
  try {
    await TemplatesApi.reset(selected.value);
    const key = selected.value;
    selected.value = '';
    saved.value = { ...texts.value };
    await open(key);
    await refreshRow();
    toast.success(t('templates.resetDone'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

function startFromDefault() {
  texts.value = { ...defaults.value };
}

async function testMail() {
  busy.value = true;
  try {
    const r = await TemplatesApi.testMail(selected.value, texts.value, previewLang.value);
    toast.success(t('templates.testSent', { to: r.to }));
  } catch (e) {
    toast.error(refusal(e));
  } finally {
    busy.value = false;
  }
}

async function refreshRow() {
  try {
    list.value = await TemplatesApi.list();
    row.value = list.value.templates.find((r) => r.key === selected.value) ?? row.value;
  } catch {
    /* the list is a convenience; the editor keeps its row */
  }
}

/** `{{ var }}` at the cursor, from the variable list. */
const editor = ref<InstanceType<typeof CodeEditor> | null>(null);
function insertVar(v: string) {
  const piece = `{{ ${v} }}`;
  if (editor.value) editor.value.insert(piece);
  else setCurrent(current() + piece);
}

/** The editor grows with the template, between 18 and 40 lines of 20 px. */
const editorHeight = computed(() => Math.min(Math.max(current().split('\n').length + 2, 18), 40) * 20);

/**
 * The line of the part on screen the engine refused, read off its words:
 * "Liquid error (line 3): ... in mail.share". An error in ANOTHER template
 * (the layout around this one) names that one, and marks nothing here.
 */
const errorLine = computed<number | null>(() => {
  if (!row.value || !previewError.value) return null;
  return errorLineOf(previewError.value, part.value === 'body' ? row.value.key : `${row.value.key}.${part.value}`);
});

/** How a template says a sentence of the language packs (shown, not translated). */
const tExample = "{{ 'mail.greeting' | t }}";

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div>
      <h1 class="text-xl font-semibold flex items-center gap-2">
        <FileCode2 class="h-5 w-5" /> {{ t('templates.title') }}
      </h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('templates.subtitle') }}</p>
    </div>

    <div class="card card-body flex items-start gap-3 text-sm" data-testid="templates-theme-note">
      <Palette class="h-4 w-4 mt-0.5 shrink-0 text-zinc-500" />
      <p class="text-zinc-600 dark:text-zinc-300">
        {{ t('templates.themeNote') }}
        <RouterLink :to="{ name: 'branding' }" class="text-blue-600 dark:text-blue-400 underline">{{ t('nav.branding') }}</RouterLink>
      </p>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="grid gap-4 lg:grid-cols-[300px_minmax(0,1fr)] items-start">
      <!-- ── The templates ── -->
      <nav class="card card-body space-y-4" :aria-label="t('templates.title')" data-testid="templates-list">
        <div v-for="g in groups" :key="g.id" class="space-y-1">
          <h2 class="text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-400">{{ g.label }}</h2>
          <button
            v-for="r in g.items"
            :key="r.key"
            type="button"
            class="w-full flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm text-start hover:bg-zinc-100 dark:hover:bg-zinc-800"
            :class="r.key === selected && 'bg-zinc-100 dark:bg-zinc-800 font-medium'"
            :aria-current="r.key === selected ? 'true' : undefined"
            :data-testid="`template-item-${r.key}`"
            @click="open(r.key)"
          >
            <span class="min-w-0 [overflow-wrap:anywhere]">{{ nameOf(r.key) }}</span>
            <span class="flex items-center gap-1 shrink-0">
              <AlertTriangle v-if="r.last_error" class="h-3.5 w-3.5 text-amber-500" :aria-label="t('templates.failedBadge')" />
              <Badge v-if="r.source !== 'default'" :tone="sourceTone(r)">{{ sourceLabel(r) }}</Badge>
            </span>
          </button>
        </div>
      </nav>

      <!-- ── The editor ── -->
      <section v-if="row" class="space-y-4" data-testid="template-editor" :data-key="row.key">
        <div class="card card-body space-y-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 class="text-base font-semibold">{{ nameOf(row.key) }}</h2>
              <p class="text-xs text-zinc-500 dark:text-zinc-400">
                <code class="font-mono">{{ row.key }}</code> · {{ sourceLabel(row) }}
                <template v-if="row.layout"> · {{ t('templates.inLayout', { layout: nameOf(row.layout) }) }}</template>
              </p>
            </div>
            <ChoiceButtons
              v-if="partOptions.length > 1"
              :options="partOptions"
              :model-value="part"
              segmented
              :aria-label="t('templates.partLabel')"
              testid-prefix="template-part"
              @update:model-value="(v) => (part = v as 'body' | 'subject' | 'text')"
            />
          </div>

          <p v-if="row.kind === 'slot'" class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('templates.slotHelp') }}</p>
          <p v-if="part === 'text'" class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('templates.part.textHelp') }}</p>
          <p v-if="row.last_error" class="text-xs text-amber-700 dark:text-amber-400" data-testid="template-last-error">
            {{ t('templates.lastError', { when: row.last_error_at ?? '' }) }} {{ row.last_error }}
          </p>

          <!-- filex's code editor (core CodeEditor: Monaco, the file preview's),
               Liquid colours, line numbers, the refused line marked. -->
          <CodeEditor
            :key="`${row.key}:${part}`"
            ref="editor"
            :model-value="current()"
            language="liquid"
            :theme="liveTheme"
            :height="editorHeight"
            :error-line="errorLine"
            :error-message="previewError"
            :aria-label="t('templates.editorLabel')"
            testid="template-source"
            @update:model-value="setCurrent"
          />

          <details class="text-sm" data-testid="template-vars" @toggle="(e) => (varsOpen = (e.target as HTMLDetailsElement).open)">
            <summary class="cursor-pointer text-zinc-600 dark:text-zinc-300">{{ t('templates.varsTitle') }}</summary>
            <p class="mt-2 text-xs text-zinc-500 dark:text-zinc-400">
              {{ t('templates.varsHelp') }} <code class="font-mono" dir="ltr">{{ tExample }}</code>
            </p>
            <div v-if="varsOpen" class="mt-2 flex flex-wrap gap-1.5">
              <button
                v-for="v in row.all_vars"
                :key="v"
                type="button"
                class="rounded bg-zinc-100 dark:bg-zinc-800 px-1.5 py-0.5 font-mono text-xs hover:bg-zinc-200 dark:hover:bg-zinc-700"
                @click="insertVar(v)"
              >
                {{ v }}
              </button>
            </div>
            <p v-if="row.hooks?.length" class="mt-2 text-xs text-zinc-500 dark:text-zinc-400">
              {{ t('templates.hooks', { hooks: row.hooks.join(', ') }) }}
            </p>
          </details>

          <div class="flex flex-wrap items-center justify-between gap-2 pt-1">
            <div class="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" @click="startFromDefault">
                <FileInput class="h-4 w-4" /> {{ t('templates.startFromDefault') }}
              </Button>
              <Button v-if="isOwn" type="button" variant="outline" size="sm" :disabled="busy" data-testid="template-reset" @click="confirmReset = true">
                <RotateCcw class="h-4 w-4" /> {{ t('templates.reset') }}
              </Button>
            </div>
            <div class="flex flex-wrap gap-2">
              <Button v-if="isMail" type="button" variant="outline" size="sm" :disabled="busy" data-testid="template-test" @click="testMail">
                <Send class="h-4 w-4" /> {{ t('templates.testMail') }}
              </Button>
              <Button type="button" :loading="busy" :disabled="!dirty && isOwn" data-testid="template-save" @click="save">
                <Save class="h-4 w-4" /> {{ t('common.save') }}
              </Button>
            </div>
          </div>
        </div>

        <!-- ── The preview: rendered by the server, in a frame that runs nothing ── -->
        <div class="card card-body space-y-3" data-testid="template-preview">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 class="text-sm font-semibold text-zinc-500 dark:text-zinc-400 uppercase tracking-wide flex items-center gap-2">
              {{ t('templates.preview') }} <Spinner v-if="previewing" class="h-3.5 w-3.5" />
            </h2>
            <div class="flex flex-wrap items-center gap-2">
              <ChoiceButtons
                v-if="isMail"
                :options="[{ value: 'html', label: t('templates.view.html') }, { value: 'text', label: t('templates.view.text') }]"
                :model-value="previewView"
                segmented
                :aria-label="t('templates.viewLabel')"
                testid-prefix="template-view"
                @update:model-value="(v) => (previewView = v as 'html' | 'text')"
              />
              <div class="w-40">
                <Select
                  :model-value="previewLang"
                  :options="langOptions"
                  :aria-label="t('templates.previewLang')"
                  @update:model-value="(v) => (previewLang = String(v))"
                />
              </div>
            </div>
          </div>
          <pre v-if="previewError" class="whitespace-pre-wrap rounded-md bg-rose-50 dark:bg-rose-950/40 p-3 text-xs text-rose-700 dark:text-rose-300" role="alert" data-testid="template-error">{{ previewError }}</pre>
          <template v-if="preview">
            <p v-if="preview.removed?.length" class="text-xs text-amber-700 dark:text-amber-400" data-testid="template-removed">
              {{ t('templates.removed', { what: preview.removed.join(', ') }) }}
            </p>
            <p v-if="preview.missing?.length" class="text-xs text-rose-700 dark:text-rose-400">
              {{ t('templates.missing', { what: preview.missing.join(', ') }) }}
            </p>
            <p v-if="preview.subject" class="text-sm"><span class="text-zinc-500">{{ t('templates.part.subject') }}:</span> {{ preview.subject }}</p>
            <pre
              v-if="isMail && previewView === 'text'"
              class="whitespace-pre-wrap rounded-md border border-zinc-200 dark:border-zinc-700 p-3 text-xs font-mono"
              data-testid="template-preview-text"
            >{{ preview.text }}</pre>
            <iframe
              v-else
              class="w-full h-[640px] rounded-md border border-zinc-200 dark:border-zinc-700 bg-white"
              sandbox=""
              referrerpolicy="no-referrer"
              :title="t('templates.preview')"
              :srcdoc="preview.html"
              data-testid="template-preview-frame"
            />
          </template>
        </div>
      </section>
    </div>

    <Modal v-model="confirmReset" :title="t('templates.resetTitle')">
      <p class="text-sm">{{ list?.scope.tenant ? t('templates.resetTextTenant') : t('templates.resetText') }}</p>
      <template #footer>
        <Button variant="ghost" @click="confirmReset = false">{{ t('common.cancel') }}</Button>
        <Button variant="danger" data-testid="template-reset-confirm" @click="reset">{{ t('templates.reset') }}</Button>
      </template>
    </Modal>

    <Modal :model-value="!!pendingKey" :title="t('templates.unsavedTitle')" @update:model-value="(v) => !v && (pendingKey = '')">
      <p class="text-sm">{{ t('templates.unsavedText') }}</p>
      <template #footer>
        <Button variant="ghost" @click="pendingKey = ''">{{ t('common.cancel') }}</Button>
        <Button variant="danger" @click="discardAndOpen">{{ t('templates.discard') }}</Button>
      </template>
    </Modal>
  </div>
</template>
