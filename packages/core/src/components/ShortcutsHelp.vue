<script setup lang="ts">
/**
 * ShortcutsHelp — "?" cheat-sheet modal.
 *
 * wiring:c2 — renders the live shortcut registry (useShortcutList) so
 * user remaps show up here too, grouped by section. Unbound actions are
 * listed with an "unassigned" chip. The footer carries a "Customize"
 * button that asks the host to open ShortcutSettings. Modal.vue
 * supplies Esc handling, backdrop, focus and the close button.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { useShortcutList, type ShortcutView } from '../composables/useKeyboardShortcuts';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'customize'): void;
}>();

const { t } = useLocale(() => props.locale);

const list = useShortcutList();

const groups = computed(() => {
  const order: string[] = [];
  const map = new Map<string, ShortcutView[]>();
  for (const s of list.value) {
    if (!map.has(s.groupKey)) {
      map.set(s.groupKey, []);
      order.push(s.groupKey);
    }
    map.get(s.groupKey)!.push(s);
  }
  return order.map((key) => ({ key, items: map.get(key)! }));
});
</script>

<template>
  <Modal :open="open" :title="t('shortcuts.title')" size="md" @close="emit('close')">
    <div class="fe-shortcuts">
      <section v-for="g in groups" :key="g.key" class="fe-shortcuts__group">
        <h3 class="fe-shortcuts__heading">{{ t(g.key) }}</h3>
        <div class="fe-shortcuts__table">
          <div v-for="s in g.items" :key="s.id" class="fe-shortcuts__row">
            <span class="fe-shortcuts__keys">
              <template v-if="s.keys.length === 0">
                <span class="fe-shortcuts__unbound">{{ t('shortcuts.settings.unbound') }}</span>
              </template>
              <template v-for="(combo, i) in s.keys" v-else :key="combo">
                <kbd class="fe-kbd">{{ combo }}</kbd>
                <span v-if="i < s.keys.length - 1" class="fe-shortcuts__or" aria-hidden="true">/</span>
              </template>
            </span>
            <span class="fe-shortcuts__desc">
              {{ t(s.labelKey) }}
              <span
                v-if="s.overridden"
                class="fe-shortcuts__custom"
                :title="t('shortcuts.settings.customized')"
              >●</span>
            </span>
          </div>
        </div>
      </section>
    </div>
    <template #actions>
      <button type="button" class="fe-btn fe-btn--primary" @click="emit('customize')">
        ⌨ {{ t('shortcuts.customize') }}
      </button>
      <button type="button" class="fe-btn" @click="emit('close')">{{ t('viewer.close') }}</button>
    </template>
  </Modal>
</template>
