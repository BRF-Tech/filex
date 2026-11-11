<script setup lang="ts">
/**
 * UploadersPanel - the person's upload destinations (0.60): make one, change
 * it, give it a new key, delete it. A tab of the user settings dialog, so the
 * web app, the desktop app and every embed draw this one file.
 *
 * An uploader is a key for ONE program (a screenshot tool, a phone shortcut,
 * a script) that puts files into one folder and answers each upload with a
 * link. WHERE the files go and WHAT the link opens are set here, in filex -
 * the program's own settings carry the key and nothing else, so copying them
 * around widens nothing.
 *
 * ⚠⚠ The key leaves the server once, in the answer that made it, together
 * with the client settings the SERVER built from it (`setup`: a ShareX
 * `.sxcu`, a curl line). This panel shows, copies and downloads those as they
 * came; it builds no config of its own, and it drops the key from memory the
 * moment the person is done with it.
 *
 * Every list is the product's one table (DataTable) and every choice is a
 * ChoiceButtons strip or the shared ChoiceSelect - never a native select.
 */
import { computed, nextTick, onMounted, ref } from 'vue';
import DataTable, { type DataColumn } from './DataTable.vue';
import ChoiceButtons, { type ChoiceOption } from './ChoiceButtons.vue';
import ChoiceSelect, { type SelectOption } from './ChoiceSelect.vue';
import type { ContextAction } from './ContextMenu.vue';
import { useLocale } from '../composables/useLocale';
import { copyText, saveTextFile } from '../lib/textOut';
import type { UserSettingsHost } from '../lib/userSettingsHost';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { Uploader, UploaderMade, UploaderVisibility, UploadersList } from '../types/Uploaders';

const props = defineProps<{ host: UserSettingsHost }>();

const { t, formatDate } = useLocale(() => props.host.locale);
const locale = computed(() => props.host.locale as LocaleCode);

const state = ref<UploadersList | null>(null);
const loadError = ref<string | null>(null);
const busy = ref(false);
/** The key and settings just made - held only while the person looks. */
const revealed = ref<UploaderMade | null>(null);
const copied = ref<string | null>(null);
const secretEl = ref<HTMLElement | null>(null);

/**
 * Show a key that was just made, and bring it into view.
 *
 * ⚠ Measured in the browser (0.60, the shots scene): Create is at the foot of
 * the form, the key appears at the head of the pane, and the pane stayed
 * scrolled to the form - the one moment the person must see the key, it was
 * off screen.
 */
async function reveal(made: UploaderMade) {
  revealed.value = made;
  await nextTick();
  secretEl.value?.scrollIntoView?.({ block: 'nearest' });
}
const confirm = ref<{ id: number; what: 'rotate' | 'delete' } | null>(null);

/* ── the form: make one, or change the one being edited ─────────────── */
const editingId = ref<number | null>(null);
const name = ref('');
const storage = ref('');
const folder = ref('');
const visibility = ref<UploaderVisibility>('public');
const life = ref<'longest' | 'days'>('longest');
const days = ref(7);
/** Answer with the link's short address (0.60): offered when an app
 *  shortens links for this person, off by default. */
const shortLink = ref(false);
/** The uploader being edited has it on: the choice stays drawn (to turn it
 *  off) even when no app shortens links any more. */
const shortHeld = ref(false);
const shortOffered = computed(() => state.value?.short_links_available === true || shortHeld.value);

const mayCreate = computed(() => state.value?.may_create !== false);
const mayLink = computed(() => state.value?.may_link_publicly !== false);
const ceiling = computed(() => state.value?.link_max_days ?? 0);

const storageChoices = computed<SelectOption[]>(() =>
  (state.value?.storages ?? []).map((s) => ({ value: s, label: s })),
);
const visibilityChoices = computed<ChoiceOption[]>(() => [
  {
    value: 'public',
    label: t('uploaders.form.public'),
    // The reason a cell is closed travels with it; the chosen cell's meaning
    // is said under the strip.
    help: mayLink.value ? undefined : t('uploaders.form.publicDenied'),
    disabled: !mayLink.value,
  },
  { value: 'private', label: t('uploaders.form.private') },
]);
const shortChoices = computed<ChoiceOption[]>(() => [
  { value: 'off', label: t('uploaders.form.shortOff') },
  { value: 'on', label: t('uploaders.form.shortOn') },
]);
const lifeChoices = computed<ChoiceOption[]>(() => [
  { value: 'longest', label: t('uploaders.form.lifeLongest') },
  { value: 'days', label: t('uploaders.form.lifeDays') },
]);
/** What "Longest allowed" means on this server, said under the strip. */
const lifeHint = computed(() =>
  ceiling.value > 0 ? t('uploaders.form.ceiling', { n: ceiling.value }) : t('uploaders.form.noCeiling'),
);

function resetForm() {
  editingId.value = null;
  name.value = '';
  storage.value = state.value?.storages?.[0] ?? '';
  folder.value = state.value?.default_folder ?? 'uploads';
  visibility.value = mayLink.value ? 'public' : 'private';
  life.value = 'longest';
  days.value = ceiling.value > 0 ? Math.min(7, ceiling.value) : 7;
  shortLink.value = false;
  shortHeld.value = false;
}

async function load() {
  const api = props.host.api;
  if (!api.uploaders) return;
  loadError.value = null;
  try {
    state.value = await api.uploaders();
    if (!editingId.value && !storage.value) resetForm();
  } catch (err) {
    loadError.value = props.host.errorText(err, t('uploaders.loadFailed'));
  }
}
onMounted(load);

function request() {
  return {
    name: name.value.trim(),
    storage: storage.value,
    folder: folder.value.trim(),
    visibility: visibility.value,
    link_days: life.value === 'days' ? Math.max(1, Math.floor(Number(days.value) || 1)) : 0,
    // Only where the choice is drawn: a server without a shortener is sent
    // what it was always sent.
    ...(shortOffered.value ? { short_link: shortLink.value } : {}),
  };
}

async function submit() {
  const api = props.host.api;
  busy.value = true;
  try {
    if (editingId.value != null && api.updateUploader) {
      const saved = await api.updateUploader(editingId.value, request());
      props.host.toast('success', t('uploaders.saved'));
      if (saved.link_days_clamped) props.host.toast('warn', t('uploaders.clamped', { n: saved.uploader.link_days }));
    } else if (api.createUploader) {
      const made = await api.createUploader(request());
      void reveal(made);
      props.host.toast('success', t('uploaders.created'));
      if (made.link_days_clamped) props.host.toast('warn', t('uploaders.clamped', { n: made.uploader.link_days }));
    }
    await load();
    resetForm();
  } catch (err) {
    props.host.toast('error', props.host.errorText(err, t('uploaders.saveFailed')));
  } finally {
    busy.value = false;
  }
}

function edit(u: Uploader) {
  editingId.value = u.id;
  name.value = u.name;
  storage.value = u.storage || state.value?.storages?.[0] || '';
  folder.value = u.folder;
  visibility.value = u.visibility;
  life.value = u.link_days > 0 ? 'days' : 'longest';
  days.value = u.link_days > 0 ? u.link_days : 7;
  shortLink.value = u.short_link === true;
  shortHeld.value = shortLink.value;
}

async function rotate(u: Uploader) {
  const api = props.host.api;
  if (!api.rotateUploaderKey) return;
  if (confirm.value?.id !== u.id || confirm.value.what !== 'rotate') {
    confirm.value = { id: u.id, what: 'rotate' };
    return;
  }
  confirm.value = null;
  busy.value = true;
  try {
    void reveal(await api.rotateUploaderKey(u.id));
    await load();
  } catch (err) {
    props.host.toast('error', props.host.errorText(err, t('uploaders.saveFailed')));
  } finally {
    busy.value = false;
  }
}

async function remove(u: Uploader) {
  const api = props.host.api;
  if (!api.deleteUploader) return;
  if (confirm.value?.id !== u.id || confirm.value.what !== 'delete') {
    confirm.value = { id: u.id, what: 'delete' };
    return;
  }
  confirm.value = null;
  busy.value = true;
  try {
    await api.deleteUploader(u.id);
    // A key shown for the uploader just deleted goes with it.
    if (revealed.value?.uploader.id === u.id) revealed.value = null;
    if (editingId.value === u.id) resetForm();
    props.host.toast('success', t('uploaders.deleted'));
    await load();
  } catch (err) {
    props.host.toast('error', props.host.errorText(err, t('uploaders.saveFailed')));
  } finally {
    busy.value = false;
  }
}

/* ── the one-time key ────────────────────────────────────────────────── */
async function copy(text: string, id: string) {
  if (!(await copyText(text))) return;
  copied.value = id;
  setTimeout(() => {
    if (copied.value === id) copied.value = null;
  }, 1600);
}

function downloadShareX() {
  const r = revealed.value;
  if (!r) return;
  saveTextFile(r.setup.sharex_file || 'filex-uploader.sxcu', JSON.stringify(r.setup.sharex, null, 2) + '\n');
}

function dismiss() {
  revealed.value = null;
  copied.value = null;
}

/* ── the table ───────────────────────────────────────────────────────── */
function whereOf(u: Uploader): string {
  if (!u.available || !u.storage) return t('uploaders.unavailable');
  return u.folder ? `${u.storage}/${u.folder}` : `${u.storage} ${t('uploaders.rootFolder')}`;
}

function linkOf(u: Uploader): string {
  if (u.visibility === 'private') return t('uploaders.linkPrivate');
  return u.link_days > 0 ? t('uploaders.linkPublicDays', { n: u.link_days }) : t('uploaders.linkPublicLongest');
}

function shortDate(v?: string | null): string {
  const ms = new Date(v ?? '').getTime();
  return Number.isNaN(ms) ? '' : formatDate(ms);
}

const columns = computed<DataColumn<Uploader>[]>(() => [
  { id: 'name', label: t('uploaders.col.name'), sortable: true, width: 150, format: (u) => u.name },
  { id: 'where', label: t('uploaders.col.folder'), sortable: true, width: 190, format: whereOf },
  { id: 'link', label: t('uploaders.col.link'), sortable: true, width: 230, format: linkOf },
  {
    id: 'lastUsed',
    label: t('uploaders.col.lastUsed'),
    sortable: true,
    sortDir: 'desc',
    width: 110,
    format: (u) => (u.last_used_at ? shortDate(u.last_used_at) : t('uploaders.neverUsed')),
    sortValue: (u) => (u.last_used_at ? new Date(u.last_used_at).getTime() : null),
  },
]);

function rowActions(u: Uploader): ContextAction[] {
  const armed = (what: 'rotate' | 'delete') => confirm.value?.id === u.id && confirm.value.what === what;
  return [
    { key: 'edit', label: t('uploaders.action.edit'), icon: 'rename', disabled: busy.value || !mayCreate.value },
    {
      key: 'rotate',
      label: armed('rotate') ? t('uploaders.action.rotateConfirm') : t('uploaders.action.rotate'),
      icon: 'refresh',
      disabled: busy.value || !mayCreate.value,
    },
    {
      key: 'delete',
      label: armed('delete') ? t('uploaders.action.deleteConfirm') : t('uploaders.action.delete'),
      icon: 'delete',
      danger: true,
      disabled: busy.value,
    },
  ];
}

function onRowAction(key: string, u: Uploader) {
  if (key === 'edit') edit(u);
  else if (key === 'rotate') void rotate(u);
  else if (key === 'delete') void remove(u);
}

const formInvalid = computed(() => !name.value.trim() || !storage.value);
</script>

<template>
  <div class="fx-up" data-testid="uploaders-panel">
    <p class="fx-us__hint">{{ t('uploaders.lead') }}</p>
    <p v-if="loadError" class="fx-up__warn" role="alert">{{ loadError }}</p>
    <p v-else-if="state && !mayCreate" class="fx-up__warn" data-testid="uploaders-cannot-create">
      {{ t('uploaders.cannotCreate') }}
    </p>

    <!-- ── the one-time key ─────────────────────────────────────────── -->
    <div v-if="revealed" ref="secretEl" class="fx-up__secret" data-testid="uploaders-key">
      <span class="fx-us__label">{{ t('uploaders.key.title', { name: revealed.uploader.name }) }}</span>
      <p class="fx-up__once">{{ t('uploaders.key.once') }}</p>
      <div class="fx-up__pair">
        <code class="fx-up__code" data-testid="uploaders-key-value">{{ revealed.key }}</code>
        <button type="button" class="fx-us__btn" data-testid="uploaders-key-copy" @click="copy(revealed.key, 'key')">
          {{ copied === 'key' ? t('conn.guide.copied') : t('conn.guide.copy') }}
        </button>
      </div>
      <div class="fx-up__setup">
        <button
          type="button"
          class="fx-us__btn fx-us__btn--primary"
          data-testid="uploaders-download-sharex"
          @click="downloadShareX"
        >
          {{ t('uploaders.key.sharex') }}
        </button>
        <span class="fx-us__hint">{{ t('uploaders.key.sharexHint') }}</span>
      </div>
      <span class="fx-us__label">{{ t('uploaders.key.curl') }}</span>
      <div class="fx-up__pair">
        <code class="fx-up__code" data-testid="uploaders-curl">{{ revealed.setup.curl }}</code>
        <button type="button" class="fx-us__btn" data-testid="uploaders-curl-copy" @click="copy(revealed.setup.curl, 'curl')">
          {{ copied === 'curl' ? t('conn.guide.copied') : t('conn.guide.copy') }}
        </button>
      </div>
      <div class="fx-us__actions">
        <button type="button" class="fx-us__btn fx-us__btn--quiet" data-testid="uploaders-key-dismiss" @click="dismiss">
          {{ t('uploaders.key.dismiss') }}
        </button>
      </div>
    </div>

    <!-- ── the uploaders ────────────────────────────────────────────── -->
    <DataTable
      v-if="state"
      table-id="account.uploaders"
      :columns="columns"
      :rows="state.uploaders"
      row-key="id"
      :locale="locale"
      :empty="t('uploaders.empty')"
      :row-class="(u: Uploader) => (u.available ? undefined : 'is-muted')"
      :row-actions="rowActions"
      :row-actions-test-id="(u: Uploader) => `uploaders-actions-${u.id}`"
      @row-action="(key: string, u: Uploader) => onRowAction(key, u)"
    />

    <!-- ── make one / change one ────────────────────────────────────── -->
    <form v-if="state && mayCreate" class="fx-up__form" data-testid="uploaders-form" @submit.prevent="submit">
      <span class="fx-us__label fx-up__title" data-testid="uploaders-form-title">
        {{ editingId != null ? t('uploaders.form.editing', { name }) : t('uploaders.form.new') }}
      </span>
      <div class="fx-us__grid">
        <label class="fx-us__field">
          <span class="fx-us__label">{{ t('uploaders.form.name') }}</span>
          <input
            v-model="name"
            class="fx-us__input"
            maxlength="100"
            :placeholder="t('uploaders.form.namePlaceholder')"
            data-testid="uploaders-name"
          />
        </label>
        <div class="fx-us__field">
          <span class="fx-us__label">{{ t('uploaders.form.storage') }}</span>
          <ChoiceSelect
            :model-value="storage"
            :options="storageChoices"
            :aria-label="t('uploaders.form.storage')"
            testid="uploaders-storage"
            @update:model-value="(v) => (storage = String(v))"
          />
        </div>
      </div>
      <label class="fx-us__field">
        <span class="fx-us__label">{{ t('uploaders.form.folder') }}</span>
        <input v-model="folder" class="fx-us__input" data-testid="uploaders-folder" />
        <span class="fx-us__hint">{{ t('uploaders.form.folderHint') }}</span>
      </label>
      <div class="fx-us__field">
        <span class="fx-us__label">{{ t('uploaders.form.link') }}</span>
        <ChoiceButtons
          :model-value="visibility"
          :options="visibilityChoices"
          segmented
          :aria-label="t('uploaders.form.link')"
          testid-prefix="uploaders-visibility"
          @update:model-value="(v: string | string[]) => (visibility = String(v) as UploaderVisibility)"
        />
        <span class="fx-us__hint">
          {{ visibility === 'public' ? t('uploaders.form.publicHelp') : t('uploaders.form.privateHelp') }}
        </span>
        <span v-if="!mayLink" class="fx-us__hint" data-testid="uploaders-public-denied">
          {{ t('uploaders.form.publicDenied') }}
        </span>
      </div>
      <div v-if="visibility === 'public'" class="fx-us__field">
        <span class="fx-us__label">{{ t('uploaders.form.life') }}</span>
        <div class="fx-up__life">
          <ChoiceButtons
            :model-value="life"
            :options="lifeChoices"
            segmented
            :aria-label="t('uploaders.form.life')"
            testid-prefix="uploaders-life"
            @update:model-value="(v: string | string[]) => (life = String(v) as 'longest' | 'days')"
          />
          <input
            v-if="life === 'days'"
            v-model.number="days"
            type="number"
            min="1"
            :max="ceiling > 0 ? ceiling : 3650"
            class="fx-us__input fx-up__days"
            :aria-label="t('uploaders.form.days')"
            data-testid="uploaders-days"
          />
        </div>
        <span class="fx-us__hint" data-testid="uploaders-ceiling">{{ lifeHint }}</span>
      </div>
      <div v-if="shortOffered" class="fx-us__field" data-testid="uploaders-short">
        <span class="fx-us__label">{{ t('uploaders.form.short') }}</span>
        <ChoiceButtons
          :model-value="shortLink ? 'on' : 'off'"
          :options="shortChoices"
          segmented
          :aria-label="t('uploaders.form.short')"
          testid-prefix="uploaders-short"
          @update:model-value="(v: string | string[]) => (shortLink = String(v) === 'on')"
        />
        <span class="fx-us__hint">{{ t('uploaders.form.shortHelp') }}</span>
      </div>
      <div class="fx-us__actions">
        <button
          v-if="editingId != null"
          type="button"
          class="fx-us__btn fx-us__btn--quiet"
          data-testid="uploaders-cancel"
          @click="resetForm"
        >
          {{ t('uploaders.form.cancel') }}
        </button>
        <button
          type="submit"
          class="fx-us__btn fx-us__btn--primary"
          :disabled="busy || formInvalid"
          data-testid="uploaders-submit"
        >
          {{ editingId != null ? t('uploaders.form.save') : t('uploaders.form.create') }}
        </button>
      </div>
    </form>

  </div>
</template>

<style>
/* ⚠ Only tokens packages/core/src/styles/variables.css declares
   (web/tests/api/themeTokens.test.ts). The fields, buttons and hints are the
   settings dialog's own classes (fx-us__*): this panel is one of its tabs. */
.fx-up {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}
.fx-up__form,
.fx-up__secret {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--fe-border);
  border-radius: 10px;
  background: var(--fe-bg-elev);
}
.fx-up__title {
  font-weight: 600;
}
.fx-up__once {
  margin: 0;
  font-size: 12px;
  color: var(--fe-danger);
}
.fx-up__pair {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.fx-up__code {
  flex: 1;
  min-width: 0;
  padding: 6px 8px;
  border: 1px solid var(--fe-border);
  border-radius: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  overflow-wrap: anywhere;
  user-select: all;
}
.fx-up__setup {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.fx-up__life {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.fx-up__days {
  width: 96px;
}
.fx-up__warn {
  margin: 0;
  font-size: 13px;
  color: var(--fe-danger);
}
</style>
