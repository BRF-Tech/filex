<script setup lang="ts">
/**
 * SurfacePeoplePicker — a plugin surface's `people-picker` node: who a
 * document goes to. Chips for the people already chosen, one box to add
 * the next.
 *
 * Internal users come from `GET /api/files/plugins/users?plugin=&q=`, which
 * answers only for a plugin that holds `users:lookup`. A 403 (or a 404 from
 * a server without the route) turns the lookup off for the life of the
 * node and the box becomes plain e-mail entry — the contract's fallback,
 * and the reason the box never says "no results" for a refusal.
 *
 * Free e-mail entry is offered when the plugin allows external people OR
 * the lookup is off: with neither a way to name a user nor a way to type
 * one, the node would be a wall.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import type { FileApi } from '../../../composables/useFileApi';
import type { PluginPerson } from '../../../types/Plugins';
import { useLocale } from '../../../composables/useLocale';
import { personName } from '../../../lib/personName';
import { looksLikeEmail } from '../../../lib/surfaceValues';
import { isListSeparatorKey } from '../../../lib/listInput';

const props = defineProps<{
  id: string;
  modelValue: PluginPerson[];
  multi?: boolean;
  allowExternal?: boolean;
  /** Absent: no lookup at all — free e-mail entry only. */
  api?: Pick<FileApi, 'pluginUsers'>;
  /** The plugin the lookup is asked for. */
  plugin?: string;
  locale: LocaleCode;
  disabled?: boolean;
  invalid?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: PluginPerson[]): void;
}>();

const { t } = useLocale(() => props.locale);

const query = ref('');
const suggestions = ref<PluginPerson[]>([]);
const showSuggest = ref(false);
/** The server refused (403) or has no route (404): free e-mail entry only. */
const lookupOff = ref(!props.api || !props.plugin);
const notice = ref('');
let timer: ReturnType<typeof setTimeout> | null = null;
let lastQuery = 0;

const people = computed(() => props.modelValue ?? []);
const freeEntry = computed(() => props.allowExternal === true || lookupOff.value);
const full = computed(() => props.multi !== true && people.value.length >= 1);

/* One rule for naming a person (lib/personName). */
function personLabel(p: PluginPerson): string {
  return personName({ name: p.name, email: p.email });
}

function add(p: PluginPerson) {
  const email = String(p.email ?? '').trim().toLowerCase();
  if (!email) return;
  const rest = props.multi === true ? people.value.filter((x) => x.email.toLowerCase() !== email) : [];
  emit('update:modelValue', [...rest, { ...p, email }]);
  query.value = '';
  suggestions.value = [];
  showSuggest.value = false;
  notice.value = '';
}

function remove(email: string) {
  emit('update:modelValue', people.value.filter((x) => x.email !== email));
}

function onInput() {
  notice.value = '';
  const q = query.value.trim();
  if (timer) clearTimeout(timer);
  if (!q || lookupOff.value) {
    suggestions.value = [];
    showSuggest.value = false;
    return;
  }
  timer = setTimeout(() => void search(q), 180);
}

async function search(q: string) {
  if (!props.api || !props.plugin || lookupOff.value) return;
  const mine = ++lastQuery;
  try {
    const r = await props.api.pluginUsers(props.plugin, q);
    if (mine !== lastQuery) return;
    suggestions.value = (r.users ?? []).filter((u) => u && typeof u.email === 'string');
    showSuggest.value = suggestions.value.length > 0;
  } catch (e) {
    if (mine !== lastQuery) return;
    const status = (e as { status?: number })?.status;
    if (status === 403 || status === 404) lookupOff.value = true;
    suggestions.value = [];
    showSuggest.value = false;
  }
}

/** Enter / comma: the typed address, when free entry is allowed. */
function commit() {
  const q = query.value.trim();
  if (!q) return;
  if (!freeEntry.value) {
    notice.value = t('plugin.people.external_off');
    return;
  }
  if (!looksLikeEmail(q)) {
    notice.value = t('plugin.people.invalid_email');
    return;
  }
  add({ email: q });
}

function onKeydown(ev: KeyboardEvent) {
  // ⚠ "،" / "，" commit too (lib/listInput — an Arabic or CJK keyboard).
  if (ev.key === 'Enter' || isListSeparatorKey(ev.key)) {
    ev.preventDefault();
    commit();
  } else if (ev.key === 'Backspace' && query.value === '' && people.value.length) {
    remove(people.value[people.value.length - 1].email);
  } else if (ev.key === 'Escape') {
    showSuggest.value = false;
  }
}

function onBlur() {
  // Let a mousedown on a suggestion land first.
  setTimeout(() => {
    showSuggest.value = false;
  }, 120);
}

watch(
  () => [props.api, props.plugin] as const,
  ([api, plugin]) => {
    if (api && plugin) lookupOff.value = false;
  },
);

onBeforeUnmount(() => {
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <div class="fe-speople" :class="{ 'is-invalid': invalid }" data-testid="surface-people">
    <ul v-if="people.length" class="fe-speople__chips" data-testid="surface-people-chips">
      <li v-for="p in people" :key="p.email" class="fe-speople__chip" :title="p.email">
        <span class="fe-speople__av" aria-hidden="true">{{ personLabel(p).charAt(0).toUpperCase() }}</span>
        <span class="fe-speople__name">{{ personLabel(p) }}</span>
        <button
          type="button"
          class="fe-speople__remove"
          :disabled="disabled"
          :title="t('plugin.people.remove', { name: personLabel(p) })"
          :aria-label="t('plugin.people.remove', { name: personLabel(p) })"
          :data-testid="`surface-people-remove-${p.email}`"
          @click="remove(p.email)"
        >
          <svg class="fe-ficon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true" focusable="false">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      </li>
    </ul>
    <div v-if="!full" class="fe-speople__addwrap">
      <input
        v-model="query"
        class="fe-cfield__input fe-speople__input"
        type="email"
        autocomplete="off"
        spellcheck="false"
        :placeholder="lookupOff ? t('plugin.people.placeholder_email') : t('plugin.people.placeholder')"
        :disabled="disabled"
        :aria-invalid="invalid ? 'true' : undefined"
        data-testid="surface-people-input"
        @input="onInput"
        @keydown="onKeydown"
        @focus="onInput"
        @blur="onBlur"
      />
      <button
        v-if="freeEntry"
        type="button"
        class="fe-btn fe-btn--sm"
        :disabled="disabled || !query.trim()"
        data-testid="surface-people-add"
        @click="commit"
      >
        {{ t('plugin.people.add') }}
      </button>
      <ul v-if="showSuggest" class="fe-share__suggest fe-speople__suggest" data-testid="surface-people-suggest">
        <li v-for="u in suggestions" :key="u.user_id ?? u.email" @mousedown.prevent="add(u)">
          <span class="fe-share__av fe-share__av--sm">{{ personLabel(u).charAt(0).toUpperCase() }}</span>
          <span class="fe-share__suggesttxt">
            <span class="fe-share__suggestname">{{ personLabel(u) }}</span>
            <span class="fe-share__suggestmeta">{{ u.email }}</span>
          </span>
        </li>
      </ul>
    </div>
    <p v-if="notice" class="fe-surface__error" role="alert">{{ notice }}</p>
  </div>
</template>
