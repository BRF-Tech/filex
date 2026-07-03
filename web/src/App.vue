<script setup lang="ts">
import { onMounted } from 'vue';
import { RouterView } from 'vue-router';
import ToastContainer from '@/components/ToastContainer.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';

const auth = useAuthStore();
const caps = useCapabilitiesStore();

onMounted(async () => {
  // Hydrate session + capabilities up-front so route guards have data.
  // Errors are swallowed: an unauthenticated user just lands on /admin/login.
  await Promise.allSettled([auth.fetchMe(), caps.fetch()]);
});
</script>

<template>
  <RouterView v-slot="{ Component, route }">
    <transition name="fade" mode="out-in">
      <component :is="Component" :key="route.path" />
    </transition>
  </RouterView>
  <ToastContainer />
</template>

<style>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 120ms ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
