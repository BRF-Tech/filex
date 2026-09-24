<script setup lang="ts">
/**
 * NotificationRow — one notification, wherever it is listed.
 *
 * ⚠⚠ THE row, not "the bell's row". It is drawn in the bell's popover and in
 * the full-list screen that "see all" opens, and rule 1 of the product rule
 * (docs/NOTIFICATIONS.md → "The bell, and who can reach it") is a fact about
 * the ROW, not about the list it happens to be in: *a notification is
 * clickable exactly when it has somewhere to go*. Two copies of that markup is
 * two places for the `is-static` class to be forgotten, and a row that looks
 * clickable and is not is exactly the defect this rule exists to kill.
 *
 * ⚠ The verdict itself is not taken locally: `isNotificationClickable` lives
 * in `lib/notificationTarget.ts`, which the browser toast and the desktop
 * shell import too. This file decides how the answer LOOKS, never what it is.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

import { formatDate } from '@/lib/format';
import { isNotificationClickable } from '@/lib/notificationTarget';
import { useNotificationText } from '@/composables/useNotificationText';
import type { NotificationItem } from '@/api/types';

const props = defineProps<{ item: NotificationItem }>();
const emit = defineEmits<{ (e: 'open', item: NotificationItem): void }>();

const { t, locale } = useI18n();
// ⚠ The same renderer as the browser toast and the desktop shell. A row's
// stored title is written once, on the server, in one language — and for most
// file events it is not written at all, so what a raw render shows is the
// event id (`share.created`). The sentence is composed by the READER.
const { notificationText } = useNotificationText();

const text = computed(() => notificationText(props.item));
const clickable = computed(() => isNotificationClickable(props.item.target));
const unread = computed(() => !props.item.read_at);

/**
 * gorunum:v2 — the severity in the reader's language, not the enum.
 *
 * It printed the raw value under a CSS `uppercase`, which in Turkish turns
 * `info` into `İNFO` — the locale's own uppercasing of a dotted i, applied to
 * a word that was never Turkish in the first place.
 */
function severityLabel(sev: string): string {
  const key = `notifications.severity.${sev}`;
  const out = t(key);
  return out === key ? sev : out;
}

function severityTone(s: string): 'danger' | 'warning' | 'info' {
  if (s === 'critical' || s === 'error') return 'danger';
  if (s === 'warning') return 'warning';
  return 'info';
}
</script>

<template>
  <!-- ⚠⚠ A row with nothing to open is NOT a button. It used to be one, and it
       took the person to the notifications page — the page they were most
       likely already on, and for a non-admin a guard bounce that threw away
       the folder they were standing in. A row that is only worth reading is
       now only readable: no cursor, no hover, no focus stop, no click. -->
  <component
    :is="clickable ? 'button' : 'div'"
    :type="clickable ? 'button' : undefined"
    class="fx-nrow"
    :class="{ 'is-unread': unread, 'has-target': clickable, 'is-static': !clickable }"
    :title="clickable ? t('notifications.openTarget') : undefined"
    data-testid="notification-row"
    :data-clickable="clickable ? 'yes' : 'no'"
    :data-notification-id="item.id"
    @click="clickable ? emit('open', item) : undefined"
  >
    <span
      class="fx-nrow__dot"
      aria-hidden="true"
    ></span>
    <span class="fx-nrow__body">
      <span class="fx-nrow__meta">
        <span
          class="fx-nrow__sev"
          :class="`is-${severityTone(item.severity)}`"
        >{{
          severityLabel(item.severity)
        }}</span>
        <span class="fx-nrow__when">{{ formatDate(item.created_at, locale) }}</span>
      </span>
      <span class="fx-nrow__title">{{ text.title }}</span>
      <span
        v-if="text.body"
        class="fx-nrow__body-text"
      >{{ text.body }}</span>
    </span>
  </component>
</template>

<style>
/* ⚠ NOT scoped. The bell's popover is TELEPORTED to <body>, outside this
   component's subtree, so a `[data-v-…]` attribute selector would not reach
   it. Every class is `fx-nrow`-prefixed instead.
   ⚠ Declared `--fe-*` tokens only: the row is drawn inside the explorer,
   under whatever palette the viewer chose. */
.fx-nrow {
  display: flex;
  align-items: flex-start;
  gap: var(--fe-gap-sm);
  width: 100%;
  padding: 8px;
  border: 0;
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text);
  font: inherit;
  text-align: start;
  cursor: default;
}
.fx-nrow.has-target {
  cursor: pointer;
}
.fx-nrow:hover,
.fx-nrow:focus-visible {
  background: var(--fe-bg-hover);
  outline: none;
}
/* ⚠ A row with nowhere to go keeps its words and loses every affordance —
   including the hover tint, which is what says "this does something". */
.fx-nrow.is-static:hover,
.fx-nrow.is-static:focus-visible {
  background: transparent;
}
/* Unread is the dot; read rows keep their words and lose only the weight. */
.fx-nrow__dot {
  flex: 0 0 auto;
  width: 8px;
  height: 8px;
  margin-top: 5px;
  border-radius: 50%;
  background: var(--fe-border-strong);
}
.fx-nrow.is-unread .fx-nrow__dot {
  background: var(--fe-primary);
}
.fx-nrow__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1 1 auto;
}
.fx-nrow__meta {
  display: flex;
  align-items: baseline;
  gap: var(--fe-gap-sm);
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
.fx-nrow__sev {
  font-weight: 600;
}
.fx-nrow__sev.is-danger {
  color: var(--fe-danger);
}
.fx-nrow__sev.is-warning {
  color: var(--fe-warning);
}
.fx-nrow__sev.is-info {
  color: var(--fe-primary-ink);
}
.fx-nrow__when {
  margin-inline-start: auto;
  white-space: nowrap;
}
.fx-nrow__title {
  font-weight: 500;
  overflow-wrap: anywhere;
}
.fx-nrow:not(.is-unread) .fx-nrow__title {
  font-weight: 400;
  color: var(--fe-text-muted);
}
.fx-nrow__body-text {
  color: var(--fe-text-muted);
  font-size: var(--fe-text-sm);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
</style>
