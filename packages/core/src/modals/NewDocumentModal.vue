<script setup lang="ts">
/**
 * NewDocumentModal — "New document": pick a type, name it, choose where it
 * goes, get an empty file of that type back.
 *
 * ## Why the type list is not in this file
 *
 * The obvious implementation is a constant array of extensions right here.
 * It would be wrong in two directions at once.
 *
 * Downwards: the server is the only party that knows whether it can PRODUCE
 * the bytes. An empty `.md` is an empty file, but an empty `.docx` is a ZIP of
 * XML parts, and if this deployment's binary holds no template for a format
 * then offering it creates a file no program will open. That answer arrives in
 * `capabilities.newdoc_types` (internal/newdoc), and it is the `types` prop.
 *
 * Upwards: the server does NOT know whether this browser can OPEN the result.
 * OnlyOffice may be unconfigured, may be configured but failing its probe, or
 * may be overridden by the embedder to a URL the backend cannot see. So each
 * row names the service its editor needs (`requires`) and the host answers
 * with `onlyOfficeReady` / `drawioReady` — the same resolution the explorer
 * already does for the viewers themselves.
 *
 * ⚠ The rule that falls out of this: **a type nobody here can open is not
 * offered**. Creating `report.docx` on an install with no document server
 * hands somebody a file and then refuses to open it, which is a worse outcome
 * than the entry not being there. When a whole family is withheld this way the
 * dialog says so in one quiet line, because "why is Word missing" should not
 * require reading the source.
 *
 * ## Where "where" is asked
 *
 * ⚠ Not here. This dialog grew its own one-level folder browser — an Up
 * button, a row list, a "Save here" — and `modals/DestinationPickerModal.vue`
 * was then written for Move to / Copy to, which is the same question with the
 * same rules (what counts as writable, whether an unwritable folder is hidden
 * or greyed, what sits above a storage root). Two choosers is how those rules
 * start disagreeing, so there is one: this mounts the picker and keeps only
 * the answer.
 *
 * ## Where the write is checked
 *
 * In three places, and only the last one counts. The browser hides folders the
 * person cannot write to; the dialog refuses to submit into one; the SERVER
 * checks ACL and read-only state and refuses regardless of what arrived. The
 * first two are courtesies that keep somebody from typing a name for thirty
 * seconds before being told no.
 *
 * Name collisions are the same shape: this dialog asks the destination listing
 * first and says "already here" BEFORE the write, and the create endpoint
 * answers 409 anyway.
 */
import { computed, nextTick, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { NewDocType } from '../types/FileNode';
import type { FileApi, ManagerResponse, NewFileResponse } from '../composables/useFileApi';
import { useLocale } from '../composables/useLocale';
import { iconTile, iconFamilyFor, typeLabelFor } from '../lib/fileIcons';
import { crumbsOfWire, permAllowsWrite, splitWire } from '../lib/destinationTree';
import Modal from './Modal.vue';
import DestinationPickerModal from './DestinationPickerModal.vue';
import { inlineKeyStep } from '../lib/direction';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** The host's API wrapper — the dialog does its own listing + create. */
  api: FileApi;
  /**
   * `capabilities.newdoc_types`. Undefined/null on a server that predates the
   * feature; the host should not offer the entry at all in that case, and if
   * it does anyway the dialog degrades to its empty state rather than guessing.
   */
  types?: NewDocType[] | null;
  /** Adapter-qualified folder in view, e.g. `main://docs`. Empty at the root. */
  currentPath: string;
  /** Storage names, for the destination browser's top level. */
  storages?: string[];
  /** Resolved by the host: is there a usable OnlyOffice / drawio? */
  onlyOfficeReady?: boolean;
  drawioReady?: boolean;
  /** Could this person set a missing service up (`capabilities.caller_admin`)?
   *  Adds WHERE to the line that says which families are missing. */
  canConfigure?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'created', file: NewFileResponse): void;
  (e: 'error', payload: { message: string }): void;
}>();

const { t, dir } = useLocale(() => props.locale);

/* ================================================================== types */

function satisfied(ty: NewDocType): boolean {
  if (ty.requires === 'onlyoffice') return !!props.onlyOfficeReady;
  if (ty.requires === 'drawio') return !!props.drawioReady;
  return true;
}

const offered = computed(() => (props.types ?? []).filter(satisfied));

/** Services that are holding a type back, so the dialog can say which. */
const withheld = computed(() => {
  const out: string[] = [];
  for (const ty of props.types ?? []) {
    if (!satisfied(ty) && ty.requires && !out.includes(ty.requires)) out.push(ty.requires);
  }
  return out;
});

/**
 * The withheld services as one sentence.
 *
 * ⚠ Joined in script rather than looped in the template with a `<span> </span>`
 * separator: Vue's compiler drops a whitespace-only text node between
 * elements, so the two sentences rendered welded together
 * ("...here.Diagrams need...") — measured in a browser, invisible in a unit
 * test that asserts on textContent.
 */
const withheldText = computed(() => {
  const parts = withheld.value.map((svc) => t('newdoc.withheld.' + svc));
  /* Someone who can fix it is told where (the owner's rule for a missing
     service, lib/serviceGate); everybody else reads only what is missing. */
  if (parts.length && props.canConfigure) parts.push(t('newdoc.withheld_admin'));
  return parts.join(' ');
});

/** Sections, in the order the server listed them. */
const groups = computed(() => {
  const order: string[] = [];
  const bucket = new Map<string, NewDocType[]>();
  for (const ty of offered.value) {
    if (!bucket.has(ty.group)) {
      bucket.set(ty.group, []);
      order.push(ty.group);
    }
    bucket.get(ty.group)!.push(ty);
  }
  return order.map((g) => ({ group: g, types: bucket.get(g)! }));
});

const selected = ref<string>('');
const selectedType = computed(() => offered.value.find((ty) => ty.ext === selected.value) ?? null);

/**
 * The words for a type come from `typeLabelFor` — the same table the listing's
 * Type column reads. A second table here would be a second answer to "what is
 * a .csv", and the two would disagree the first week somebody edited one.
 */
function labelFor(ext: string): string {
  return typeLabelFor({ type: 'file', extension: ext }, t);
}
function tileFor(ext: string): string {
  return iconTile(iconFamilyFor({ type: 'file', extension: ext, basename: 'x.' + ext }));
}

/* =========================================================== destinations */

const dest = ref('');
/** The picker is up. Also gates Create: a half-answered "where" is not a where. */
const pickingDest = ref(false);

/** Listing cache for the name check. The picker keeps its own for its rows. */
const dirCache = new Map<string, ManagerResponse>();

function canWrite(resp: ManagerResponse | undefined): boolean {
  if (!resp) return false;
  if (resp.read_only) return false;
  return permAllowsWrite(resp.perm as string | undefined);
}

async function loadDir(p: string): Promise<ManagerResponse | undefined> {
  if (dirCache.has(p)) return dirCache.get(p);
  try {
    const resp = await props.api.index(p);
    dirCache.set(p, resp);
    return resp;
  } catch {
    // A folder that cannot be listed is a folder we cannot offer. Silence is
    // right here: the caller renders "you cannot write here" / "no subfolders",
    // and a listing failure inside a picker is not an error the host should toast.
    return undefined;
  }
}

/** `main://a/b` → `main / a / b` — the trail, from the one place that
 *  decomposes a wire path (lib/destinationTree). */
const destLabel = computed(() =>
  dest.value ? crumbsOfWire(dest.value).map((c) => c.label).join(' / ') : '',
);

const destWritable = ref(true);
const destChecked = ref(false);

/* ================================================================== naming */

const name = ref('');
const nameTouched = ref(false);
const collision = ref(false);
const busy = ref(false);
const failure = ref<string | null>(null);

const finalName = computed(() => {
  const base = name.value.trim();
  const ext = selectedType.value?.ext ?? '';
  if (!base || !ext) return base;
  return base.toLowerCase().endsWith('.' + ext) ? base : base + '.' + ext;
});

const nameHasSeparator = computed(() => /[\\/]/.test(name.value));

function freeName(taken: Set<string>, ext: string): string {
  const base = t('newdoc.untitled');
  if (!taken.has((base + '.' + ext).toLowerCase())) return base;
  for (let i = 2; i < 100; i++) {
    const cand = `${base} (${i})`;
    if (!taken.has((cand + '.' + ext).toLowerCase())) return cand;
  }
  return base;
}

function takenNames(resp: ManagerResponse | undefined): Set<string> {
  const s = new Set<string>();
  for (const f of resp?.files ?? []) s.add(f.basename.toLowerCase());
  return s;
}

let checkSeq = 0;
let checkTimer: ReturnType<typeof setTimeout> | null = null;

/**
 * Ask the destination what is already in it: the write permission and the
 * names, in one listing. Debounced because it runs on every keystroke.
 */
async function refreshDestination(opts: { suggest?: boolean } = {}) {
  const seq = ++checkSeq;
  const target = dest.value;
  if (!target) {
    destChecked.value = false;
    collision.value = false;
    return;
  }
  const resp = await loadDir(target);
  if (seq !== checkSeq || target !== dest.value) return;
  destWritable.value = canWrite(resp);
  destChecked.value = true;
  const taken = takenNames(resp);
  const ext = selectedType.value?.ext ?? '';
  if (opts.suggest && !nameTouched.value && ext) {
    name.value = freeName(taken, ext);
  }
  collision.value = !!finalName.value && taken.has(finalName.value.toLowerCase());
}

function scheduleCheck() {
  if (checkTimer) clearTimeout(checkTimer);
  checkTimer = setTimeout(() => void refreshDestination(), 220);
}

/* ================================================================ lifecycle */

function defaultDestination(): string {
  const cur = (props.currentPath || '').trim();
  const [adapter] = splitWire(cur);
  // Standing in a real folder: that folder. Making somebody navigate to where
  // they already are is the small insult that makes a feature feel unfinished.
  if (adapter) return cur;
  const names = props.storages ?? [];
  if (names.length === 1) return names[0] + '://';
  return '';
}

watch(
  () => props.open,
  async (isOpen) => {
    if (!isOpen) return;
    dirCache.clear();
    failure.value = null;
    collision.value = false;
    nameTouched.value = false;
    busy.value = false;
    name.value = '';
    selected.value = offered.value[0]?.ext ?? '';
    dest.value = defaultDestination();
    destChecked.value = false;
    destWritable.value = true;
    /* Nowhere to write yet (several storages and no folder in view): ask
     * first, because a name typed against no destination is a name typed
     * twice. */
    pickingDest.value = !dest.value;
    await refreshDestination({ suggest: true });
  },
);

// A different type means a different extension, so the suggested name has to
// be re-asked against the destination (Untitled.md may be free where
// Untitled.docx is not).
watch(selected, () => {
  void refreshDestination({ suggest: true });
});
watch(name, () => {
  collision.value = false;
  scheduleCheck();
});
watch(dest, () => {
  void refreshDestination({ suggest: true });
});

/* ================================================================== actions */

function pick(ext: string) {
  selected.value = ext;
}

/** Roving focus across the tiles — a radiogroup is expected to move on arrows. */
function onTileKey(e: KeyboardEvent, ext: string) {
  const list = offered.value.map((ty) => ty.ext);
  const i = list.indexOf(ext);
  let next = -1;
  // ⚠ RTL: ← / → move the way they point — in a right-to-left grid the next
  // tile is to the LEFT (lib/direction).
  const step = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : inlineKeyStep(e.key, dir.value);
  if (step) next = (i + step + list.length) % list.length;
  else if (e.key === 'Home') next = 0;
  else if (e.key === 'End') next = list.length - 1;
  if (next < 0) return;
  e.preventDefault();
  selected.value = list[next];
  void nextTick(() => {
    const el = document.querySelector<HTMLElement>(`[data-newdoc-ext="${list[next]}"]`);
    el?.focus();
  });
}

function onDestinationPicked(path: string) {
  dest.value = path;
  pickingDest.value = false;
}

/**
 * ⚠ Escape with the picker open must close the PICKER, not this dialog.
 *
 * `Modal` listens for Escape on `document`, and there are two of them on
 * screen; the outer one registered first, so it hears it first. Swallowing
 * that first close here is what stops one keypress from throwing away a name
 * somebody just typed. (The picker's own Modal then fires, closes the picker,
 * and the flag is already down — the second close is a no-op.)
 */
function onOuterClose() {
  if (pickingDest.value) {
    pickingDest.value = false;
    return;
  }
  emit('close');
}

const canCreate = computed(
  () =>
    !busy.value &&
    !!selectedType.value &&
    !!dest.value &&
    !pickingDest.value &&
    !!name.value.trim() &&
    !nameHasSeparator.value &&
    !collision.value &&
    (!destChecked.value || destWritable.value),
);

async function create() {
  if (!canCreate.value || !selectedType.value) return;
  busy.value = true;
  failure.value = null;
  try {
    const res = await props.api.newFile(dest.value, name.value.trim(), selectedType.value.ext);
    emit('created', res);
  } catch (e) {
    const err = e as Error & { status?: number };
    if (err.status === 409) {
      // The server refused for the reason the dialog warns about. Say it in
      // the same place rather than throwing a toast over the form.
      collision.value = true;
    } else {
      failure.value = err.message || String(e);
      emit('error', { message: failure.value });
    }
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal
    :open="open"
    :title="t('newdoc.title')"
    size="lg"
    :theme="theme"
    @close="onOuterClose"
  >
    <div class="fe-newdoc" data-testid="newdoc-modal">
      <!-- Nothing to offer. Either the server sent no types (too old) or every
           one of them needs a service this install has not got. -->
      <div v-if="!offered.length" class="fe-newdoc__empty" data-testid="newdoc-empty">
        <p class="fe-newdoc__empty-title">{{ t('newdoc.empty.title') }}</p>
        <p class="fe-newdoc__empty-body">
          {{ withheld.length ? t('newdoc.empty.blocked') : t('newdoc.empty.body') }}
        </p>
      </div>

      <template v-else>
        <div
          class="fe-newdoc__types"
          role="radiogroup"
          :aria-label="t('newdoc.type.label')"
        >
          <section v-for="g in groups" :key="g.group" class="fe-newdoc__group">
            <h3 class="fe-newdoc__grouplabel">{{ t('newdoc.group.' + g.group) }}</h3>
            <div class="fe-newdoc__grid">
              <button
                v-for="ty in g.types"
                :key="ty.ext"
                type="button"
                role="radio"
                class="fe-newdoc__type"
                :class="{ 'is-selected': selected === ty.ext }"
                :aria-checked="selected === ty.ext"
                :tabindex="selected === ty.ext ? 0 : -1"
                :data-newdoc-ext="ty.ext"
                :data-testid="'newdoc-type-' + ty.ext"
                @click="pick(ty.ext)"
                @keydown="onTileKey($event, ty.ext)"
              >
                <span class="fe-newdoc__tile" aria-hidden="true" v-html="tileFor(ty.ext)"></span>
                <span class="fe-newdoc__ext">.{{ ty.ext }}</span>
                <span class="fe-newdoc__kind">{{ labelFor(ty.ext) }}</span>
              </button>
            </div>
          </section>

          <!-- Why a family is missing. One quiet line, only when something was
               actually withheld — an operator reading "Office documents need a
               document server" knows what to do; an empty space teaches nobody. -->
          <p v-if="withheldText" class="fe-newdoc__withheld" data-testid="newdoc-withheld">
            {{ withheldText }}
          </p>
        </div>

        <div class="fe-newdoc__field">
          <label class="fe-newdoc__label" for="fe-newdoc-name">{{ t('newdoc.name') }}</label>
          <div class="fe-newdoc__nameline">
            <input
              id="fe-newdoc-name"
              v-model="name"
              type="text"
              class="fe-input fe-newdoc__name"
              autocomplete="off"
              spellcheck="false"
              data-testid="newdoc-name"
              :placeholder="t('newdoc.name.placeholder')"
              @input="nameTouched = true"
              @keydown.enter.prevent="create"
            />
            <span v-if="selectedType" class="fe-newdoc__suffix">.{{ selectedType.ext }}</span>
          </div>
          <p v-if="nameHasSeparator" class="fe-form__error">{{ t('newdoc.err.slash') }}</p>
          <p v-else-if="collision" class="fe-form__error" data-testid="newdoc-collision">
            {{ t('newdoc.err.exists', { name: finalName }) }}
          </p>
        </div>

        <div class="fe-newdoc__field">
          <span class="fe-newdoc__label">{{ t('newdoc.location') }}</span>
          <div class="fe-newdoc__dest">
            <span class="fe-newdoc__destpath" data-testid="newdoc-dest">
              {{ dest ? destLabel : t('newdoc.location.none') }}
            </span>
            <button
              type="button"
              class="fe-newdoc__changebtn"
              data-testid="newdoc-change"
              @click="pickingDest = true"
            >
              {{ t('newdoc.location.change') }}
            </button>
          </div>
          <p
            v-if="destChecked && !destWritable"
            class="fe-form__error"
            data-testid="newdoc-noaccess"
          >
            {{ t('newdoc.location.noaccess') }}
          </p>
        </div>

        <p v-if="failure" class="fe-form__error" data-testid="newdoc-failure">{{ failure }}</p>
      </template>
    </div>

    <template #actions>
      <button type="button" class="fe-btn" @click="onOuterClose">
        {{ t('newdoc.cancel') }}
      </button>
      <button
        v-if="offered.length"
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="!canCreate"
        data-testid="newdoc-create"
        @click="create"
      >
        {{ busy ? t('newdoc.creating') : t('newdoc.create') }}
      </button>
    </template>
  </Modal>

  <!-- tasi:m1 — THE folder chooser, the same component Move to / Copy to
       mount. A SIBLING of the dialog above rather than a child of it: `Modal`
       is not teleported, so nesting would put a `position: fixed` overlay
       inside a card that clips its overflow, and a backdrop click meant for
       the picker would bubble into the dialog's own dismiss. -->
  <DestinationPickerModal
    :open="pickingDest"
    :api="api"
    :locale="locale"
    mode="choose"
    :storages="storages"
    :start-at="dest || currentPath"
    @close="pickingDest = false"
    @pick="onDestinationPicked"
  />
</template>
