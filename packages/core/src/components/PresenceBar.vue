<script setup lang="ts">
// PresenceBar — compact avatar strip showing who else is viewing the current
// folder, and (as a small badge) which file each person is focused on. The
// server already excludes the recipient from the roster (presence answers
// "who ELSE is here"); the optional selfId filter remains as a belt-and-braces
// for older backends. Purely presentational; the live data comes from the
// realtime presence stream.

import { computed } from 'vue';
import type { PresenceUser } from '../lib/realtime';

const props = defineProps<{
  users: PresenceUser[];
  selfId?: number | null;
}>();

const others = computed(() =>
  (props.users ?? []).filter((u) => props.selfId == null || u.id !== props.selfId),
);

// Stable key per identity: end users behind one shared proxy token have the
// same numeric id but distinct server-issued uids.
function keyOf(u: PresenceUser): string {
  return u.uid ?? String(u.id);
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

// Deterministic hue per identity so the same person keeps the same colour.
function hue(u: PresenceUser): number {
  const s = keyOf(u);
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h % 360;
}

function label(u: PresenceUser): string {
  return u.file ? `${u.name} · ${u.file}` : u.name;
}
</script>

<template>
  <div v-if="others.length" class="fx-presence" :title="others.map(label).join(', ')">
    <div class="fx-presence-avatars">
      <span
        v-for="u in others.slice(0, 5)"
        :key="keyOf(u)"
        class="fx-presence-avatar"
        :style="{ backgroundColor: `hsl(${hue(u)} 60% 45%)` }"
        :title="label(u)"
      >
        {{ initials(u.name) }}
        <span v-if="u.file" class="fx-presence-dot" aria-hidden="true"></span>
      </span>
      <span v-if="others.length > 5" class="fx-presence-more">+{{ others.length - 5 }}</span>
    </div>
    <span class="fx-presence-text">
      {{ others.length === 1 ? others[0].name : `${others.length} kişi` }}
      <template v-if="others.length === 1 && others[0].file">
        · <span class="fx-presence-file">{{ others[0].file }}</span>
      </template>
    </span>
  </div>
</template>

<style scoped>
.fx-presence {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  max-width: 100%;
}
.fx-presence-avatars {
  display: inline-flex;
  align-items: center;
}
.fx-presence-avatar {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.5rem;
  height: 1.5rem;
  margin-left: -0.4rem;
  border-radius: 9999px;
  border: 2px solid var(--fx-presence-ring, #ffffff);
  color: #fff;
  font-size: 0.6rem;
  font-weight: 600;
  line-height: 1;
  user-select: none;
}
.fx-presence-avatar:first-child {
  margin-left: 0;
}
.fx-presence-dot {
  position: absolute;
  right: -1px;
  bottom: -1px;
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 9999px;
  background: #22c55e; /* green — actively focused on a file */
  border: 1.5px solid var(--fx-presence-ring, #ffffff);
}
.fx-presence-more {
  margin-left: 0.15rem;
  font-size: 0.7rem;
  color: rgb(113 113 122); /* zinc-500 */
}
.fx-presence-text {
  font-size: 0.75rem;
  color: rgb(113 113 122); /* zinc-500 */
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 14rem;
}
.fx-presence-file {
  color: rgb(82 82 91); /* zinc-600 */
  font-weight: 500;
}
:global(.dark) .fx-presence-avatar,
:global(.dark) .fx-presence-dot {
  --fx-presence-ring: #18181b; /* zinc-900 */
}
</style>
