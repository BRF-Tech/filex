<script setup lang="ts">
/**
 * DestinationPickerModal — "choose a folder", as one component.
 *
 * # Why it exists
 *
 * `Move to…` and `Copy to…` were missing from the selection bar, and the
 * missing part was never the verb: `transferItems` already moves and copies,
 * across storages, and cut+paste has always done it. What did not exist
 * anywhere in the client was a way to SAY WHERE. `dirsOnly` — the backend's
 * folders-only listing — had zero consumers; `api.subfolders()` had been
 * written and never called.
 *
 * # Why a component and not two buttons' private dialog
 *
 * `NewDocumentModal` already grew a private one-level browser for its own
 * "where should this go?" question. A second private copy for Move/Copy is how
 * the two start disagreeing — about what a writable folder is, about whether an
 * unwritable one is hidden or greyed, about what sits above a storage root. So
 * this takes its inputs as props and answers with one event, and the rules live
 * in `lib/destinationTree.ts` where they can be tested without a DOM. Anything
 * that needs a folder — Move, Copy, and the new-document flow next — mounts
 * this.
 *
 * # Contract
 *
 *   <DestinationPickerModal
 *     :open="show" :api="api" :locale="locale"
 *     :storages="['main','s3']"        // drive names; >1 adds a drives level
 *     :start-at="'main://belgeler'"     // where to open (the current folder)
 *     :moving="['main://belgeler/eski']" // folders that may not be the target
 *     mode="move"                        // titles the dialog and its button
 *     @pick="(wirePath) => …"
 *     @close="show = false"
 *   />
 *
 * `pick` emits an adapter-qualified wire path — exactly what `api.copy` and
 * `api.moveAsync` take.
 */
import { computed, ref, watch } from 'vue';
import Modal from './Modal.vue';
import { useLocale } from '../composables/useLocale';
import type { FileApi, ManagerResponse } from '../composables/useFileApi';
import type { LocaleCode } from '../types/ExplorerConfig';
import { fileIconTile, iconTile } from '../lib/fileIcons';
import {
  DRIVES,
  type DestinationRow,
  blockedReason,
  crumbsOfWire,
  destinationRows,
  driveRows,
  initialLocation,
  labelOfWire,
  parentOfWire,
  permAllowsWrite,
} from '../lib/destinationTree';

const props = defineProps<{
  open: boolean;
  api: Pick<FileApi, 'index'>;
  locale: LocaleCode;
  /** Storage names. More than one adds a drives level above the roots. */
  storages?: string[];
  /** Wire path to open at — normally the folder the user is looking at. */
  startAt?: string;
  /**
   * Folders that must not be offered: what is being moved. A destination equal
   * to one of these, or inside one, is shown greyed with a reason rather than
   * hidden, because a folder that silently vanishes reads as data loss.
   */
  moving?: string[];
  /** Titles the dialog and names its confirm button. */
  mode?: 'move' | 'copy' | 'choose';
  /**
   * What the answer is. `dir` (the default) is the folder you are standing in;
   * `file` lists the files too and the answer is the one you tick — an app
   * plugin's `file-chooser` asks for this. A file pick has no write check:
   * the chooser reads, it never writes.
   */
  pick?: 'dir' | 'file';
  /** The parent is running the operation: the dialog stays up but inert. */
  busy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'pick', path: string): void;
}>();

const { t } = useLocale(() => props.locale);

const at = ref<string>(DRIVES);
const rows = ref<DestinationRow[]>([]);
const loading = ref(false);
/** The listing of `at` itself — what says whether the CURRENT folder is writable. */
const here = ref<ManagerResponse | undefined>(undefined);
const failed = ref(false);

const multiDrive = computed(() => (props.storages?.length ?? 0) > 1);
const drivesLabel = computed(() => t('destpicker.drives'));
const pickingFile = computed(() => props.pick === 'file');
/** The file ticked in this folder — file pick only. */
const picked = ref<string | null>(null);

/**
 * One listing cache for the life of the dialog.
 *
 * Walking up and back down is the normal way people use a picker, and without
 * this every Up button press costs another round trip to a storage that may be
 * an S3 bucket on another continent.
 */
let cache = new Map<string, ManagerResponse>();

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return;
    cache = new Map();
    void goTo(initialLocation(props.startAt, props.storages));
  },
  { immediate: true },
);

async function load(path: string): Promise<ManagerResponse | undefined> {
  const hit = cache.get(path);
  if (hit) return hit;
  try {
    const resp = await props.api.index(path);
    cache.set(path, resp);
    return resp;
  } catch {
    // A folder that cannot be listed is a folder that cannot be offered. The
    // dialog says so in its own body; a listing failure inside a picker is not
    // an error the host should be made to toast.
    return undefined;
  }
}

async function goTo(path: string): Promise<void> {
  at.value = path;
  failed.value = false;
  picked.value = null;
  if (path === DRIVES) {
    here.value = undefined;
    rows.value = driveRows(props.storages, props.moving);
    return;
  }
  loading.value = true;
  const resp = await load(path);
  // A second navigation may have started while this listing was in flight;
  // only the newest one gets to write the rows.
  if (at.value !== path) return;
  loading.value = false;
  here.value = resp;
  failed.value = !resp;
  rows.value = destinationRows(resp?.files, props.moving, { files: pickingFile.value });
}

const parent = computed(() => parentOfWire(at.value, multiDrive.value));
const crumbs = computed(() => crumbsOfWire(at.value));

/** Why the CURRENT folder cannot be chosen, or null when it can. */
const hereBlocked = computed(() => blockedReason(at.value, props.moving));

const hereWritable = computed(() => {
  if (at.value === DRIVES) return false;
  if (!here.value) return false;
  if (here.value.read_only) return false;
  return permAllowsWrite(here.value.perm as string | undefined);
});

const canChoose = computed(() => {
  if (props.busy || at.value === DRIVES) return false;
  if (pickingFile.value) return picked.value !== null;
  return hereWritable.value && hereBlocked.value === null;
});

/**
 * Is the line under the list a REFUSAL or a prompt?
 *
 * "Open a storage to choose a folder inside it" is not a mistake anybody made —
 * it is the drives level telling you what to do next. Painting it in the danger
 * colour, which is what happened the first time this was drawn, makes the very
 * first screen of the dialog look like something has gone wrong.
 */
const reasonIsRefusal = computed(() => at.value !== DRIVES && !(pickingFile.value && !failed.value));

/** The one line under the list that says why Choose is off, if it is. */
const reason = computed<string>(() => {
  if (at.value === DRIVES) return t('destpicker.pick_a_drive');
  if (pickingFile.value) {
    if (failed.value) return t('destpicker.unreadable');
    return picked.value === null ? t('destpicker.pick_a_file') : '';
  }
  if (hereBlocked.value === 'self') return t('destpicker.blocked.self');
  if (hereBlocked.value === 'descendant') return t('destpicker.blocked.descendant');
  if (failed.value) return t('destpicker.unreadable');
  if (here.value && !hereWritable.value) return t('destpicker.readonly_here');
  return '';
});

const title = computed(() => {
  if (props.mode === 'move') return t('destpicker.title.move');
  if (props.mode === 'copy') return t('destpicker.title.copy');
  return pickingFile.value ? t('destpicker.title.choose_file') : t('destpicker.title.choose');
});

const confirmLabel = computed(() => {
  if (props.mode === 'move') return t('destpicker.confirm.move');
  if (props.mode === 'copy') return t('destpicker.confirm.copy');
  return pickingFile.value ? t('destpicker.confirm.choose_file') : t('destpicker.confirm.choose');
});

/** What the line under the list names: the ticked file, else the folder. */
const targetName = computed(() =>
  pickingFile.value && picked.value !== null
    ? labelOfWire(picked.value, drivesLabel.value)
    : labelOfWire(at.value, drivesLabel.value),
);

function rowIcon(row: DestinationRow): string {
  return row.kind === 'file' ? fileIconTile({ type: 'file', basename: row.label }) : iconTile('folder');
}

function rowTitle(row: DestinationRow): string {
  if (row.kind === 'file') return row.label;
  if (row.blocked === 'self') return t('destpicker.blocked.self');
  if (row.blocked === 'descendant') return t('destpicker.blocked.descendant');
  if (!row.writable) return t('destpicker.readonly');
  return row.label;
}

function openRow(row: DestinationRow): void {
  // A file is the answer, not a place to go.
  if (row.kind === 'file') {
    picked.value = row.path;
    return;
  }
  // A blocked row is a dead end — everything under it is blocked too — so
  // walking into it would be navigation that cannot end anywhere.
  if (row.blocked) return;
  void goTo(row.path);
}

function choose(): void {
  if (!canChoose.value) return;
  emit('pick', pickingFile.value && picked.value !== null ? picked.value : at.value);
}
</script>

<template>
  <Modal
    :open="open"
    :title="title"
    size="md"
    @close="emit('close')"
  >
    <div class="fe-destpick" data-testid="destpicker">
      <div class="fe-destpick__bar">
        <button
          type="button"
          class="fe-destpick__up"
          :disabled="!parent"
          :title="t('destpicker.up')"
          :aria-label="t('destpicker.up')"
          data-testid="destpicker-up"
          @click="goTo(parent as string)"
        >
          <svg
            class="fe-ficon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
            focusable="false"
          >
            <path d="M12 19V5M5 12l7-7 7 7" />
          </svg>
        </button>
        <nav class="fe-destpick__crumbs" :aria-label="t('destpicker.crumbs')">
          <button
            v-if="multiDrive"
            type="button"
            class="fe-destpick__crumb"
            :class="{ 'is-current': at === DRIVES }"
            @click="goTo(DRIVES)"
          >
            {{ drivesLabel }}
          </button>
          <template v-for="c in crumbs" :key="c.path">
            <span class="fe-destpick__sep" aria-hidden="true">/</span>
            <button
              type="button"
              class="fe-destpick__crumb"
              :class="{ 'is-current': c.path === at }"
              @click="goTo(c.path)"
            >
              {{ c.label }}
            </button>
          </template>
          <span v-if="!multiDrive && !crumbs.length" class="fe-destpick__crumb is-current">
            {{ labelOfWire(at, drivesLabel) }}
          </span>
        </nav>
      </div>

      <ul class="fe-destpick__rows" data-testid="destpicker-rows">
        <li v-if="loading" class="fe-destpick__note">{{ t('destpicker.loading') }}</li>
        <li v-else-if="failed" class="fe-destpick__note">{{ t('destpicker.unreadable') }}</li>
        <li v-else-if="!rows.length" class="fe-destpick__note">
          {{ pickingFile ? t('destpicker.empty_files') : t('destpicker.empty') }}
        </li>
        <li v-for="row in rows" :key="row.path">
          <button
            type="button"
            class="fe-destpick__row"
            :class="{
              'is-locked': !row.writable,
              'is-blocked': !!row.blocked,
              'is-file': row.kind === 'file',
              'is-picked': row.kind === 'file' && picked === row.path,
            }"
            :disabled="!!row.blocked"
            :title="rowTitle(row)"
            :aria-pressed="row.kind === 'file' ? picked === row.path : undefined"
            :data-testid="'destpicker-row-' + row.label"
            @click="openRow(row)"
          >
            <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
            <span class="fe-destpick__rowicon" aria-hidden="true" v-html="rowIcon(row)"></span>
            <span class="fe-destpick__rowname"><bdi>{{ row.label }}</bdi></span>
            <span v-if="row.blocked" class="fe-destpick__rowtag">{{ t('destpicker.tag.blocked') }}</span>
            <span v-else-if="!row.writable" class="fe-destpick__rowtag">{{ t('destpicker.readonly') }}</span>
            <svg
              v-if="row.kind === 'dir'"
              class="fe-destpick__rowinto"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
              focusable="false"
            >
              <path d="M9 5l7 7-7 7" />
            </svg>
          </button>
        </li>
      </ul>

      <p
        v-if="reason"
        class="fe-destpick__reason"
        :class="{ 'is-hint': !reasonIsRefusal }"
        data-testid="destpicker-reason"
      >
        {{ reason }}
      </p>
      <p v-else class="fe-destpick__target" data-testid="destpicker-target">
        {{ t('destpicker.target', { name: targetName }) }}
      </p>
    </div>

    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('destpicker.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="!canChoose"
        data-testid="destpicker-confirm"
        @click="choose"
      >
        {{ confirmLabel }}
      </button>
    </template>
  </Modal>
</template>
