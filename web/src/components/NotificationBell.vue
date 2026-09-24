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
import { anchorUnderEndEdge, refElement } from '@/lib/anchoredPanel';
import { dirOfElement } from '@brftech/filex-core';
import { openNotificationTarget } from '@/lib/notificationNav';
// ⚠ The row and the badge are components, not markup written here: the same
// row is drawn by the full-list screen and the same badge by that screen's
// heading and by the desktop app's dock icon. Rule 1 and rule 3 of
// docs/NOTIFICATIONS.md → "The bell, and who can reach it" are both rules
// about "the same everywhere", which a second copy quietly ends.
import NotificationRow from '@/components/NotificationRow.vue';
import UnreadBadge from '@/components/UnreadBadge.vue';
import type { NotificationItem } from '@/api/types';

const { t } = useI18n();
const notif = useNotificationsStore();
const auth = useAuthStore();
const router = useRouter();

/* ── the count on the button ───────────────────────────────────────────── */
const unread = computed(() => Math.max(0, notif.unreadCount || 0));
/** The accessible name carries the count too — a badge is a picture of a number. */
const buttonLabel = computed(() =>
  unread.value > 0
    ? `${t('notifications.bell')} — ${t('notifications.unreadCount', { n: unread.value })}`
    : t('notifications.bell'),
);

/* ── where the teleported panel goes ───────────────────────────────────── */
const PANEL_W = 360;
const btnEl = ref<InstanceType<typeof PopoverButton> | null>(null);
const pos = ref<{ top: string; right?: string; left?: string; width: string; maxHeight: string }>({
  top: '0px',
  right: '0px',
  width: `${PANEL_W}px`,
  maxHeight: '420px',
});

/**
 * Recorded from the button, not the panel (the panel does not exist until it
 * opens), on BOTH click and keydown: Headless UI opens on Enter/Space without
 * a click, and a stale rectangle would hang the panel wherever the button used
 * to be. Neither handler intercepts the event.
 */
function syncPos() {
  const el = refElement(btnEl.value);
  const r = el?.getBoundingClientRect();
  if (!r) return;
  const at = anchorUnderEndEdge(
    r,
    { width: window.innerWidth, height: window.innerHeight },
    // The list scrolls inside the panel; 520 keeps ~6 rows on a laptop and
    // the head + foot visible on a phone. ⚠ RTL: flush with the bell's END
    // edge — its left one in a right-to-left header.
    { width: PANEL_W, maxHeight: 520, dir: dirOfElement(el) },
  );
  pos.value = { top: at.top, right: at.right, left: at.left, width: at.width ?? `${PANEL_W}px`, maxHeight: at.maxHeight };
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
 * Clicking a row marks it read AND goes to the thing it is about — through the
 * SAME resolver the browser and desktop notifications use, so the three can
 * never land in three different places. Owner's standing requirement:
 * *"tıklandığında yollarına gitmesini istiyorum."*
 *
 * ⚠ Only a row that HAS somewhere to go ever gets here: `NotificationRow`
 * emits nothing for an inert one (it is not even a button).
 */
async function openItem(n: NotificationItem, close: () => void) {
  close();
  if (!n.read_at) await notif.markRead(n.id).catch(() => {});
  await openNotificationTarget(router, n.target);
}

/**
 * "See all" — for EVERYBODY, and it no longer leaves the explorer.
 *
 * ⚠⚠ It used to be an admin-only `<RouterLink to="/notifications">`, i.e. the
 * instance-wide audit page behind `requiresAdmin`. Owner, 2026-09-20: *"Tüm
 * bildirimleri gör butonu explore'dan dışarı çıkıyor; adam admin değilse
 * göremez."* So a person who was not an administrator could read the newest
 * fifteen rows in this popover and had no way to reach the sixteenth. The
 * full list is now a panel over the explorer (NotificationsPanel.vue) reading
 * the user-scoped endpoints, and the admin page keeps its own, separate door
 * below — for MANAGING the subsystem, which is what it is for.
 */
function seeAll(close: () => void) {
  close();
  notif.openPanel();
}

// The newest fifteen. The full list is one click further on.
const list = computed(() => notif.feed.slice(0, 15));
</script>

<template>
  <Popover
    v-slot="{ open, close }"
    class="fx-bell"
  >
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
      <span
        class="fx-bell__icon"
        aria-hidden="true"
        v-html="actionIconSvg('bell')"
      ></span>
      <!-- ⚠ THE badge component (rule 3): the count is drawn on the icon by
           one thing, here, in the admin nav, in the full list's heading and —
           through lib/unreadBadge.ts — on the desktop app's dock. -->
      <UnreadBadge :count="unread" />
    </PopoverButton>

    <Teleport to="body">
      <PopoverPanel
        class="fx-bell__panel"
        :style="{ top: pos.top, right: pos.right, left: pos.left, width: pos.width }"
        data-testid="notification-panel"
      >
        <div
          class="fx-bell__head"
          @vue:mounted="onPanelOpen"
        >
          <span class="fx-bell__heading">{{ t('notifications.title') }}</span>
          <button
            v-if="notif.hasUnread"
            type="button"
            class="fx-bell__markall"
            data-testid="notification-mark-all"
            @click="notif.markAllRead()"
          >
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
            <span
              class="fx-bell__markall-icon"
              aria-hidden="true"
              v-html="actionIconSvg('check')"
            ></span>
            {{ t('notifications.markAllRead') }}
          </button>
        </div>

        <ul
          class="fx-bell__list"
          :style="{ maxHeight: pos.maxHeight }"
          data-testid="notification-list"
        >
          <li
            v-if="!list.length && !notif.feedLoading"
            class="fx-bell__empty"
          >
            {{ t('notifications.empty') }}
          </li>
          <li
            v-for="n in list"
            :key="n.id"
          >
            <!-- ⚠⚠ THE row component — the same one the full-list screen
                 draws. Whether a row is clickable is a fact about the row
                 (rule 1), so it is decided in one place and looks the same in
                 every list it appears in. -->
            <NotificationRow
              :item="n"
              @open="openItem($event, close)"
            />
          </li>
        </ul>

        <div class="fx-bell__foot">
          <!-- ⚠⚠ For EVERYBODY. This was `v-if="auth.isAdmin"` around a link
               to the admin audit page, which meant a non-admin was shown no
               way at all to read their own sixteenth notification. It now
               opens the full list over the explorer, from the user-scoped
               endpoints — no admin rights anywhere on the path. -->
          <button
            type="button"
            class="fx-bell__all"
            data-testid="notification-view-all"
            @click="seeAll(close)"
          >
            {{ t('notifications.viewAll') }}
          </button>
          <!-- ⚠ The admin page is a SECOND, smaller door, and it is for
               managing the subsystem (the webhook, everybody's rows) — not
               for reading your own mail. An administrator walks to it
               deliberately; nobody is sent there by a "see all". -->
          <RouterLink
            v-if="auth.isAdmin"
            to="/notifications"
            class="fx-bell__manage"
            data-testid="notification-manage"
            @click="close()"
          >
            {{ t('notifications.manageAdmin') }}
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
/* The unread count is UnreadBadge.vue now (rule 3: one badge, every
   surface). The button keeps `position: relative` above, which is the
   badge's positioning context and the only part of it that is about
   THIS control. */

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
/* The rows are NotificationRow.vue now — the same component the full-list
   screen draws, so "which rows are clickable" cannot be answered
   differently in the two places it is asked. */

.fx-bell__foot {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--fe-gap-sm);
  padding: 8px 12px;
  border-top: 1px solid var(--fe-border-soft);
  text-align: center;
  font-size: var(--fe-text-xs);
}
.fx-bell__all {
  border: 0;
  background: transparent;
  padding: 0;
  font: inherit;
  color: var(--fe-primary-ink);
  text-decoration: none;
  cursor: pointer;
}
.fx-bell__all:hover {
  text-decoration: underline;
}
/* ⚠ Visibly the lesser of the two. "See all" is where a person reads their
   own notifications; this one goes to the operator's console, and drawing
   them as equals is how the old bell sent everybody to the wrong one. */
.fx-bell__manage {
  color: var(--fe-text-muted);
  text-decoration: none;
}
.fx-bell__manage::before {
  content: '·';
  margin-inline-end: var(--fe-gap-sm);
  color: var(--fe-border-strong);
}
.fx-bell__manage:hover {
  color: var(--fe-text);
  text-decoration: underline;
}
</style>
