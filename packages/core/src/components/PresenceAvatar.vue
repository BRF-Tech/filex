<script setup lang="ts">
// One person's circle in the presence strip: their profile picture when the
// account has one, their initials when it does not.
//
// It is its own component because the strip draws the same circle twice (the
// compact overlapping row and the expanded name chips). Written inline in both
// places, the photo would sooner or later appear in one and not the other —
// which is exactly the kind of split this codebase keeps paying for.

import { computed, ref } from 'vue';
import type { PresenceUser } from '../lib/realtime';

const props = defineProps<{
  user: PresenceUser;
  /** Tooltip; omitted in contexts where the surrounding chip already names the
   *  person. */
  title?: string;
}>();

// Only sources a browser will actually render. The value comes off the socket
// (an account's stored avatar, or one a trusted host proxy stamped for its own
// end user), so anything unexpected stays initials instead of a broken frame.
const src = computed(() => {
  const v = (props.user.avatar ?? '').trim();
  return /^(data:image\/|https?:\/\/|\/)/.test(v) ? v : '';
});

// A picture that fails to load falls back to initials rather than an empty hole.
const failed = ref(false);
const showPhoto = computed(() => !!src.value && !failed.value);

const initials = computed(() => {
  const parts = props.user.name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
});

// Deterministic hue per identity so the same person keeps the same colour.
// Behind a photo it is only the ring/backdrop, so it stays either way.
const hue = computed(() => {
  const s = props.user.uid ?? String(props.user.id);
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h % 360;
});
</script>

<template>
  <span
    class="fx-presence-avatar"
    :class="{ 'fx-presence-avatar--photo': showPhoto }"
    :style="{ backgroundColor: `hsl(${hue} 60% 45%)` }"
    :title="title"
  >
    <img v-if="showPhoto" class="fx-presence-photo" :src="src" :alt="user.name" @error="failed = true" />
    <template v-else>{{ initials }}</template>
    <span v-if="user.file" class="fx-presence-dot" aria-hidden="true"></span>
  </span>
</template>
