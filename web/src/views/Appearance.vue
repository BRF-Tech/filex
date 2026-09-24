<script setup lang="ts">
/**
 * tema:v1 — Görünüm / Appearance: the instance's own themes, and the raw-CSS
 * escape hatch.
 *
 * ⚠⚠ THIS SCREEN IS THE ONE THAT MUST ALWAYS WORK. It is where an operator
 * turns off a stylesheet they have just pasted, so it is immune to that
 * stylesheet two independent ways:
 *
 *  1. `suspendCustomCss()` on mount / `resumeCustomCss()` on unmount — the
 *     <style> element is physically out of the document while this route is
 *     open. That is the guard that survives `:root { display: none }`, which
 *     no amount of selector scoping can undo.
 *  2. The panel root carries `fe-css-immune`, the class named by the server's
 *     `@scope (:root) to (.fe-css-immune)` wrapper. That is the guard for
 *     surfaces where the sheet IS loaded, and it is here as well so the class
 *     is exercised by the same page it protects.
 *
 * ⚠ No folded "advanced" section, per the panel's rules: the dangerous switch
 * is visible, labelled, and next to what it does. A fold would hide the one
 * control somebody comes here in a hurry to find.
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  Palette,
  Save,
  Trash2,
  Upload,
  Plus,
  Check,
  TriangleAlert,
  RotateCcw,
} from 'lucide-vue-next';

import { AppearanceApi, type ThemeDocument } from '@/api/appearance';
import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { applyInstanceThemes } from '@/lib/instanceThemes';
import { formatBytes } from '@/lib/format';
import {
  suspendCustomCss,
  resumeCustomCss,
  loadCustomCss,
  applyCustomCss,
} from '@/lib/customCss';
import {
  AUTHORED_TOKENS,
  contrastWarnings,
  draftFromStored,
  draftToTokens,
  newDraft,
  slugify,
  THEME_KEY_RE,
  type ThemeDraft,
} from '@/lib/themeTokens';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Textarea from '@/components/ui/Textarea.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Spinner from '@/components/ui/Spinner.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

const CUSTOM_CSS_KEY = 'ui.custom_css';
const CUSTOM_CSS_ENABLED_KEY = 'ui.custom_css_enabled';
const DEFAULT_THEME_KEY = 'ui.default_theme';
const CUSTOM_CSS_MAX_BYTES = 64 * 1024;

const { t, locale } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();

/* ── themes ────────────────────────────────────────────────────────── */

const themes = ref<ThemeDocument[]>([]);
const loading = ref(true);
const saving = ref(false);
const draft = reactive<ThemeDraft>(newDraft());
const editingExisting = ref(false);
/** Which variant the editor is showing. A segmented control, not a native
 *  <select> — the panel's rule, and two options never wanted a dropdown. */
const variant = ref<'light' | 'dark'>('light');
const fileInput = ref<HTMLInputElement | null>(null);

const defaultThemeId = computed(() => (settings.data[DEFAULT_THEME_KEY] as string) || 'default');

const previewTokens = computed(() => draftToTokens(draft));
const previewVars = computed(() =>
  variant.value === 'dark' ? previewTokens.value.dark : previewTokens.value.light,
);
const warnings = computed(() => contrastWarnings(previewVars.value));

const keyValid = computed(() => THEME_KEY_RE.test(draft.key));
const canSave = computed(() => !!draft.name.trim() && keyValid.value && !saving.value);

const rows = computed(() =>
  themes.value.map((th) => ({
    key: th.key,
    name: th.name,
    id: `custom:${th.key}`,
    isDefault: `custom:${th.key}` === defaultThemeId.value,
    swatch: th.light?.['--fe-primary'] ?? '#2f6ceb',
  })),
);

type ThemeRow = (typeof rows.value)[number];

/* The explorer's table (DataTable), remembered under `admin.appearance.themes`.
 * The list is every theme at once, so the table's own sort is honest. */
const columns = computed<DataColumn<ThemeRow>[]>(() => [
  { id: 'name', label: t('appearance.col.name'), sortable: true, width: 260 },
  { id: 'id', label: t('appearance.col.id'), sortable: true, width: 200 },
]);

/**
 * The row's verbs, in the one pinned control every admin table uses.
 *
 * ⚠⚠ This screen was written on a branch that predates that rule and shipped
 * four loose buttons in the cell — two of them ICON ONLY, with the word hidden
 * in `sr-only` text. The guard test caught it at the merge, which is exactly
 * what it is for. The rule is the owner's and it is not cosmetic: on a narrow
 * window a scatter of buttons falls off the right edge of the table, and a
 * glyph alone does not tell an administrator what a button will do before they
 * press it. Every verb is NAMED here.
 *
 * ⚠ "Make default" is HIDDEN on the row that already is the default rather
 * than greyed: a greyed entry invites a press that can never do anything,
 * while the row's own badge already says which one it is.
 */
function rowActions(row: (typeof rows.value)[number]): ContextAction[] {
  return [
    // ⚠ `rename` is the pencil in the shared catalogue; there is no `edit`
    // key, and an unknown name falls through to a generic glyph rather
    // than failing, so a typo here would ship silently.
    { key: 'edit', label: t('common.edit'), icon: 'rename' },
    {
      key: 'default',
      label: t('appearance.makeDefault'),
      icon: 'star',
      hidden: row.isDefault,
    },
    { key: 'export', label: t('appearance.export'), icon: 'download' },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}

function onRowAction(key: string, row: (typeof rows.value)[number]) {
  if (key === 'edit') edit(row.key);
  else if (key === 'default') void makeDefault(row.id);
  else if (key === 'export') exportTheme(row.key);
  else if (key === 'delete') void remove(row.key);
}

async function refresh() {
  loading.value = true;
  try {
    themes.value = await AppearanceApi.list();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

/** Republish the palette list so the change is visible everywhere at once —
 *  the explorer's gallery and the user settings modal read the same registry. */
async function republish() {
  AppearanceApi.invalidateBoot();
  try {
    applyInstanceThemes(await AppearanceApi.boot());
  } catch {
    /* the list on this page is already correct; the registry catches up on
       the next page load */
  }
}

function startNew() {
  Object.assign(draft, newDraft());
  editingExisting.value = false;
}

function edit(key: string) {
  const found = themes.value.find((th) => th.key === key);
  if (!found) return;
  Object.assign(draft, draftFromStored(found));
  editingExisting.value = true;
}

function onNameInput(v: string) {
  const hadSuggestedKey = !editingExisting.value && (draft.key === '' || draft.key === slugify(draft.name));
  draft.name = v;
  // Suggest a slug while the operator has not typed one of their own, and
  // stop the moment they do — silently rewriting a key somebody chose would
  // change the id an exported file carries.
  if (hadSuggestedKey) draft.key = slugify(v);
}

async function save() {
  if (!canSave.value) return;
  saving.value = true;
  try {
    const tokens = draftToTokens(draft);
    const doc: ThemeDocument = {
      filex_theme: 1,
      key: draft.key,
      name: draft.name.trim(),
      light: tokens.light,
      dark: tokens.dark,
    };
    await AppearanceApi.put(draft.key, doc);
    await refresh();
    await republish();
    editingExisting.value = true;
    toast.success(t('appearance.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

async function remove(key: string) {
  if (!window.confirm(t('appearance.deleteConfirm', { name: key }))) return;
  try {
    await AppearanceApi.remove(key);
    await refresh();
    // ⚠ Republishing is what puts anybody still holding this theme back on the
    // stock palette — including whoever is looking at this page right now.
    await republish();
    if (draft.key === key) startNew();
    toast.success(t('appearance.deletedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function makeDefault(id: string) {
  try {
    await settings.update({ [DEFAULT_THEME_KEY]: id });
    await republish();
    toast.success(t('appearance.defaultOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

function exportTheme(key: string) {
  const found = themes.value.find((th) => th.key === key);
  if (!found) return;
  const blob = new Blob([JSON.stringify(found, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `filex-theme-${key}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function pickImport() {
  fileInput.value?.click();
}

async function onImportFile(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  try {
    const doc = JSON.parse(await file.text()) as ThemeDocument;
    // The server validates for real; this only catches an obviously wrong file
    // before a round trip.
    if (!doc || typeof doc !== 'object' || !doc.key) throw new Error(t('appearance.importBad'));
    await AppearanceApi.put(doc.key, doc);
    await refresh();
    await republish();
    edit(doc.key);
    toast.success(t('appearance.importOk', { name: doc.name ?? doc.key }));
  } catch (err: unknown) {
    toast.error(extractError(err, t('appearance.importBad')));
  }
}

/* ── the escape hatch ──────────────────────────────────────────────── */

const cssEnabled = ref(false);
const cssText = ref('');
const cssSaving = ref(false);
const cssBytes = computed(() => new TextEncoder().encode(cssText.value).length);
const cssTooBig = computed(() => cssBytes.value > CUSTOM_CSS_MAX_BYTES);

function loadCssFromSettings() {
  cssText.value = (settings.data[CUSTOM_CSS_KEY] as string) ?? '';
  cssEnabled.value = String(settings.data[CUSTOM_CSS_ENABLED_KEY] ?? '') === 'true';
}

async function saveCss() {
  if (cssTooBig.value) {
    toast.error(t('appearance.css.tooBig'));
    return;
  }
  cssSaving.value = true;
  try {
    await settings.update({
      [CUSTOM_CSS_KEY]: cssText.value,
      [CUSTOM_CSS_ENABLED_KEY]: cssEnabled.value ? 'true' : 'false',
    });
    toast.success(t('appearance.css.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    cssSaving.value = false;
  }
}

/** One click: the sheet is gone and the switch is off. */
async function removeCss() {
  if (!window.confirm(t('appearance.css.removeConfirm'))) return;
  cssSaving.value = true;
  try {
    await settings.update({ [CUSTOM_CSS_KEY]: '', [CUSTOM_CSS_ENABLED_KEY]: 'false' });
    cssText.value = '';
    cssEnabled.value = false;
    // Drop whatever is cached in this tab too, so leaving this screen does not
    // restore a sheet that no longer exists.
    applyCustomCss('');
    toast.success(t('appearance.css.removedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    cssSaving.value = false;
  }
}

/* ── lifecycle ─────────────────────────────────────────────────────── */

onMounted(async () => {
  // ⚠⚠ FIRST, before anything can paint: this screen edits and removes the
  // operator stylesheet, so it must not be wearing it.
  suspendCustomCss();
  await settings.fetch();
  loadCssFromSettings();
  await refresh();
  startNew();
});

onBeforeUnmount(() => {
  resumeCustomCss();
  // Re-read on the way out so the rest of the panel immediately wears what was
  // just saved, rather than the copy this tab booted with.
  void loadCustomCss();
});
</script>

<template>
  <!-- fe-css-immune: the class the server's @scope wrapper excludes. -->
  <div class="fe-css-immune space-y-4 max-w-6xl" data-testid="appearance-page">
    <div>
      <h1 class="text-xl font-semibold flex items-center gap-2">
        <Palette class="h-5 w-5" /> {{ t('appearance.title') }}
      </h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('appearance.subtitle') }}</p>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <template v-else>
      <!-- ── Saved themes ── -->
      <section class="card card-body space-y-3">
        <div class="flex items-center justify-between gap-2 flex-wrap">
          <h2 class="text-sm font-semibold">{{ t('appearance.saved') }}</h2>
          <div class="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" @click="pickImport">
              <Upload class="h-3.5 w-3.5" /> {{ t('appearance.import') }}
            </Button>
            <Button type="button" size="sm" data-testid="theme-new" @click="startNew">
              <Plus class="h-3.5 w-3.5" /> {{ t('appearance.new') }}
            </Button>
            <input
              ref="fileInput"
              type="file"
              accept="application/json,.json"
              class="hidden"
              data-testid="theme-import-input"
              @change="onImportFile"
            />
          </div>
        </div>

        <DataTable
          table-id="admin.appearance.themes"
          :columns="columns"
          :rows="rows"
          row-key="key"
          :empty="t('appearance.noThemes')"
          data-testid="theme-table"
          :row-actions="(row: ThemeRow) => rowActions(row)"
          :row-actions-test-id="(row: ThemeRow) => `theme-actions-${row.key}`"
          @row-action="(key: string, row: ThemeRow) => onRowAction(key, row)"
        >
          <template #cell-name="{ row }">
            <span class="inline-flex items-center gap-2">
              <span
                class="inline-block h-3.5 w-3.5 rounded-full border border-zinc-300 dark:border-zinc-600"
                :style="{ backgroundColor: row.swatch }"
                aria-hidden="true"
              />
              <span class="tbl-clamp font-medium">{{ row.name }}</span>
              <span
                v-if="row.isDefault"
                class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs bg-brand-50 text-brand-700 dark:bg-brand-900/40 dark:text-brand-200"
              >
                <Check class="h-3 w-3" /> {{ t('appearance.isDefault') }}
              </span>
            </span>
          </template>
          <template #cell-id="{ row }">
            <span class="tbl-mono text-xs">{{ row.id }}</span>
          </template>
        </DataTable>

        <p v-if="rows.length" class="text-xs text-zinc-500 dark:text-zinc-400">
          {{ t('appearance.deleteFallbackNote') }}
        </p>
      </section>

      <!-- ── Editor + live preview ── -->
      <section class="grid gap-4 lg:grid-cols-2 items-start">
        <form class="card card-body space-y-3" @submit.prevent="save">
          <h2 class="text-sm font-semibold">
            {{ editingExisting ? t('appearance.editing', { name: draft.name }) : t('appearance.composing') }}
          </h2>

          <Input
            :model-value="draft.name"
            :label="t('appearance.name')"
            :hint="t('appearance.nameHelp')"
            :placeholder="t('appearance.namePlaceholder')"
            data-testid="theme-name"
            @update:model-value="(v) => onNameInput(v as string)"
          />

          <Input
            :model-value="draft.key"
            :label="t('appearance.key')"
            :hint="t('appearance.keyHelp')"
            monospace
            :disabled="editingExisting"
            :error="draft.key && !keyValid ? t('appearance.keyInvalid') : null"
            data-testid="theme-key"
            @update:model-value="(v) => (draft.key = (v as string).toLowerCase())"
          />

          <!-- Variant switch: a segmented control. Two options never needed a
               dropdown, and the panel forbids a native one here. -->
          <div>
            <span class="label-base block mb-1">{{ t('appearance.variant') }}</span>
            <div
              class="inline-flex rounded-lg border border-zinc-200 dark:border-zinc-700 p-0.5"
              role="group"
              :aria-label="t('appearance.variant')"
            >
              <button
                v-for="v in (['light', 'dark'] as const)"
                :key="v"
                type="button"
                :data-testid="`theme-variant-${v}`"
                :aria-pressed="variant === v"
                class="px-3 py-1 text-sm rounded-md transition-colors"
                :class="
                  variant === v
                    ? 'bg-brand-600 text-white'
                    : 'text-zinc-600 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800'
                "
                @click="variant = v"
              >
                {{ v === 'light' ? t('appearance.light') : t('appearance.dark') }}
              </button>
            </div>
          </div>

          <div class="grid sm:grid-cols-2 gap-x-4 gap-y-2">
            <div v-for="tok in AUTHORED_TOKENS" :key="tok.key">
              <label class="label-base block mb-1" :for="`tok-${tok.key}`">
                {{ t(tok.labelKey) }}
              </label>
              <div class="flex items-center gap-2">
                <input
                  :id="`tok-${tok.key}`"
                  type="color"
                  :value="draft[variant][tok.key]"
                  class="h-8 w-10 shrink-0 cursor-pointer rounded border border-zinc-200 dark:border-zinc-700 bg-transparent p-0.5"
                  :data-testid="`tok-${tok.key}`"
                  :aria-label="t(tok.labelKey)"
                  @input="(e) => (draft[variant][tok.key] = (e.target as HTMLInputElement).value)"
                />
                <input
                  type="text"
                  :value="draft[variant][tok.key]"
                  class="input-base px-2 py-1 text-xs font-mono"
                  :data-testid="`tokhex-${tok.key}`"
                  @input="(e) => (draft[variant][tok.key] = (e.target as HTMLInputElement).value)"
                />
              </div>
            </div>
          </div>

          <div class="grid sm:grid-cols-2 gap-4">
            <Input
              :model-value="draft.radius"
              :label="t('appearance.radius')"
              :hint="t('appearance.radiusHelp')"
              type="number"
              data-testid="theme-radius"
              @update:model-value="(v) => (draft.radius = v as string)"
            />
            <Input
              :model-value="draft.font"
              :label="t('appearance.font')"
              :hint="t('appearance.fontHelp')"
              monospace
              data-testid="theme-font"
              @update:model-value="(v) => (draft.font = v as string)"
            />
          </div>

          <!-- Contrast warnings: said out loud, never enforced. An operator's
               brand is theirs; a screen that refused the save would just push
               them to the raw-CSS hatch, which has no checks at all. -->
          <div
            v-if="warnings.length"
            class="rounded-lg border border-amber-300 bg-amber-50 p-2.5 text-xs text-amber-800 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
            data-testid="theme-contrast-warnings"
          >
            <p class="flex items-center gap-1.5 font-medium">
              <TriangleAlert class="h-3.5 w-3.5" /> {{ t('appearance.contrastTitle') }}
            </p>
            <ul class="mt-1 space-y-0.5">
              <li v-for="w in warnings" :key="w.labelKey">
                {{ t(w.labelKey) }} — {{ w.ratio.toFixed(2) }}:1 &lt; {{ w.min }}:1
              </li>
            </ul>
          </div>

          <div class="flex justify-between items-center pt-1">
            <Button type="button" variant="outline" size="sm" @click="startNew">
              <RotateCcw class="h-3.5 w-3.5" /> {{ t('appearance.reset') }}
            </Button>
            <Button type="submit" :loading="saving" :disabled="!canSave" data-testid="theme-save">
              <Save class="h-4 w-4" /> {{ t('common.save') }}
            </Button>
          </div>
        </form>

        <!-- Live preview: a real `.fe` subtree carrying the draft's tokens, so
             what is on screen is painted by the same rules the product uses
             rather than by a mock-up that can drift from it. -->
        <div class="card card-body space-y-2">
          <h2 class="text-sm font-semibold">{{ t('appearance.preview') }}</h2>
          <div
            class="fe theme-preview"
            :class="variant === 'dark' ? 'fe--theme-dark' : 'fe--theme-light'"
            :style="previewVars"
            data-testid="theme-preview"
          >
            <div class="tp-bar">
              <span class="tp-btn tp-btn--primary">{{ t('appearance.previewUpload') }}</span>
              <span class="tp-btn">{{ t('appearance.previewNew') }}</span>
              <span class="tp-spacer" />
              <span class="tp-btn tp-btn--danger">{{ t('common.delete') }}</span>
            </div>
            <div class="tp-body">
              <div class="tp-row"><span class="tp-dot" /><span class="tp-name">{{ t('common.sampleFileName') }}</span><span class="tp-meta">{{ formatBytes(1_200_000, locale) }}</span></div>
              <div class="tp-row is-selected"><span class="tp-dot tp-dot--primary" /><span class="tp-name">{{ t('appearance.sampleSlides') }}</span><span class="tp-meta">{{ formatBytes(4_800_000, locale) }}</span></div>
              <div class="tp-row"><span class="tp-dot tp-dot--muted" /><span class="tp-name">{{ t('appearance.sampleNotes') }}</span><span class="tp-meta">{{ formatBytes(2_000, locale) }}</span></div>
              <div class="tp-chips">
                <span class="tp-chip tp-chip--ok">{{ t('appearance.previewOk') }}</span>
                <span class="tp-chip tp-chip--warn">{{ t('appearance.previewWarn') }}</span>
                <span class="tp-chip tp-chip--danger">{{ t('appearance.previewDanger') }}</span>
              </div>
            </div>
          </div>
          <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('appearance.previewHelp') }}</p>
        </div>
      </section>

      <!-- ── The escape hatch ── -->
      <section class="card card-body space-y-3" data-testid="custom-css-panel">
        <div>
          <h2 class="text-sm font-semibold">{{ t('appearance.css.title') }}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('appearance.css.help') }}</p>
        </div>

        <div
          class="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
        >
          <p class="flex items-center gap-1.5 font-medium">
            <TriangleAlert class="h-4 w-4" /> {{ t('appearance.css.warnTitle') }}
          </p>
          <ul class="mt-1 list-disc ps-5 space-y-0.5">
            <li>{{ t('appearance.css.warnScope') }}</li>
            <li>{{ t('appearance.css.warnNetwork') }}</li>
            <li>{{ t('appearance.css.warnLie') }}</li>
            <li>{{ t('appearance.css.warnOperator') }}</li>
          </ul>
        </div>

        <Toggle
          v-model="cssEnabled"
          :label="t('appearance.css.enable')"
          :description="t('appearance.css.enableHelp')"
          name="custom-css-enabled"
        />

        <Textarea
          :model-value="cssText"
          :label="t('appearance.css.field')"
          :rows="10"
          monospace
          :error="cssTooBig ? t('appearance.css.tooBig') : null"
          :hint="t('appearance.css.hint')"
          data-testid="custom-css-text"
          @update:model-value="(v) => (cssText = v as string)"
        />

        <div class="flex items-center justify-between gap-2 flex-wrap">
          <span
            class="text-xs"
            :class="cssTooBig ? 'text-rose-500' : 'text-zinc-500 dark:text-zinc-400'"
          >
            {{ t('appearance.css.count', { used: cssBytes, max: CUSTOM_CSS_MAX_BYTES }) }}
          </span>
          <div class="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              data-testid="custom-css-remove"
              @click="removeCss"
            >
              <Trash2 class="h-3.5 w-3.5" /> {{ t('appearance.css.remove') }}
            </Button>
            <Button type="button" :loading="cssSaving" data-testid="custom-css-save" @click="saveCss">
              <Save class="h-4 w-4" /> {{ t('common.save') }}
            </Button>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
/* The preview is a real `.fe` scope so it paints with the product's own
   tokens. ⚠ It must NOT inherit the explorer root's box — filex lesson #139:
   a wrapper given `.fe` wears `flex-direction: column; min-height: 420px;
   height: 100%; border; border-radius` and turns into a framed column. The
   three lines below take that back. */
.theme-preview {
  min-height: 0;
  height: auto;
  border-radius: 12px;
  border: 1px solid var(--fe-border);
  background: var(--fe-bg);
  overflow: hidden;
  font-family: var(--fe-font);
}

.tp-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: var(--fe-bg-elev);
  border-bottom: 1px solid var(--fe-border);
}

.tp-spacer { flex: 1 1 auto; }

.tp-btn {
  display: inline-flex;
  align-items: center;
  padding: 5px 10px;
  border-radius: var(--fe-radius-sm);
  border: 1px solid var(--fe-border-strong);
  background: var(--fe-bg);
  color: var(--fe-text);
  font-size: 12px;
  font-weight: 600;
}

.tp-btn--primary {
  background: var(--fe-primary);
  border-color: var(--fe-primary);
  color: var(--fe-text-on-primary);
}

.tp-btn--danger {
  background: transparent;
  border-color: var(--fe-danger);
  color: var(--fe-danger);
}

.tp-body { padding: 10px 12px 12px; }

.tp-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 8px;
  border-radius: var(--fe-radius-sm);
  font-size: 12.5px;
  color: var(--fe-text);
}

.tp-row.is-selected { background: var(--fe-bg-selected); }

.tp-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--fe-text);
  opacity: 0.7;
  flex: none;
}

.tp-dot--primary { background: var(--fe-primary); opacity: 1; }
.tp-dot--muted { background: var(--fe-text-muted); opacity: 1; }

.tp-name { flex: 1 1 auto; }
.tp-meta { color: var(--fe-text-muted); font-size: 11.5px; }

.tp-chips {
  display: flex;
  gap: 6px;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--fe-border-soft);
}

.tp-chip {
  padding: 3px 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  color: var(--fe-bg);
}

.tp-chip--ok { background: var(--fe-ok); }
.tp-chip--warn { background: var(--fe-warning); }
.tp-chip--danger { background: var(--fe-danger); }
</style>
