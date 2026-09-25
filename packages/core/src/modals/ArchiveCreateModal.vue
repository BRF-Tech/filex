<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { ArchiveCreateFormat } from '../types/FileNode';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';
import { archiveFormatLabel } from '../lib/archiveFormats';

const props = withDefaults(defineProps<{
  open: boolean;
  locale: LocaleCode;
  count: number;
  suggestedName: string;
  defaultFormat?: ArchiveCreateFormat;
  allowedFormats?: ArchiveCreateFormat[];
  busy?: boolean;
  requestError?: string;
  /** Can the server set a password (it needs 7-Zip)? Default yes. ⚠ Stated
   *  in withDefaults: Vue casts an absent optional boolean prop to false. */
  encryption?: boolean;
}>(), { encryption: true });

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', value: { name: string; format: ArchiveCreateFormat; password?: string; encrypt_filenames: boolean; compression: number; solid?: boolean; dictionary_size_mb?: number }): void;
}>();

const { t } = useLocale(() => props.locale);
const name = ref('archive.zip');
const format = ref<ArchiveCreateFormat>('7z');
const password = ref('');
const confirmPassword = ref('');
const encryptNames = ref(false);
const compression = ref(5);
const solid = ref(true);
const dictionarySizeMB = ref(64);
const error = ref('');

const allFormats: ArchiveCreateFormat[] = ['zip', '7z', 'tar', 'tar.gz', 'tar.bz2', 'tar.xz'];
const availableFormats = computed<ArchiveCreateFormat[]>(() => {
  const formats = (props.allowedFormats ?? allFormats).filter(
    (value, index, all) => allFormats.includes(value) && all.indexOf(value) === index,
  );
  return formats.length ? formats : allFormats;
});
const supportsEncryption = computed(() => props.encryption !== false && (format.value === 'zip' || format.value === '7z'));

function formatFromName(value: string): ArchiveCreateFormat | null {
  const lower = value.trim().toLowerCase();
  return allFormats.find((candidate) => lower.endsWith(`.${candidate}`)) ?? null;
}

function stripArchiveExtension(value: string): string {
  const detected = formatFromName(value);
  return detected ? value.slice(0, -(detected.length + 1)) : value;
}

watch(() => props.open, (open) => {
  if (!open) return;
  const preferred = props.defaultFormat && availableFormats.value.includes(props.defaultFormat)
    ? props.defaultFormat
    : availableFormats.value.includes('7z') ? '7z' : availableFormats.value[0];
  format.value = preferred;
  name.value = `${stripArchiveExtension(props.suggestedName || 'archive')}.${preferred}`;
  password.value = '';
  confirmPassword.value = '';
  encryptNames.value = false;
  compression.value = 5;
  solid.value = true;
  dictionarySizeMB.value = 64;
  error.value = '';
});

watch(format, (next) => {
  if (!props.open) return;
  const base = stripArchiveExtension(name.value.trim()) || 'archive';
  name.value = `${base}.${next}`;
  if (next !== 'zip' && next !== '7z') {
    password.value = '';
    confirmPassword.value = '';
    encryptNames.value = false;
  }
});

watch(name, (next) => {
  const detected = formatFromName(next);
  if (detected && availableFormats.value.includes(detected)) format.value = detected;
});

function submit() {
  if (!name.value.trim() || /[\\/]/.test(name.value)) {
    error.value = t('archive.invalid_name');
    return;
  }
  const extension = formatFromName(name.value);
  if (!extension || !availableFormats.value.includes(extension)) {
    error.value = t('archive.invalid_extension');
    return;
  }
  if (supportsEncryption.value && password.value !== confirmPassword.value) {
    error.value = t('archive.password_mismatch');
    return;
  }
  /* 7-Zip encrypts a ZIP with printable ASCII only; the server refuses the
   * rest too (PASSWORD_CHARSET), this only says so before anything is sent. */
  if (extension === 'zip' && password.value && !/^[\x20-\x7e]*$/.test(password.value)) {
    error.value = t('archive.password_charset');
    return;
  }
  emit('submit', {
    name: name.value.trim(),
    format: extension,
    password: supportsEncryption.value ? password.value || undefined : undefined,
    encrypt_filenames: !!password.value && extension === '7z' && encryptNames.value,
    compression: compression.value,
    solid: extension === '7z' ? solid.value : undefined,
    dictionary_size_mb: extension === '7z' ? dictionarySizeMB.value : undefined,
  });
}
</script>

<template>
  <Modal :open="open" :title="t('archive.create_title')" size="sm" @close="emit('close')">
    <form class="fe-form" @submit.prevent="submit">
      <p class="fe-field__hint">{{ t('archive.create_count', { count }) }}</p>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('archive.filename') }}</span>
        <input v-model="name" class="fe-input" autocomplete="off" />
      </label>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('archive.format') }}</span>
        <select v-model="format" class="fe-input">
          <option v-for="option in availableFormats" :key="option" :value="option">
            {{ archiveFormatLabel(option) }}
          </option>
        </select>
      </label>
      <label v-if="supportsEncryption" class="fe-field">
        <span class="fe-field__label">{{ t('archive.password_optional') }}</span>
        <input
          v-model="password"
          class="fe-input"
          type="password"
          autocomplete="off"
          data-1p-ignore
          data-bwignore="true"
          data-lpignore="true"
        />
      </label>
      <label v-if="supportsEncryption" class="fe-field">
        <span class="fe-field__label">{{ t('archive.password_confirm') }}</span>
        <input
          v-model="confirmPassword"
          class="fe-input"
          type="password"
          autocomplete="off"
          data-1p-ignore
          data-bwignore="true"
          data-lpignore="true"
        />
      </label>
      <label v-if="format === '7z' && password" class="fe-checkbox">
        <input v-model="encryptNames" type="checkbox" />
        <span>{{ t('archive.encrypt_names') }}</span>
      </label>
      <label v-if="format !== 'tar'" class="fe-field">
        <span class="fe-field__label">{{ t('archive.compression') }}: {{ compression }}</span>
        <input v-model.number="compression" type="range" min="0" max="9" />
      </label>
      <template v-if="format === '7z'">
        <label class="fe-checkbox">
          <input v-model="solid" type="checkbox" />
          <span>{{ t('archive.solid') }}</span>
        </label>
        <label class="fe-field">
          <span class="fe-field__label">{{ t('archive.dictionary_size') }}</span>
          <select v-model.number="dictionarySizeMB" class="fe-input">
            <option v-for="size in [4, 8, 16, 32, 64, 128, 256]" :key="size" :value="size">{{ t('archive.dictionary_size_value', { n: size }) }}</option>
          </select>
          <span class="fe-field__hint">{{ t('archive.dictionary_hint') }}</span>
        </label>
      </template>
      <p v-if="error || requestError" class="fe-form__error">{{ error || requestError }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">{{ t('archive.cancel') }}</button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" @click="submit">
        {{ busy ? t('archive.creating') : t('archive.create') }}
      </button>
    </template>
  </Modal>
</template>
