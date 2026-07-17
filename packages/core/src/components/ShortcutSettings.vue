<script setup lang="ts">
/**
 * ShortcutSettings — wiring:c2 keyboard-shortcut customization modal.
 *
 * Category-grouped rows from the shortcut registry; each row shows the
 * action label + current combo badge(s) + "Change" (press-to-capture:
 * the next key combination is recorded, Esc cancels) + a per-row
 * "reset to default" and a header "reset all".
 *
 * Conflicts: assigning a combo that another action already uses turns
 * the row into a red warning with two choices — unbind the old action
 * and take the combo, or cancel. Combos reserved by fixed shortcuts
 * (Backspace, Esc) can never be taken.
 *
 * While the modal is open a window-level CAPTURE listener swallows
 * keydowns (except Esc/Tab) so experimenting with keys can't trigger
 * explorer actions behind the modal; native button activation (Enter/
 * Space on a focused button) is a default action and still works.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import {
  comboFromEvent,
  effectiveCombo,
  findShortcutConflict,
  resetAllShortcuts,
  resetShortcut,
  setShortcutOverride,
  useShortcutList,
  SHORTCUT_ACTIONS,
  type ShortcutView,
} from '../composables/useKeyboardShortcuts';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: 'light' | 'dark' | 'auto';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
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

const anyOverridden = computed(() => list.value.some((s) => s.overridden));

// --- capture / conflict state ---

const capturingId = ref<string | null>(null);
const conflict = ref<{ id: string; combo: string; conflictId: string; fixed: boolean } | null>(null);

function labelOf(id: string): string {
  const def = SHORTCUT_ACTIONS.find((a) => a.id === id);
  return def ? t(def.labelKey) : id;
}

function beginCapture(id: string) {
  conflict.value = null;
  capturingId.value = id;
}

function cancelCapture() {
  capturingId.value = null;
}

function attemptAssign(id: string, combo: string) {
  capturingId.value = null;
  if (combo === effectiveCombo(id)) return; // unchanged
  const clash = findShortcutConflict(combo, id);
  if (clash) {
    conflict.value = { id, combo, conflictId: clash.id, fixed: clash.fixed };
    return;
  }
  setShortcutOverride(id, combo);
}

function confirmConflictTake() {
  const c = conflict.value;
  if (!c || c.fixed) return;
  setShortcutOverride(c.conflictId, ''); // unbind the old owner
  setShortcutOverride(c.id, c.combo);
  conflict.value = null;
}

function dismissConflict() {
  conflict.value = null;
}

function onReset(id: string) {
  conflict.value = null;
  resetShortcut(id);
}

function onResetAll() {
  conflict.value = null;
  capturingId.value = null;
  resetAllShortcuts();
}

// --- window capture listener (only while open) ---

function onWindowKeydown(e: KeyboardEvent) {
  if (capturingId.value) {
    e.preventDefault();
    e.stopPropagation();
    if (e.key === 'Escape') {
      cancelCapture();
      return;
    }
    const combo = comboFromEvent(e);
    if (!combo) return; // bare modifier — keep listening
    attemptAssign(capturingId.value, combo);
    return;
  }
  // Modal open but idle: let Esc (close) and Tab (focus nav) through,
  // swallow everything else so explorer shortcuts can't fire behind us.
  if (e.key === 'Escape' || e.key === 'Tab') return;
  e.stopPropagation();
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      window.addEventListener('keydown', onWindowKeydown, true);
    } else {
      window.removeEventListener('keydown', onWindowKeydown, true);
      capturingId.value = null;
      conflict.value = null;
    }
  },
);

onBeforeUnmount(() => window.removeEventListener('keydown', onWindowKeydown, true));

function onClose() {
  if (capturingId.value) {
    cancelCapture();
    return;
  }
  emit('close');
}
</script>

<template>
  <Modal :open="open" :title="t('shortcuts.settings.title')" size="lg" :theme="theme" @close="onClose">
    <div class="fe-shortset">
      <div class="fe-shortset__topbar">
        <p class="fe-shortset__hint">{{ t('shortcuts.settings.hint') }}</p>
        <button
          type="button"
          class="fe-btn fe-shortset__reset-all"
          :disabled="!anyOverridden"
          @click="onResetAll"
        >
          ↺ {{ t('shortcuts.settings.reset_all') }}
        </button>
      </div>

      <section v-for="g in groups" :key="g.key" class="fe-shortset__group">
        <h3 class="fe-shortcuts__heading">{{ t(g.key) }}</h3>
        <div
          v-for="s in g.items"
          :key="s.id"
          class="fe-shortset__item"
          :class="{
            'is-capturing': capturingId === s.id,
            'is-conflict': conflict?.id === s.id,
          }"
        >
          <div class="fe-shortset__row">
            <span class="fe-shortset__label">
              {{ t(s.labelKey) }}
              <span v-if="s.overridden" class="fe-shortcuts__custom" :title="t('shortcuts.settings.customized')">●</span>
            </span>
            <span class="fe-shortset__keys">
              <span v-if="capturingId === s.id" class="fe-shortset__capture" role="status">
                {{ t('shortcuts.settings.capture') }}
              </span>
              <template v-else>
                <span v-if="s.keys.length === 0" class="fe-shortcuts__unbound">
                  {{ t('shortcuts.settings.unbound') }}
                </span>
                <template v-for="(combo, i) in s.keys" v-else :key="combo">
                  <kbd class="fe-kbd">{{ combo }}</kbd>
                  <span v-if="i < s.keys.length - 1" class="fe-shortcuts__or" aria-hidden="true">/</span>
                </template>
              </template>
            </span>
            <span class="fe-shortset__actions">
              <template v-if="s.customizable">
                <button
                  v-if="capturingId !== s.id"
                  type="button"
                  class="fe-btn fe-shortset__btn"
                  @click="beginCapture(s.id)"
                >
                  {{ t('shortcuts.settings.change') }}
                </button>
                <button
                  v-else
                  type="button"
                  class="fe-btn fe-shortset__btn"
                  @click="cancelCapture"
                >
                  {{ t('shortcuts.settings.conflict_cancel') }}
                </button>
                <button
                  v-if="s.overridden"
                  type="button"
                  class="fe-btn fe-btn--icon-only fe-shortset__btn"
                  :title="t('shortcuts.settings.reset')"
                  :aria-label="t('shortcuts.settings.reset')"
                  @click="onReset(s.id)"
                >
                  ↺
                </button>
              </template>
              <span v-else class="fe-shortset__fixed">{{ t('shortcuts.settings.fixed') }}</span>
            </span>
          </div>
          <div v-if="conflict && conflict.id === s.id" class="fe-shortset__conflict" role="alert">
            <span class="fe-shortset__conflict-msg">
              <kbd class="fe-kbd">{{ conflict.combo }}</kbd>
              {{
                conflict.fixed
                  ? t('shortcuts.settings.conflict_fixed', { name: labelOf(conflict.conflictId) })
                  : t('shortcuts.settings.conflict', { name: labelOf(conflict.conflictId) })
              }}
            </span>
            <span class="fe-shortset__conflict-actions">
              <button
                v-if="!conflict.fixed"
                type="button"
                class="fe-btn fe-btn--danger fe-shortset__btn"
                @click="confirmConflictTake"
              >
                {{ t('shortcuts.settings.conflict_take') }}
              </button>
              <button type="button" class="fe-btn fe-shortset__btn" @click="dismissConflict">
                {{ t('shortcuts.settings.conflict_cancel') }}
              </button>
            </span>
          </div>
        </div>
      </section>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" @click="onClose">{{ t('viewer.close') }}</button>
    </template>
  </Modal>
</template>
