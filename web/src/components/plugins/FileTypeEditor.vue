<script setup lang="ts">
/**
 * FileTypeEditor - one kind of file's two lists (Default apps, filex 0.50):
 * who opens it and who draws its thumbnails, each handler on or off, in the
 * order they are asked.
 *
 *   • Open: people choose among the openers that are on; the first is what a
 *     file of the kind opens in until they do. Every opener off: the file
 *     opens in filex's own viewer all the same (a file must open somewhere).
 *   • Thumbnails: the first handler that draws the file wins; when it cannot,
 *     the next one is asked. Every handler off: no thumbnail (`no_handler`).
 *
 * Save sends only the capability whose list changed (a list that was left
 * as it came is not turned into a rule); "Back to the default" removes both.
 * Read-only for a caller who may not change it (an API key, the demo): the
 * lists are drawn, the controls are not.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChevronDown, ChevronUp, RotateCcw, Save } from 'lucide-vue-next';

import type { FileTypeChange, FileTypeKind } from '@/api/fileTypes';
import { handlerLabelVersioned, itemsOf, ruleOf, sameItems, type HandlerItem } from '@/lib/fileTypes';

import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';
import Toggle from '@/components/ui/Toggle.vue';

const props = defineProps<{
  modelValue: boolean;
  kind: FileTypeKind | null;
  editable: boolean;
  busy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  (e: 'save', change: FileTypeChange): void;
  (e: 'reset'): void;
}>();

const { t, locale } = useI18n();

type Cap = 'open' | 'thumbnail';
const CAPS: Cap[] = ['open', 'thumbnail'];

const lists = ref<Record<Cap, HandlerItem[]>>({ open: [], thumbnail: [] });
const initial = ref<Record<Cap, HandlerItem[]>>({ open: [], thumbnail: [] });

watch(
  () => [props.modelValue, props.kind] as const,
  ([open, kind]) => {
    if (!open || !kind) return;
    for (const c of CAPS) {
      initial.value[c] = itemsOf(kind[c].on, kind[c].off);
      lists.value[c] = initial.value[c].map((i) => ({ ...i }));
    }
  },
  { immediate: true },
);

function move(c: Cap, index: number, by: -1 | 1) {
  const list = [...lists.value[c]];
  const to = index + by;
  if (to < 0 || to >= list.length) return;
  [list[index], list[to]] = [list[to], list[index]];
  lists.value[c] = list;
}

function setOn(c: Cap, index: number, on: boolean) {
  const list = [...lists.value[c]];
  list[index] = { ...list[index], on };
  lists.value[c] = list;
}

const changed = computed(() => CAPS.filter((c) => !sameItems(lists.value[c], initial.value[c])));

const allOff = computed<Record<Cap, boolean>>(() => ({
  open: lists.value.open.length > 0 && lists.value.open.every((i) => !i.on),
  thumbnail: lists.value.thumbnail.length > 0 && lists.value.thumbnail.every((i) => !i.on),
}));

const title = computed(() => (props.kind ? t('defaultApps.editor.title', { ext: props.kind.ext }) : ''));
const custom = computed(() => !!props.kind && (props.kind.open.custom || props.kind.thumbnail.custom));

function save() {
  if (!props.editable || !changed.value.length) {
    if (!changed.value.length) emit('update:modelValue', false);
    return;
  }
  const change: FileTypeChange = {};
  for (const c of changed.value) change[c] = ruleOf(lists.value[c]);
  emit('save', change);
}

function close() {
  emit('update:modelValue', false);
}
</script>

<template>
  <Modal :model-value="modelValue" :title="title" size="lg" @update:model-value="(v: boolean) => !v && close()">
    <div v-if="kind" class="space-y-5" data-testid="file-type-editor">
      <p v-if="kind.mime" class="font-mono text-xs text-zinc-500">{{ kind.mime }}</p>
      <p
        v-if="!editable"
        class="rounded-lg border border-zinc-200 bg-zinc-50 p-2 text-xs text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300"
        data-testid="file-type-editor-readonly"
      >
        {{ t('defaultApps.readonly') }}
      </p>

      <section v-for="c in CAPS" :key="c" class="space-y-2" :data-testid="`file-type-editor-${c}`">
        <h3 class="text-sm font-semibold">{{ t(`defaultApps.editor.${c}`) }}</h3>
        <p class="text-xs text-zinc-500">{{ t(`defaultApps.editor.${c}Hint`) }}</p>
        <p v-if="!lists[c].length" class="text-sm text-zinc-500" :data-testid="`file-type-editor-${c}-none`">
          {{ t(`defaultApps.editor.${c}None`) }}
        </p>
        <ol v-else class="space-y-1">
          <li
            v-for="(item, i) in lists[c]"
            :key="item.handler.id"
            class="flex items-center gap-2 rounded-lg border border-zinc-200 px-2 py-1.5 text-sm dark:border-zinc-800"
            :class="!item.on && 'opacity-60'"
            :data-testid="`file-type-handler-${c}-${item.handler.id}`"
          >
            <span class="w-5 shrink-0 text-end text-xs tabular-nums text-zinc-500">{{ item.on ? lists[c].filter((x) => x.on).indexOf(item) + 1 : '' }}</span>
            <span class="min-w-0 flex-1 truncate" :title="item.handler.id">{{ handlerLabelVersioned(item.handler, t, locale) }}</span>
            <template v-if="editable">
              <Toggle
                :model-value="item.on"
                :name="`fte-${c}-${item.handler.id}`"
                :label="t('defaultApps.editor.on')"
                @update:model-value="(v: boolean) => setOn(c, i, v)"
              />
              <Button
                type="button"
                size="sm"
                variant="ghost"
                :disabled="i === 0"
                :aria-label="t('defaultApps.editor.up')"
                :title="t('defaultApps.editor.up')"
                :data-testid="`file-type-up-${c}-${item.handler.id}`"
                @click="move(c, i, -1)"
              >
                <ChevronUp class="h-4 w-4" />
              </Button>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                :disabled="i === lists[c].length - 1"
                :aria-label="t('defaultApps.editor.down')"
                :title="t('defaultApps.editor.down')"
                :data-testid="`file-type-down-${c}-${item.handler.id}`"
                @click="move(c, i, 1)"
              >
                <ChevronDown class="h-4 w-4" />
              </Button>
            </template>
            <span v-else class="text-xs text-zinc-500">{{ item.on ? t('defaultApps.editor.on') : t('defaultApps.editor.off') }}</span>
          </li>
        </ol>
        <p
          v-if="allOff[c]"
          class="rounded-lg border border-amber-200 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          :data-testid="`file-type-editor-${c}-alloff`"
        >
          {{ t(`defaultApps.editor.${c}AllOff`) }}
        </p>
      </section>
    </div>

    <template #footer>
      <Button
        v-if="editable && custom"
        type="button"
        size="sm"
        variant="ghost"
        :loading="busy"
        data-testid="file-type-editor-reset"
        @click="emit('reset')"
      >
        <RotateCcw class="h-4 w-4" />
        {{ t('defaultApps.actions.reset') }}
      </Button>
      <Button type="button" size="sm" variant="ghost" @click="close">
        {{ editable ? t('common.cancel') : t('common.close') }}
      </Button>
      <Button
        v-if="editable"
        type="button"
        size="sm"
        variant="primary"
        :loading="busy"
        :disabled="!changed.length"
        data-testid="file-type-editor-save"
        @click="save"
      >
        <Save class="h-4 w-4" />
        {{ t('common.save') }}
      </Button>
    </template>
  </Modal>
</template>
