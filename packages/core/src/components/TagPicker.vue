<script setup lang="ts">
import { requestFailure } from '../lib/errorWords';
/**
 * TagPicker — small chip list with add/remove for a node.
 *
 * Reads `GET /api/files/manager/tags?node_id=…` on mount, writes the
 * full new set via `POST /api/files/manager/tags`.
 *
 * Tag colors are deterministic: hash(name) → palette index. Consumers
 * can override with the `palette` prop.
 *
 * === etiket:k2 — TWO KINDS, AND THE CHIP SAYS WHICH (v0.43.0) ===========
 * ⚠⚠ Tester, 2026-09-22: a non-admin's tag "müşteri teklifi" appeared in
 * another user's and the admin's panel and on the file, and the other user
 * could remove it. Tags were one shared label per file while the code called
 * them per-user, and this component never said a word about who could see
 * one. The owner's decision: personal and team tags.
 *
 *   - Every chip carries its kind — `TagKindIcon` plus the words in its title
 *     and accessible name — so nobody has to guess who sees it.
 *   - Adding asks WHO SEES IT, with the answer written under each choice.
 *     Personal is the default: a label nobody chose to share is not shared.
 *   - "Team" is offered only where saving it would succeed: the server says
 *     `can_edit_team` (edit permission on the file). For a viewer it stays on
 *     screen, disabled, with the reason — a choice that vanishes cannot tell
 *     the person it exists. A team chip's × is likewise absent for them.
 *   - The name keeps its capitals ("Müşteri Teklifi"); the server decides
 *     sameness (lib/tags `tagKey` mirrors it only to avoid a pointless POST).
 *   - An OLDER server (no `items` in its answer) has one shared kind: the
 *     chips read as team and the kind choice is not offered, because the old
 *     server would ignore it and the person would believe a label was private
 *     when it is not.
 *
 * === etiket:t1 — THE CHIP IS A DOOR ==================================
 * ⚠⚠ Owner, 2026-09-13: "taglediğim dosya klasör tag'ine gitmiyor." The tag
 * VIEW existed and the panel listed every tag, but from a file there was no way
 * into the one it carries: the chip was a `<span>`, so the only route from
 * "this file is tagged invoices" to "what else is tagged invoices" was to read
 * the word, find it again in the panel and click it there.
 *
 * ⚠ The name is the button and the × stays its own. Two targets in one chip,
 * neither of which may fire the other's action — a mis-hit here either loses a
 * tag or navigates away from the file you were reading about.
 *
 * ⚠ It navigates from BOTH mounts, the details panel's and the modal the
 * right-click menu opens. One component, one behaviour: a chip that led
 * somewhere in one place and was inert three inches away in another would be
 * the surface-specific split this package exists to avoid. The host closes the
 * modal on the way (it opened it).
 */
import { ref, watch, onMounted, computed } from 'vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';
import { announceTagsChanged, tagItemsOf, tagKey, type TagItem, type TagKind } from '../lib/tags';
import TagKindIcon from './TagKindIcon.vue';
import ChoiceButtons, { type ChoiceOption } from './ChoiceButtons.vue';

const props = defineProps<{
  nodeId: number;
  /**
   * ⚠ Added 2026-09-13 with the chip's new "open this tag" affordance, and it
   * pulls three OLDER strings out of English with it: this component printed
   * "+ Add tag", "tag name" and "Remove tag" literally, so a Turkish user
   * reading "Etiketler" as the section heading got three English controls under
   * it. Optional, defaulting to the catalogue's own default, so an embedder
   * that never passed one is unaffected.
   */
  locale?: LocaleCode;
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  /** Credentials mode, from the explorer's auth kind. ⚠ Defaults to
   *  'same-origin': a credentialed cross-origin request cannot be answered
   *  with ACAO:* , so hardcoding 'include' broke this call in every embed
   *  served from a different origin to the API. */
  authCredentials?: RequestCredentials;
  /** Optional list of palette swatches; one is picked per tag deterministically. */
  palette?: string[];
}>();

const emit = defineEmits<{
  /** The names (the pre-v0.43 payload, kept for embedders), then the items. */
  (e: 'change', tags: string[], items: TagItem[]): void;
  (e: 'error', message: string): void;
  /** etiket:t1 — "show me everything tagged this". The host owns navigation.
   *  `kind` (v0.43) says WHICH of two same-named tags was clicked. */
  (e: 'open', tag: string, kind: TagKind): void;
}>();

const { t } = useLocale(() => props.locale ?? 'en');

/** "Open tag: invoices · Personal tag …" — the chip's title and its accessible
 *  name, one value so the two cannot say different things. The kind sentence
 *  rides along, so a screen reader hears WHO sees the tag, not only its name. */
function openTitle(item: TagItem): string {
  return `${t('tags.open', { tag: item.name })} · ${t(`tags.chip.${item.kind}`, { tag: item.name })}`;
}

const items = ref<TagItem[]>([]);
/** Whether TEAM tags may be added or removed here (server: `can_edit_team`). */
const canEditTeam = ref(true);
/** The server predates kinds: one shared kind, no choice to offer. */
const legacy = ref(false);
const loading = ref(false);
const adding = ref(false);
const newTag = ref('');
const newKind = ref<TagKind>('personal');
const addForm = ref<HTMLFormElement | null>(null);

/** Personal chips first, then team — the grouping the navigation panel uses,
 *  so the same file reads the same in every place. */
const shown = computed(() => [
  ...items.value.filter((i) => i.kind === 'personal'),
  ...items.value.filter((i) => i.kind === 'team'),
]);

const kindOptions = computed<ChoiceOption[]>(() => [
  { value: 'personal', label: t('tags.kind.personal'), help: t('tags.kind.personal_help') },
  {
    value: 'team',
    label: t('tags.kind.team'),
    help: canEditTeam.value ? t('tags.kind.team_help') : t('tags.kind.team_locked'),
    disabled: !canEditTeam.value,
  },
]);

const palette = computed(() => props.palette ?? [
  '#ef4444', '#f59e0b', '#10b981', '#3b82f6',
  '#8b5cf6', '#ec4899', '#14b8a6', '#f97316',
]);

function colorFor(tag: string): string {
  let h = 0;
  for (let i = 0; i < tag.length; i++) h = (h * 31 + tag.charCodeAt(i)) | 0;
  const arr = palette.value;
  return arr[Math.abs(h) % arr.length];
}

/** A team chip a viewer cannot take off draws no ×. */
function canRemove(item: TagItem): boolean {
  return item.kind === 'personal' || canEditTeam.value;
}

async function buildHeaders(extra: Record<string, string> = {}): Promise<Record<string, string>> {
  return { ...(await (props.authHeaders ?? (() => ({})))()), ...extra };
}

function absorb(body: unknown) {
  const b = (body ?? {}) as { items?: unknown; can_edit_team?: unknown };
  legacy.value = !Array.isArray(b.items);
  items.value = tagItemsOf(body);
  if (typeof b.can_edit_team === 'boolean') canEditTeam.value = b.can_edit_team;
}

async function load() {
  loading.value = true;
  try {
    const base = props.apiBase ?? '';
    const res = await fetch(`${base}/api/files/manager/tags?node_id=${props.nodeId}`, {
      headers: await buildHeaders(),
      credentials: props.authCredentials ?? 'same-origin',
    });
    if (res.ok) absorb(await res.json());
  } catch (err) {
    emit('error', err instanceof Error ? err.message : String(err));
  } finally {
    loading.value = false;
  }
}

async function persist(next: TagItem[]) {
  const previous = [...items.value];
  items.value = next;
  try {
    const base = props.apiBase ?? '';
    // ⚠ An older server gets the old shape: it would ignore `items` and read
    // the missing `tags` as "clear every tag".
    const body = legacy.value
      ? { node_id: props.nodeId, tags: next.map((i) => i.name) }
      : { node_id: props.nodeId, items: next };
    const res = await fetch(`${base}/api/files/manager/tags`, {
      method: 'POST',
      headers: await buildHeaders({ 'Content-Type': 'application/json' }),
      credentials: props.authCredentials ?? 'same-origin',
      body: JSON.stringify(body),
    });
    if (!res.ok) throw requestFailure(res.status, await res.text().catch(() => ''), props.locale);
    // The server's answer is the truth: it keeps the FIRST spelling of a name
    // ("müşteri teklifi" typed on a second file lands as "Müşteri Teklifi").
    const answer: unknown = await res.json().catch(() => null);
    if (!legacy.value && answer && Array.isArray((answer as { items?: unknown }).items)) {
      items.value = tagItemsOf(answer);
    }
    // ⚠ Announced, not only emitted: if the dialog was closed while the
    // request was in flight this component is gone and Vue drops its emit —
    // the panel would keep the list from before the save (lib/tags).
    announceTagsChanged();
    emit('change', items.value.map((i) => i.name), items.value);
  } catch (err) {
    items.value = previous;
    emit('error', err instanceof Error ? err.message : String(err));
  }
}

function closeAdd() {
  newTag.value = '';
  newKind.value = 'personal';
  adding.value = false;
}

function add() {
  const v = newTag.value.trim();
  const kind: TagKind = legacy.value ? 'team' : newKind.value;
  if (!v || items.value.some((i) => i.kind === kind && tagKey(i.name) === tagKey(v))) {
    closeAdd();
    return;
  }
  void persist([...items.value, { name: v, kind }]);
  closeAdd();
}

/**
 * What leaving the name field does.
 *
 * ⚠⚠ Before kinds, leaving the field SAVED what was typed. With a kind choice
 * beside the field that is a trap: the pointer's way to "Team" is a blur of
 * the field, so the tag would be saved as PERSONAL on the way to the button —
 * and Safari does not focus a clicked button at all, so there is no
 * `relatedTarget` to recognise the move by. So with kinds, only Enter or the
 * Add button saves; the choice and the button swallow `mousedown` (below) so
 * a pointer never takes focus off the field; and leaving an EMPTY field closes
 * the form, which is the tidy-up the old blur gave. An older server has no
 * choice to lose, so it keeps the old save-on-leave.
 */
function onFieldBlur(e: FocusEvent) {
  if (legacy.value) {
    add();
    return;
  }
  const to = e.relatedTarget as Node | null;
  if (to && addForm.value?.contains(to)) return;
  if (!newTag.value.trim()) closeAdd();
}

function remove(item: TagItem) {
  void persist(items.value.filter((i) => i !== item));
}

onMounted(load);
watch(() => props.nodeId, load);
</script>

<template>
  <div class="filex-tag-picker" :class="{ 'is-loading': loading }">
    <span
      v-for="item in shown"
      :key="`${item.kind}:${item.name}`"
      class="filex-tag"
      :class="`filex-tag--${item.kind}`"
      :data-tag-kind="item.kind"
      :style="{ '--filex-tag-color': colorFor(item.name) }"
    >
      <TagKindIcon :kind="item.kind" />
      <button
        class="filex-tag-open"
        type="button"
        :title="openTitle(item)"
        :aria-label="openTitle(item)"
        :data-testid="`tag-open-${item.name}`"
        @click="emit('open', item.name, item.kind)"
      >{{ item.name }}</button>
      <button
        v-if="canRemove(item)"
        class="filex-tag-x"
        type="button"
        :aria-label="`${t('tags.remove')}: ${item.name}`"
        @click="remove(item)"
      >×</button>
    </span>

    <form v-if="adding" ref="addForm" class="filex-tag-add" @submit.prevent="add">
      <input
        v-model="newTag"
        autofocus
        maxlength="64"
        :placeholder="t('tags.name')"
        :aria-label="t('tags.name')"
        @blur="onFieldBlur"
        @keydown.escape="closeAdd"
      />
      <!-- `mousedown.prevent`: a pointer on the choice or the button must not
           take focus off the field (see onFieldBlur). -->
      <div v-if="!legacy" class="filex-tag-kindwrap" @mousedown.prevent>
        <ChoiceButtons
          :model-value="newKind"
          :options="kindOptions"
          :aria-label="t('tags.kind.label')"
          testid-prefix="tag-kind"
          @update:model-value="(v: string | string[]) => (newKind = v === 'team' ? 'team' : 'personal')"
        />
      </div>
      <button class="filex-tag-add-submit" type="submit" @mousedown.prevent>{{ t('tags.add_submit') }}</button>
    </form>
    <button v-else class="filex-tag-add-btn" type="button" @click="adding = true">
      {{ t('tags.add') }}
    </button>
  </div>
</template>

<style>
/* ⚠ NOT `scoped`, deliberately. Vue's scoped styles compile to
   `.cls[data-v-HASH]`, and in the web-component build the hash baked into
   this CSS does not match the one Vue stamps onto the DOM — so every rule
   here silently stopped applying. Measured in the desktop app: the share
   dialog had `position: static`, no background and no radius, i.e. raw
   unstyled HTML, in EVERY embedded surface.
   Safe to drop: every selector below is prefixed (fx-/fe-/filex-), so
   there is nothing here that can leak into a host page. */
.filex-tag-picker {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.filex-tag-picker.is-loading {
  opacity: 0.6;
  pointer-events: none;
}
.filex-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border-radius: 999px;
  padding: 2px 10px;
  font-size: 12px;
  font-weight: 500;
  color: #fff;
  background: var(--filex-tag-color);
}
/* etiket:t1 — the tag's NAME is the door to its view. A bare button: it
   inherits the chip's own colour and type so the chip still reads as one
   object, and the underline appears only under the pointer, where the chip has
   already said it is interactive by changing the cursor. */
.filex-tag-open {
  background: transparent;
  border: 0;
  padding: 0;
  margin: 0;
  color: inherit;
  font: inherit;
  line-height: inherit;
  cursor: pointer;
}
.filex-tag-open:hover,
.filex-tag-open:focus-visible {
  text-decoration: underline;
}
.filex-tag-x {
  background: transparent;
  border: none;
  color: rgba(255, 255, 255, 0.85);
  cursor: pointer;
  padding: 0 2px;
  font-size: 14px;
  line-height: 1;
}
.filex-tag-x:hover {
  color: #fff;
}
.filex-tag-add input {
  border: 1px solid var(--filex-border, #d1d5db);
  border-radius: 12px;
  padding: 2px 10px;
  font-size: 12px;
  width: 110px;
}
.filex-tag-add > input {
  width: auto;
  min-width: 0;
  grid-column: 1;
  grid-row: 1;
}
.filex-tag-add-btn {
  border: 1px dashed var(--filex-border, #d1d5db);
  background: transparent;
  border-radius: 999px;
  padding: 2px 10px;
  font-size: 12px;
  color: var(--filex-text-muted, #6b7280);
  cursor: pointer;
}
/* gorunum:v1 — a host override still wins, but the fallback chain now ends at
   the product blue instead of the old indigo, and it asks the palette first:
   in a themed context this hover follows --fe-primary (and so goes light blue
   in dark mode), and only a context with no stylesheet at all — the very case
   where a fallback is the only thing painting — lands on the literal. */
.filex-tag-add-btn:hover {
  border-color: var(--filex-accent, var(--fe-primary, #2f6ceb));
  color: var(--filex-accent, var(--fe-primary, #2f6ceb));
}
/* etiket:k2 — the kind rides inside the chip, before the name, in the chip's
   own colour: the icon is the at-a-glance half, the title the spelled-out one.
   A personal chip is OUTLINED and a team chip FILLED, so the two read apart
   even for someone who cannot tell two 12px glyphs apart. */
.filex-tag .fe-tagkind {
  flex: none;
  opacity: 0.9;
}
.filex-tag--personal {
  background: transparent;
  /* The page's own text colour, not the tag colour: an amber outline is
     legible, amber letters on white are not. */
  color: var(--fe-text, inherit);
  box-shadow: inset 0 0 0 1.5px var(--filex-tag-color);
}
.filex-tag--personal .fe-tagkind {
  color: var(--filex-tag-color);
  opacity: 1;
}
.filex-tag--personal .filex-tag-x {
  color: inherit;
  opacity: 0.7;
}
/* The add form: the name and Add on one line, the kind choice under them at
   full width with its two answers side by side, so "who sees this" reads as
   one question with two answers rather than two stray boxes. */
.filex-tag-add {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: 6px;
  flex-basis: 100%;
}
.filex-tag-add .filex-tag-add-submit {
  grid-column: 2;
  grid-row: 1;
}
.filex-tag-kindwrap {
  grid-column: 1 / -1;
  grid-row: 2;
}
.filex-tag-kindwrap .fe-choice {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.filex-tag-add-submit {
  border: 1px solid var(--filex-border, #d1d5db);
  background: transparent;
  border-radius: 12px;
  padding: 2px 12px;
  font-size: 12px;
  color: inherit;
  cursor: pointer;
}
</style>
