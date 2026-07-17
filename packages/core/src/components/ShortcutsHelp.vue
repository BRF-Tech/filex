<script setup lang="ts">
/**
 * ShortcutsHelp — "?" cheat-sheet modal.
 *
 * Renders the SHORTCUTS table exported by useKeyboardShortcuts (single
 * source of truth) grouped by section. Modal.vue supplies Esc handling,
 * backdrop, focus and the close button.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { SHORTCUTS, type ShortcutDef } from '../composables/useKeyboardShortcuts';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const { t } = useLocale(() => props.locale);

const groups = computed(() => {
  const order: string[] = [];
  const map = new Map<string, ShortcutDef[]>();
  for (const s of SHORTCUTS) {
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
          <div v-for="s in g.items" :key="s.labelKey" class="fe-shortcuts__row">
            <span class="fe-shortcuts__keys">
              <template v-for="(combo, i) in s.keys" :key="combo">
                <kbd class="fe-kbd">{{ combo }}</kbd>
                <span v-if="i < s.keys.length - 1" class="fe-shortcuts__or" aria-hidden="true">/</span>
              </template>
            </span>
            <span class="fe-shortcuts__desc">{{ t(s.labelKey) }}</span>
          </div>
        </div>
      </section>
    </div>
  </Modal>
</template>
