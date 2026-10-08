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
 * Name collisions are the same shape, with one difference: whether a name is
 * taken, and which free name to offer instead, is asked of the SERVER
 * (`newFileCheck`, the create's own dry run - #211 audit B18), which answers
 * with the existence check the create makes and its own " (2)" numbering. The
 * dialog used to lower-case the listing itself, and so refused "Report.docx"
 * beside "report.docx" on a store where the two are different files. The
 * create endpoint answers 409 anyway.
 *
 * ## The name is the whole name (#56)
 *
 * The field holds the file name, extension and all. The TYPE decides the
 * bytes the server writes and the editor the file opens in; it only prefills
 * the name (`Untitled.txt`). It used to draw the extension as a read-only
 * suffix beside the field, which made `LICENSE`, `Makefile`, `test.conf` and
 * `example.custom` impossible to create. A type whose editor finds it BY
 * extension (office documents, diagrams — `ext_required`) still keeps it: the
 * dialog says "will be created as report.docx" instead of locking the field.
 * The rules are lib/newDocName; this file wires them.
 *
 * ## It makes a DRAFT (issue #71)
 *
 * On a server that keeps drafts for this person (`drafts`, from
 * `capabilities.drafts`) Create no longer writes the file where it was asked
 * for: it writes a draft — the same bytes, in the person's own drafts area of
 * that storage — and the editor opens on it. Nothing appears in the folder
 * until the draft is saved; a person who changes their mind leaves nothing
 * behind. At the draft limit Create is refused and the dialog says so, with
 * the way to Drafts; it never falls back to creating the file instead.
 */
import { computed, nextTick, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { NewDocType } from '../types/FileNode';
import type { FileApi, ManagerResponse, NewFileCheck, NewFileResponse } from '../composables/useFileApi';
import { useLocale } from '../composables/useLocale';
import { iconTile, iconFamilyFor, typeLabelFor } from '../lib/fileIcons';
import { crumbsOfWire, permAllowsWrite, splitWire } from '../lib/destinationTree';
import Modal from './Modal.vue';
import DestinationPickerModal from './DestinationPickerModal.vue';
import { draftLimitOf, isDraftLimit } from '../lib/drafts';
import { inlineKeyStep } from '../lib/direction';
import { labelOf } from '../lib/pluginLabel';
import {
  docNameProblem,
  extLocked,
  finalDocName,
  retypeDocName,
  stemEnd,
} from '../lib/newDocName';

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
  /** Make a DRAFT rather than the file (issue #71): the server keeps drafts
   *  for this person (`capabilities.drafts`). Absent: the file is created. */
  drafts?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'created', file: NewFileResponse): void;
  (e: 'error', payload: { message: string }): void;
  /** Drafts: the person asked to see their drafts (from the limit message). */
  (e: 'open-drafts'): void;
}>();

const { t, dir } = useLocale(() => props.locale);

/* ================================================================== types */

/** A row's identity: its key (an app's row), else its extension. */
function keyOf(ty: NewDocType): string {
  return ty.key || ty.ext;
}

function satisfied(ty: NewDocType): boolean {
  if (ty.requires === 'onlyoffice') return !!props.onlyOfficeReady;
  if (ty.requires === 'drawio') return !!props.drawioReady;
  return true;
}

const offered = computed(() => (props.types ?? []).filter(satisfied));

/** Services that are holding a type back, so the dialog can say which.
 *  A kind an app's row makes anyway (draw.io's app for `.drawio`) is not
 *  missing: saying "Diagrams need draw.io" beside the app's own .drawio row
 *  would contradict the row. */
const withheld = computed(() => {
  const madeByApps = new Set(offered.value.filter((ty) => ty.app).map((ty) => ty.ext));
  const out: string[] = [];
  for (const ty of props.types ?? []) {
    if (satisfied(ty) || !ty.requires || ty.requires === 'app' || madeByApps.has(ty.ext)) continue;
    if (!out.includes(ty.requires)) out.push(ty.requires);
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
const selectedType = computed(() => offered.value.find((ty) => keyOf(ty) === selected.value) ?? null);

/**
 * The words for a type come from `typeLabelFor` — the same table the listing's
 * Type column reads. A second table here would be a second answer to "what is
 * a .csv", and the two would disagree the first week somebody edited one.
 */
function labelFor(ty: NewDocType): string {
  // An app's row is said in the app's own words (its manifest's label).
  if (ty.app) return labelOf(ty.app.label, props.locale) || ty.ext;
  return typeLabelFor({ type: 'file', extension: ty.ext }, t);
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
/** Drafts: the person already keeps as many drafts as the server allows —
 *  the number, for the sentence. Null: not refused for that. */
const draftLimitHit = ref<number | null>(null);

/** The name the server will write: what the collision check and the
 *  "will be created as" line quote. */
const finalName = computed(() =>
  selectedType.value ? finalDocName(name.value, selectedType.value) : name.value.trim(),
);

/** Why the typed name cannot be created — the server's own refusals, said
 *  before the click (lib/newDocName). */
const nameProblem = computed(() =>
  selectedType.value ? docNameProblem(name.value, selectedType.value, props.types ?? []) : null,
);

const nameError = computed(() => {
  const p = nameProblem.value;
  if (!p) return '';
  switch (p.kind) {
    case 'slash':
      return t('newdoc.err.slash');
    case 'dots':
      return t('modal.newfolder.invalid');
    case 'bare_ext':
      return t('newdoc.err.bare_ext');
    case 'reserved':
      return t('names.reserved', { name: p.name });
    case 'ext_needs_type':
      return t('newdoc.err.ext_needs_type', { ext: p.ext });
  }
  return '';
});

/**
 * A type whose editor needs its extension, and a name without it: the server
 * will add it, and the person reads that BEFORE Create rather than finding
 * `report.docx` afterwards. Also what a server from before #56 gets for every
 * type (lib/newDocName `extLocked`).
 */
const extHint = computed(() => {
  const ty = selectedType.value;
  const typed = name.value.trim();
  if (!ty || !typed || nameProblem.value || !extLocked(ty)) return '';
  return finalName.value === typed ? '' : t('newdoc.hint.ext_added', { name: finalName.value });
});

/**
 * The server's answer for a name in a folder (NewFileCheck), or null when it
 * cannot be asked - a host api without the dry run, a name it would refuse
 * anyway (said by `nameProblem`), a folder that cannot be written. Null is
 * "not known to be taken": the create still answers 409.
 */
async function checkName(dir: string, typed: string, ty: NewDocType): Promise<NewFileCheck | null> {
  const check = props.api.newFileCheck;
  if (typeof check !== 'function' || !typed.trim()) return null;
  try {
    return await check(dir, typed.trim(), keyOf(ty));
  } catch {
    return null;
  }
}

let checkSeq = 0;
let checkTimer: ReturnType<typeof setTimeout> | null = null;

/**
 * Ask the destination whether it may be written (its listing) and the server
 * whether the name is free there (a dry run of the create). Debounced
 * because it runs on every keystroke. An untouched name is the server's
 * first free `Untitled (n).<ext>`.
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
  const ty = selectedType.value;
  if (!ty) {
    collision.value = false;
    return;
  }
  if (opts.suggest && !nameTouched.value) {
    const first = `${t('newdoc.untitled')}.${ty.ext}`;
    const answer = await checkName(target, first, ty);
    if (seq !== checkSeq || target !== dest.value) return;
    name.value = answer?.taken && answer.suggested ? answer.suggested : first;
  }
  if (!finalName.value || nameProblem.value) {
    collision.value = false;
    return;
  }
  const answer = await checkName(target, name.value, ty);
  if (seq !== checkSeq || target !== dest.value) return;
  collision.value = answer?.taken === true;
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
    draftLimitHit.value = null;
    collision.value = false;
    nameTouched.value = false;
    busy.value = false;
    name.value = '';
    selected.value = offered.value[0] ? keyOf(offered.value[0]) : '';
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

// A different type means a different default extension. What the person typed
// is kept — only the previous type's default is swapped (lib/newDocName
// retypeDocName) — and an untouched suggestion is re-asked against the
// destination (Untitled.md may be free where Untitled.docx is not).
watch(selected, (next, prev) => {
  const nextType = offered.value.find((ty) => keyOf(ty) === next);
  const prevType = offered.value.find((ty) => keyOf(ty) === prev) ?? null;
  if (nextType && name.value) name.value = retypeDocName(name.value, prevType, nextType);
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

function pick(key: string) {
  selected.value = key;
}

/** Roving focus across the tiles — a radiogroup is expected to move on arrows. */
function onTileKey(e: KeyboardEvent, key: string) {
  const list = offered.value.map(keyOf);
  const i = list.indexOf(key);
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
    const el = document.querySelector<HTMLElement>(`[data-newdoc-key="${list[next]}"]`);
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
    !nameProblem.value &&
    !collision.value &&
    (!destChecked.value || destWritable.value),
);

/* ================================================================ the field */

/**
 * Focusing the name selects the STEM, the way a rename does: typing replaces
 * `Untitled` and keeps `.txt`. A name with no extension (`LICENSE`) selects
 * whole.
 *
 * ⚠ A mouse click focuses on mousedown and then places the caret on mouseup,
 * which would undo the selection; the first click's mouseup is therefore
 * swallowed and the stem re-selected. A click into an already-focused field
 * is the person placing the caret, and is left alone.
 */
let reselectOnMouseUp = false;
function selectStem(el: HTMLInputElement) {
  try {
    el.setSelectionRange(0, stemEnd(el.value));
  } catch {
    /* an input type without a selection — nothing to select */
  }
}
function onNameFocus(e: FocusEvent) {
  selectStem(e.target as HTMLInputElement);
}
function onNameMouseDown(e: MouseEvent) {
  reselectOnMouseUp = document.activeElement !== e.target;
}
function onNameMouseUp(e: MouseEvent) {
  if (!reselectOnMouseUp) return;
  reselectOnMouseUp = false;
  e.preventDefault();
  selectStem(e.target as HTMLInputElement);
}

async function create() {
  if (!canCreate.value || !selectedType.value) return;
  busy.value = true;
  failure.value = null;
  draftLimitHit.value = null;
  try {
    // exactName: the field IS the file name (#56). The server still adds the
    // extension a type's editor needs, which extHint has already said.
    const ty = selectedType.value;
    const res = props.drafts
      ? await props.api.drafts.create(dest.value, name.value.trim(), keyOf(ty), { exactName: true })
      : await props.api.newFile(dest.value, name.value.trim(), keyOf(ty), {
          exactName: true,
        });
    // An app's row opens in the app's view it names, whatever else opens
    // that extension.
    emit('created', ty.app ? { ...res, app: { plugin: ty.app.plugin, view: ty.app.view } } : res);
  } catch (e) {
    const err = e as Error & { status?: number };
    if (isDraftLimit(err)) {
      // ⚠ Said here, where it was pressed, with the way to Drafts — and
      // nothing else is created in its place (the owner's rule, issue #71).
      draftLimitHit.value = draftLimitOf(err) ?? 0;
    } else if (err.status === 409) {
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
                :key="keyOf(ty)"
                type="button"
                role="radio"
                class="fe-newdoc__type"
                :class="{ 'is-selected': selected === keyOf(ty) }"
                :aria-checked="selected === keyOf(ty)"
                :tabindex="selected === keyOf(ty) ? 0 : -1"
                :data-newdoc-ext="ty.ext"
                :data-newdoc-key="keyOf(ty)"
                :data-testid="`newdoc-type-${keyOf(ty)}`"
                @click="pick(keyOf(ty))"
                @keydown="onTileKey($event, keyOf(ty))"
              >
                <span class="fe-newdoc__tile" aria-hidden="true" v-html="tileFor(ty.ext)"></span>
                <span class="fe-newdoc__ext">.{{ ty.ext }}</span>
                <span class="fe-newdoc__kind">{{ labelFor(ty) }}</span>
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
          <!-- #56 — the whole file name, extension included. There used to be
               a read-only ".txt" beside the field, which is why LICENSE could
               not be made. -->
          <input
            id="fe-newdoc-name"
            v-model="name"
            type="text"
            class="fe-input fe-newdoc__name"
            autocomplete="off"
            spellcheck="false"
            data-testid="newdoc-name"
            :placeholder="t('newdoc.name.placeholder')"
            :aria-describedby="nameError || extHint ? 'fe-newdoc-name-note' : undefined"
            @input="nameTouched = true"
            @focus="onNameFocus"
            @mousedown="onNameMouseDown"
            @mouseup="onNameMouseUp"
            @keydown.enter.prevent="create"
          />
          <p
            v-if="nameError"
            id="fe-newdoc-name-note"
            class="fe-form__error"
            data-testid="newdoc-name-error"
          >
            {{ nameError }}
          </p>
          <p v-else-if="collision" class="fe-form__error" data-testid="newdoc-collision">
            {{ t('newdoc.err.exists', { name: finalName }) }}
          </p>
          <p
            v-else-if="extHint"
            id="fe-newdoc-name-note"
            class="fe-newdoc__hint"
            data-testid="newdoc-ext-hint"
          >
            {{ extHint }}
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

        <p v-if="drafts && !draftLimitHit" class="fe-newdoc__hint" data-testid="newdoc-draft-hint">
          {{ t('newdoc.hint.draft') }}
        </p>
        <div v-if="draftLimitHit !== null" class="fe-newdoc__limit" role="alert" data-testid="newdoc-draft-limit">
          <p class="fe-form__error">
            {{ draftLimitHit ? t('newdoc.err.draft_limit', { limit: draftLimitHit }) : t('newdoc.err.draft_limit_any') }}
          </p>
          <button type="button" class="fe-btn fe-btn--sm" data-testid="newdoc-open-drafts" @click="emit('open-drafts')">
            {{ t('newdoc.open_drafts') }}
          </button>
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
