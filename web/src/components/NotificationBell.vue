<script setup lang="ts">
/**
 * NotificationBell — the one bell, wherever a header has room for it.
 *
 * It lives in TWO headers and is the same component in both: the admin
 * panel's top nav, and the explorer's own header cluster (Explore.vue, beside
 * the avatar). The second home is the owner's ruling, 2026-09-14, verbatim:
 * *"üst bara zil koyalım."* Until then the bell existed only inside the admin
 * panel, so a NON-admin — whose whole product is /home and /explore — got
 * browser notifications and could never open the list of them, mark one read,
 * or follow one to what it was about.
 *
 * ⚠ The poll is NOT here. It lives in App.vue (composables/useNotificationWatcher)
 * so every signed-in screen is covered by one 15 s loop whether or not a bell is
 * drawn on it; this component reads the store that loop fills — `notif.feed`,
 * the same array the loop refreshes whenever the unread count moves, so a row
 * that raises the badge also appears in the list, open or closed.
 *
 * ⚠⚠ The panel is TELEPORTED to <body>, for the same measured reason the avatar
 * menu is: `.fe` — the explorer's root — carries `overflow: hidden`, so a panel
 * positioned inside the header is cut off at the header's bottom edge. Its
 * position is taken from the button's rectangle at the moment it opens,
 * anchored to the button's RIGHT edge and clamped into the viewport, because at
 * 390px the bell sits a little left of the avatar and a 360px panel hung from
 * its right edge would otherwise start off-screen.
 *
 * ⚠ Declared `--fe-*` tokens only, no Tailwind colours: in the explorer header
 * it sits four pixels from controls drawn in those tokens (and under a palette
 * the viewer may have chosen), and the glyph is the explorer's own
 * (`actionIconSvg('bell')`) for the same reason.
 */
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { RouterLink, useRouter } from 'vue-router';
import { Popover, PopoverButton, PopoverPanel } from '@headlessui/vue';
import { actionIconSvg } from '@brftech/filex-core';

import { useNotificationsStore } from '@/stores/notifications';
import { useAuthStore } from '@/stores/auth';
import { formatDate } from '@/lib/format';
import { anchorUnderRightEdge, refElement } from '@/lib/anchoredPanel';
import { openNotificationTarget } from '@/lib/notificationNav';
import { resolveNotificationTarget } from '@/lib/notificationTarget';
import { useNotificationText } from '@/composables/useNotificationText';
import type { NotificationItem } from '@/api/types';

const { t, locale } = useI18n();
// ⚠ Same renderer as the browser toast and the desktop shell. A row's stored
// title is written once, on the server, in one language — and for most file
// events it is not written at all, so what was shown here was the raw event id
// (`share.created`). The sentence is composed by the reader; see
// lib/notificationText.ts.
const { notificationText } = useNotificationText();
const notif = useNotificationsStore();
const auth = useAuthStore();
const router = useRouter();

/* ── the count on the button ───────────────────────────────────────────── */
const unread = computed(() => Math.max(0, notif.unreadCount || 0));
const countLabel = computed(() => (unread.value > 99 ? '99+' : String(unread.value)));
/** The accessible name carries the count too — a badge is a picture of a number. */
const buttonLabel = computed(() =>
  unread.value > 0
    ? `${t('notifications.bell')} — ${t('notifications.unreadCount', { n: unread.value })}`
    : t('notifications.bell'),
);

/* ── where the teleported panel goes ───────────────────────────────────── */
const PANEL_W = 360;
const btnEl = ref<InstanceType<typeof PopoverButton> | null>(null);
const pos = ref({ top: '0px', right: '0px', width: `${PANEL_W}px`, maxHeight: '420px' });

/**
 * Recorded from the button, not the panel (the panel does not exist until it
 * opens), on BOTH click and keydown: Headless UI opens on Enter/Space without
 * a click, and a stale rectangle would hang the panel wherever the button used
 * to be. Neither handler intercepts the event.
 */
function syncPos() {
  const r = refElement(btnEl.value)?.getBoundingClientRect();
  if (!r) return;
  const at = anchorUnderRightEdge(
    r,
    { width: window.innerWidth, height: window.innerHeight },
    // The list scrolls inside the panel; 520 keeps ~6 rows on a laptop and
    // the head + foot visible on a phone.
    { width: PANEL_W, maxHeight: 520 },
  );
  pos.value = { top: at.top, right: at.right, width: at.width ?? `${PANEL_W}px`, maxHeight: at.maxHeight };
}

/**
 * The panel just OPENED: place it and bring the rows up to date.
 *
 * ⚠⚠ Bound to the panel's CONTENT, not to `<PopoverPanel>` itself. Headless UI
 * keeps the panel component mounted for the life of the popover and only stops
 * rendering its children while closed, so a `vue:mounted` on the component
 * fired exactly once — on the first render, before anybody opened anything —
 * and every later open showed whatever that one fetch had returned. Its
 * children really do mount and unmount with each open, so the hook lives on
 * the first of them.
 */
function onPanelOpen() {
  syncPos();
  void notif.refreshFeed();
}

/* ── the rows ──────────────────────────────────────────────────────────── */

/**
 * gorunum:v2 — the severity in the reader's language, not the enum.
 *
 * It printed the raw value under a CSS `uppercase`, which in Turkish turns
 * `info` into `İNFO` — the locale's own uppercasing of a dotted i, applied to a
 * word that was never Turkish in the first place.
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

/** Does this row go anywhere? Drives the hint and the cursor. */
function hasTarget(n: NotificationItem): boolean {
  return resolveNotificationTarget(n.target).kind !== 'none';
}

/**
 * Clicking a row marks it read AND goes to the thing it is about — through the
 * SAME resolver the browser and desktop notifications use, so the three can
 * never land in three different places. Owner's standing requirement:
 * *"tıklandığında yollarına gitmesini istiyorum."*
 */
async function openItem(n: NotificationItem, close: () => void) {
  close();
  if (!n.read_at) await notif.markRead(n.id).catch(() => {});
  await openNotificationTarget(router, n.target);
}

// ⚠ Rendered ONCE per row, here, not twice per row in the template (title +
// body). vue-i18n's locale is reactive, so this recomputes when the language
// changes: the same row reads differently for a different person.
const list = computed(() =>
  notif.feed.slice(0, 15).map((n) => ({ ...n, text: notificationText(n) })),
);
</script>

<template>
  <Popover v-slot="{ open, close }" class="fx-bell">
    <PopoverButton
      ref="btnEl"
      class="fx-bell__btn"
      :class="{ 'is-open': open }"
      :title="buttonLabel"
      :aria-label="buttonLabel"
      data-testid="notification-bell"
      @click="syncPos"
      @keydown="syncPos"
    >
      <!-- eslint-disable-next-line vue/no-v-html — static markup from @brftech/filex-core's lib/actionIcons -->
      <span class="fx-bell__icon" aria-hidden="true" v-html="actionIconSvg('bell')"></span>
      <span v-if="unread > 0" class="fx-bell__count" aria-hidden="true" data-testid="notification-bell-count">{{
        countLabel
      }}</span>
    </PopoverButton>

    <Teleport to="body">
      <PopoverPanel
        class="fx-bell__panel"
        :style="{ top: pos.top, right: pos.right, width: pos.width }"
        data-testid="notification-panel"
      >
        <div class="fx-bell__head" @vue:mounted="onPanelOpen">
          <span class="fx-bell__heading">{{ t('notifications.title') }}</span>
          <button
            v-if="notif.hasUnread"
            type="button"
            class="fx-bell__markall"
            data-testid="notification-mark-all"
            @click="notif.markAllRead()"
          >
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
            <span class="fx-bell__markall-icon" aria-hidden="true" v-html="actionIconSvg('check')"></span>
            {{ t('notifications.markAllRead') }}
          </button>
        </div>

        <ul class="fx-bell__list" :style="{ maxHeight: pos.maxHeight }" data-testid="notification-list">
          <li v-if="!list.length && !notif.feedLoading" class="fx-bell__empty">
            {{ t('notifications.empty') }}
          </li>
          <li v-for="n in list" :key="n.id">
            <button
              type="button"
              class="fx-bell__row"
              :class="{ 'is-unread': !n.read_at, 'has-target': hasTarget(n) }"
              :title="hasTarget(n) ? t('notifications.openTarget') : undefined"
              data-testid="notification-row"
              @click="openItem(n, close)"
            >
              <span class="fx-bell__dot" aria-hidden="true"></span>
              <span class="fx-bell__rowbody">
                <span class="fx-bell__meta">
                  <span class="fx-bell__sev" :class="`is-${severityTone(n.severity)}`">{{
                    severityLabel(n.severity)
                  }}</span>
                  <span class="fx-bell__when">{{ formatDate(n.created_at, locale) }}</span>
                </span>
                <span class="fx-bell__title">{{ n.text.title }}</span>
                <span v-if="n.text.body" class="fx-bell__body">{{ n.text.body }}</span>
              </span>
            </button>
          </li>
        </ul>

        <!-- ⚠ Admins only. /notifications is the instance-wide audit page and
             sits behind the admin panel's route guard, so for anybody else
             this link would bounce them to their Home — a door that opens onto
             the hallway. For them the list above IS the list. -->
        <div v-if="auth.isAdmin" class="fx-bell__foot">
          <RouterLink to="/notifications" class="fx-bell__all" @click="close()">
            {{ t('notifications.viewAll') }}
          </RouterLink>
        </div>
      </PopoverPanel>
    </Teleport>
  </Popover>
</template>

<style>
/* ⚠ NOT scoped. The panel is teleported to <body>, outside this component's
   subtree, and the button has to lose to the explorer header's own
   `.fe-toolbar__tail` rules on equal footing rather than out-rank them with a
   `[data-v-…]` attribute. Every class is `fx-bell`-prefixed. */
.fx-bell {
  position: relative;
  display: inline-flex;
}
/* The same square as every other control in the explorer's trailing cluster
   (`.fe-toolbar__tail .fe-btn`, base.css) — whoever rendered it. */
.fx-bell__btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: var(--fe-h-md);
  min-width: var(--fe-h-md);
  padding: 0 var(--fe-gap-sm);
  border: 1px solid transparent;
  border-radius: var(--fe-radius-md);
  background: transparent;
  color: var(--fe-text-muted);
  cursor: pointer;
}
.fx-bell__btn:hover,
.fx-bell__btn.is-open {
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-bell__btn:focus-visible {
  outline: 2px solid var(--fe-primary);
  outline-offset: 1px;
}
/* ⚠ Not while open. Headless UI's Popover button re-focuses itself from script
   on a MOUSE click, which Chrome answers with `:focus-visible` — so the app's
   global `:focus-visible` ring (web/src styles) was drawn around the bell on
   every click, while the avatar's Menu button one control to the right gets
   none. Measured through CDP: the ring came from that global rule, not from
   this file. While open, the panel and the pressed ground already say where
   you are; the ring is for finding the button, and it is back once closed. */
.fx-bell__btn.is-open {
  outline: none;
}
.fx-bell__icon {
  display: inline-flex;
}
.fx-bell__icon svg {
  width: 16px;
  height: 16px;
  display: block;
  color: inherit;
}
/* The unread count. Primary on text-on-primary — the one ink pairing the
   theme contrast test pins in every palette and both variants — with a ring
   in the header's own ground so it reads as sitting ON the bell. */
.fx-bell__count {
  position: absolute;
  top: 2px;
  right: 1px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 999px;
  background: var(--fe-primary);
  color: var(--fe-text-on-primary);
  box-shadow: 0 0 0 2px var(--fe-bg);
  font-family: var(--fe-font);
  font-size: 10px;
  font-weight: 600;
  line-height: 16px;
  text-align: center;
  pointer-events: none;
}

.fx-bell__panel {
  position: fixed;
  /* Above the explorer's own overlays (context menus 90, tour 96) and level
     with the avatar menu, which it can never be open at the same time as. */
  z-index: 140;
  display: flex;
  flex-direction: column;
  max-width: calc(100vw - 16px);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-md);
  background: var(--fe-bg);
  color: var(--fe-text);
  box-shadow: var(--fe-shadow-sm);
  font-family: var(--fe-font);
  font-size: var(--fe-text-md);
  outline: none;
  overflow: hidden;
}
.fx-bell__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--fe-gap-sm);
  padding: 8px 12px;
  border-bottom: 1px solid var(--fe-border-soft);
}
.fx-bell__heading {
  font-weight: 600;
}
.fx-bell__markall {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 0;
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text-muted);
  font: inherit;
  font-size: var(--fe-text-xs);
  cursor: pointer;
}
.fx-bell__markall:hover {
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-bell__markall-icon svg {
  width: 14px;
  height: 14px;
  display: block;
  color: inherit;
}
.fx-bell__list {
  margin: 0;
  padding: 4px;
  list-style: none;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.fx-bell__empty {
  padding: 24px 12px;
  text-align: center;
  color: var(--fe-text-muted);
}
.fx-bell__row {
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
  text-align: left;
  cursor: default;
}
.fx-bell__row.has-target {
  cursor: pointer;
}
.fx-bell__row:hover,
.fx-bell__row:focus-visible {
  background: var(--fe-bg-hover);
  outline: none;
}
/* Unread is the dot; read rows keep their words and lose only the weight. */
.fx-bell__dot {
  flex: 0 0 auto;
  width: 8px;
  height: 8px;
  margin-top: 5px;
  border-radius: 50%;
  background: var(--fe-border-strong);
}
.fx-bell__row.is-unread .fx-bell__dot {
  background: var(--fe-primary);
}
.fx-bell__rowbody {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1 1 auto;
}
.fx-bell__meta {
  display: flex;
  align-items: baseline;
  gap: var(--fe-gap-sm);
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
.fx-bell__sev {
  font-weight: 600;
}
.fx-bell__sev.is-danger {
  color: var(--fe-danger);
}
.fx-bell__sev.is-warning {
  color: var(--fe-warning);
}
.fx-bell__sev.is-info {
  color: var(--fe-primary-ink);
}
.fx-bell__when {
  margin-left: auto;
  white-space: nowrap;
}
.fx-bell__title {
  font-weight: 500;
  overflow-wrap: anywhere;
}
.fx-bell__row:not(.is-unread) .fx-bell__title {
  font-weight: 400;
  color: var(--fe-text-muted);
}
.fx-bell__body {
  color: var(--fe-text-muted);
  font-size: var(--fe-text-sm);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
.fx-bell__foot {
  padding: 8px 12px;
  border-top: 1px solid var(--fe-border-soft);
  text-align: center;
  font-size: var(--fe-text-xs);
}
.fx-bell__all {
  color: var(--fe-primary-ink);
  text-decoration: none;
}
.fx-bell__all:hover {
  text-decoration: underline;
}
</style>
