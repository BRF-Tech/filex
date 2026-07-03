<script setup lang="ts">
import { storeToRefs } from 'pinia';
import { useToastStore } from '@/stores/toast';
import Toast from './ui/Toast.vue';

const toast = useToastStore();
const { toasts } = storeToRefs(toast);
</script>

<template>
  <div
    aria-live="polite"
    class="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2"
  >
    <Toast v-for="t in toasts" :key="t.id" :toast="t" @dismiss="toast.dismiss" />
  </div>
</template>
