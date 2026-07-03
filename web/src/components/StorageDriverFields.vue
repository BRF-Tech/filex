<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type { StorageDriver } from '@/api/types';
import Input from './ui/Input.vue';
import Toggle from './ui/Toggle.vue';

interface Props {
  driver: StorageDriver;
  modelValue: Record<string, unknown>;
}

const props = defineProps<Props>();
const emit = defineEmits<{
  (e: 'update:modelValue', v: Record<string, unknown>): void;
}>();

const { t } = useI18n();

function set(key: string, value: unknown) {
  emit('update:modelValue', { ...props.modelValue, [key]: value });
}

function field<T = unknown>(key: string, fallback?: T): T {
  const v = props.modelValue?.[key];
  if (v === undefined || v === null) return fallback as T;
  return v as T;
}
</script>

<template>
  <div class="space-y-3">
    <!-- LOCAL -->
    <template v-if="driver === 'local'">
      <Input
        :model-value="field<string>('path', '')"
        :label="t('storages.fields.path')"
        placeholder="/var/lib/filex/data"
        required
        monospace
        @update:model-value="(v) => set('path', v)"
      />
    </template>

    <!-- S3 -->
    <template v-if="driver === 's3'">
      <Input
        :model-value="field<string>('endpoint', '')"
        :label="t('storages.fields.endpoint')"
        placeholder="https://nbg1.your-objectstorage.com"
        required
        monospace
        @update:model-value="(v) => set('endpoint', v)"
      />
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input
          :model-value="field<string>('region', '')"
          :label="t('storages.fields.region')"
          placeholder="eu-central"
          @update:model-value="(v) => set('region', v)"
        />
        <Input
          :model-value="field<string>('bucket', '')"
          :label="t('storages.fields.bucket')"
          placeholder="my-bucket"
          required
          @update:model-value="(v) => set('bucket', v)"
        />
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input
          :model-value="field<string>('access_key', '')"
          :label="t('storages.fields.accessKey')"
          autocomplete="off"
          monospace
          required
          @update:model-value="(v) => set('access_key', v)"
        />
        <Input
          :model-value="field<string>('secret_key', '')"
          :label="t('storages.fields.secretKey')"
          autocomplete="off"
          type="password"
          monospace
          required
          @update:model-value="(v) => set('secret_key', v)"
        />
      </div>
      <Toggle
        :model-value="field<boolean>('path_style', true)"
        :label="t('storages.fields.pathStyle')"
        @update:model-value="(v) => set('path_style', v)"
      />
    </template>

    <!-- SFTP -->
    <template v-if="driver === 'sftp'">
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <Input
          class="sm:col-span-2"
          :model-value="field<string>('host', '')"
          :label="t('storages.fields.host')"
          placeholder="files.example.com"
          required
          monospace
          @update:model-value="(v) => set('host', v)"
        />
        <Input
          :model-value="field<number>('port', 22)"
          :label="t('storages.fields.port')"
          type="number"
          :min="1"
          :max="65535"
          @update:model-value="(v) => set('port', v)"
        />
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input
          :model-value="field<string>('user', '')"
          :label="t('storages.fields.user')"
          autocomplete="username"
          required
          @update:model-value="(v) => set('user', v)"
        />
        <Input
          :model-value="field<string>('password', '')"
          :label="t('common.password')"
          type="password"
          autocomplete="new-password"
          @update:model-value="(v) => set('password', v)"
        />
      </div>
      <Input
        :model-value="field<string>('key_path', '')"
        :label="t('storages.fields.keyPath')"
        placeholder="/etc/filex/keys/sftp_id_ed25519"
        monospace
        @update:model-value="(v) => set('key_path', v)"
      />
      <Input
        :model-value="field<string>('base_path', '/')"
        :label="t('storages.fields.basePath')"
        monospace
        @update:model-value="(v) => set('base_path', v)"
      />
    </template>

    <!-- WEBDAV -->
    <template v-if="driver === 'webdav'">
      <Input
        :model-value="field<string>('url', '')"
        :label="t('storages.fields.url')"
        placeholder="https://dav.example.com/files/"
        required
        monospace
        @update:model-value="(v) => set('url', v)"
      />
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input
          :model-value="field<string>('user', '')"
          :label="t('storages.fields.user')"
          autocomplete="username"
          @update:model-value="(v) => set('user', v)"
        />
        <Input
          :model-value="field<string>('password', '')"
          :label="t('common.password')"
          type="password"
          autocomplete="new-password"
          @update:model-value="(v) => set('password', v)"
        />
      </div>
    </template>
  </div>
</template>
