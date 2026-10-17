<script setup lang="ts">
/**
 * Admin → Plugins → Default apps → Office engine (filex 0.57): who converts
 * office documents for apps (Convert, MCP's file_convert) and draws their
 * thumbnails - the engine an app brings (the Office editor's, run on this
 * server) or a connected ONLYOFFICE Document Server.
 *
 * Each back end with how it stands now: ready, being prepared (an app's
 * engine is compiled once after an install or an update, a few minutes),
 * failed (with why), not running, not connected. The first one on the list
 * that runs takes the work; with two or more the administrator chooses which
 * goes first (buttons, never a native select), and can go back to the
 * default (the apps' engines first, the Document Server after them).
 *
 * The server's word on all of it (handlers/file_types_admin.go, the
 * `engine` capability of internal/assoc); this only draws it and sends the
 * rule.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { Cpu } from 'lucide-vue-next';
import { ChoiceButtons, type ChoiceOption } from '@brftech/filex-core';

import { ONLYOFFICE_HANDLER, type EngineBackendState, type EngineRow, type FileTypeHandler, type FileTypeRule } from '@/api/fileTypes';
import { engineBackendLabel, firstRule, itemsOf, type HandlerItem } from '@/lib/fileTypes';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';

const props = defineProps<{
  row: EngineRow;
  editable: boolean;
  busy: boolean;
}>();

const emit = defineEmits<{
  (e: 'choose', rule: FileTypeRule): void;
  (e: 'reset'): void;
}>();

const { t, locale } = useI18n();

/** Every back end, the ones asked first on top, the ones switched off last. */
const items = computed<HandlerItem[]>(() => itemsOf(props.row.on, props.row.off));

function label(h: FileTypeHandler): string {
  return engineBackendLabel(h, t, locale.value);
}

function stateOf(h: FileTypeHandler): EngineBackendState {
  return props.row.states[h.id] ?? { state: h.id === ONLYOFFICE_HANDLER ? 'off' : 'idle' };
}

const STATES = ['ready', 'preparing', 'failed', 'idle', 'off'];

function stateLabel(s: EngineBackendState): string {
  return STATES.includes(s.state) ? t(`defaultApps.engine.state.${s.state}`) : s.state;
}

function toneOf(s: EngineBackendState): 'emerald' | 'amber' | 'rose' | 'zinc' {
  if (s.state === 'ready') return 'emerald';
  if (s.state === 'preparing') return 'amber';
  if (s.state === 'failed') return 'rose';
  return 'zinc';
}

const first = computed(() => props.row.on[0]?.id ?? '');

const firstOptions = computed<ChoiceOption[]>(() => items.value.map((i) => ({ value: i.handler.id, label: label(i.handler) })));

function choose(v: string | string[]) {
  const id = Array.isArray(v) ? v[0] : v;
  if (!id || id === first.value) return;
  emit('choose', firstRule(props.row.on, props.row.off, id));
}
</script>

<template>
  <section
    class="space-y-3 rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    data-testid="default-apps-engine"
  >
    <header class="flex flex-wrap items-center gap-2">
      <Cpu class="h-5 w-5 text-brand-600 dark:text-brand-400" />
      <h2 class="text-base font-semibold">{{ t('defaultApps.engine.title') }}</h2>
      <Badge :tone="row.custom ? 'brand' : 'zinc'" size="xs" data-testid="default-apps-engine-status">
        {{ row.custom ? t('defaultApps.status.custom') : t('defaultApps.status.default') }}
      </Badge>
    </header>
    <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('defaultApps.engine.desc') }}</p>

    <p
      v-if="!items.length"
      class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
      data-testid="default-apps-engine-none"
    >
      {{ t('defaultApps.engine.none') }}
    </p>

    <ul v-else class="space-y-2">
      <li
        v-for="(item, i) in items"
        :key="item.handler.id"
        class="flex flex-wrap items-start justify-between gap-2 rounded-lg border border-zinc-200 p-3 dark:border-zinc-800"
        :data-testid="`default-apps-engine-row-${item.handler.id}`"
      >
        <div class="min-w-0">
          <div class="text-sm font-medium">
            <span v-if="item.on" class="text-zinc-500">{{ i + 1 }}.</span>
            {{ label(item.handler) }}
            <span v-if="item.handler.version" class="text-xs text-zinc-500">{{ item.handler.version }}</span>
          </div>
          <div class="text-xs text-zinc-500">
            {{ item.handler.id === ONLYOFFICE_HANDLER ? t('defaultApps.engine.kindServer') : t('defaultApps.engine.kindApp') }}
          </div>
          <p v-if="stateOf(item.handler).state === 'preparing'" class="text-xs text-amber-700 dark:text-amber-300">
            {{ t('defaultApps.engine.preparingHint') }}
          </p>
          <p
            v-else-if="stateOf(item.handler).state === 'failed'"
            class="text-xs text-rose-700 dark:text-rose-300"
            :data-testid="`default-apps-engine-error-${item.handler.id}`"
          >
            {{ t('defaultApps.engine.failedHint', { error: stateOf(item.handler).error || '?' }) }}
          </p>
        </div>
        <div class="flex shrink-0 items-center gap-1">
          <Badge v-if="!item.on" tone="zinc" size="xs">{{ t('defaultApps.engine.switchedOff') }}</Badge>
          <Badge :tone="toneOf(stateOf(item.handler))" size="xs" dot :data-testid="`default-apps-engine-state-${item.handler.id}`">
            {{ stateLabel(stateOf(item.handler)) }}
          </Badge>
        </div>
      </li>
    </ul>

    <div v-if="editable && items.length > 1" class="space-y-1">
      <div id="default-apps-engine-first-label" class="text-sm font-medium">{{ t('defaultApps.engine.first') }}</div>
      <ChoiceButtons
        :model-value="first"
        :options="firstOptions"
        :disabled="busy"
        aria-labelledby="default-apps-engine-first-label"
        testid="default-apps-engine-first"
        testid-prefix="default-apps-engine-first"
        @update:model-value="choose"
      />
      <p class="text-xs text-zinc-500">{{ t('defaultApps.engine.firstHint') }}</p>
    </div>

    <div v-if="editable && row.custom" class="flex flex-wrap items-center gap-2">
      <Button variant="outline" size="sm" :disabled="busy" data-testid="default-apps-engine-reset" @click="emit('reset')">
        {{ t('defaultApps.engine.reset') }}
      </Button>
      <span class="text-xs text-zinc-500">{{ t('defaultApps.engine.resetHint') }}</span>
    </div>
  </section>
</template>
