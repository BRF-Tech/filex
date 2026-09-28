<script setup lang="ts">
/**
 * NotificationsPanel — ALL of a person's notifications, where they already are.
 *
 * Owner's ruling, 2026-09-20, verbatim: *"Tüm bildirimleri gör butonu
 * explore'dan dışarı çıkıyor; adam admin değilse göremez."*
 *
 * ⚠⚠ What it replaces. "See all" pointed at `/notifications`, which is the
 * ADMIN panel's instance-wide audit page and sits behind `requiresAdmin`. So
 * the bell's footer was hidden from everybody who was not an administrator,
 * and an ordinary person — whose whole product is the explorer — could read
 * the newest fifteen rows in the popover and had no way to reach the
 * sixteenth. Their own mail, behind a permission they do not have.
 *
 * ⚠ So it is a PANEL OVER THE EXPLORER rather than a route. A route would
 * have to live outside the admin block to escape that guard, and it would
 * unmount the explorer to show a list — throwing away the folder the person
 * was standing in, which is the same complaint one layer down. This opens
 * over what they were doing and closes back onto it.
 *
 * ⚠ It reads the USER-scoped endpoints — `GET /api/notifications`, `POST
 * /api/notifications/{id}/read`, `POST /api/notifications/read-all` — every
 * one of which is authenticated-but-not-admin (docs/NOTIFICATIONS.md →
 * "In-app bell (endpoints)"). Nothing here touches `/api/admin`.
 *
 * ⚠ Rendered ONCE per feed and opened through it (`feed.openPanel()`): the web
 * draws it at its root (App.vue) because the bell is in two headers; the
 * explorer draws it beside its own bell when it draws one (the desktop app).
 * A panel per bell would be two panels, and the one that was not open would
 * still be fetching. Moved here from web/src/components on 2026-09-27 with the
 * bell, so the desktop app's "See all" opens this very screen.
 */
import { computed, onBeforeUnmount, watch } from 'vue';

import { useLocale } from '../composables/useLocale';
import type { NotificationFeed, NotificationRowData } from '../composables/useNotificationFeed';
import { actionIconSvg } from '../lib/actionIcons';
import NotificationRow from './NotificationRow.vue';
import UnreadBadge from './UnreadBadge.vue';

const props = defineProps<{
  /** The person's notifications (useNotificationFeed), refs unwrapped. */
  feed: NotificationFeed;
  /** The language on screen — any offered code, a language pack's included. */
  locale: string;
}>();

const emit = defineEmits<{
  /** A row with somewhere to go was clicked — already marked read. */
  (e: 'open', item: NotificationRowData): void;
}>();

const { t } = useLocale(() => props.locale);

const page = computed(() => Math.floor(props.feed.mineOffset / props.feed.mineLimit) + 1);
const pages = computed(() => Math.max(1, Math.ceil(props.feed.mineTotal / props.feed.mineLimit)));

function go(p: number): void {
  if (p < 1 || p > pages.value) return;
  props.feed.setMinePage(p);
  void props.feed.fetchMine();
}

function toggleUnread(): void {
  props.feed.setMineUnreadFilter(!props.feed.mineUnreadOnly);
  void props.feed.fetchMine();
}

/**
 * A row was clicked: mark it read and go where it points — the SAME resolver
 * the bell, the browser toast and the desktop shell use, so the four cannot
 * land in four different places. The panel closes first: the destination is
 * the explorer underneath it, and a list left open over it would be covering
 * the very file it just revealed.
 */
async function open(n: NotificationRowData): Promise<void> {
  props.feed.closePanel();
  if (!n.read_at) await props.feed.markRead(n.id).catch(() => {});
  emit('open', n);
}

/** Mark one row read WITHOUT going anywhere — the only thing an inert row
 *  can be done with, and the thing a long list is mostly used for. */
async function markOne(n: NotificationRowData): Promise<void> {
  if (n.read_at) return;
  await props.feed.markRead(n.id).catch(() => {});
}

/**
 * ⚠ Escape closes it. The panel covers the whole viewport and the explorer
 * beneath it still has focus handlers; without this the only way out on a
 * keyboard would be to tab to the close button, which on a 25-row list is a
 * long walk.
 */
function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') props.feed.closePanel();
}

watch(
  () => props.feed.panelOpen,
  (open) => {
    if (open) window.addEventListener('keydown', onKey);
    else window.removeEventListener('keydown', onKey);
  },
  { immediate: true },
);
onBeforeUnmount(() => window.removeEventListener('keydown', onKey));
</script>

<template>
  <Teleport to="body">
    <div
      v-if="feed.panelOpen"
      class="fx-nall"
      role="dialog"
      aria-modal="true"
      :aria-label="t('notifications.title')"
      data-testid="notifications-screen"
    >
      <!-- ⚠ The backdrop closes it. `.self` so a click that started inside the
           sheet and ended on the backdrop (a drag over a long title) does not
           close a list somebody is reading. -->
      <div
        class="fx-nall__scrim"
        @click.self="feed.closePanel()"
      ></div>

      <section class="fx-nall__sheet">
        <header class="fx-nall__head">
          <span class="fx-nall__title">
            <span
              class="fx-nall__title-icon"
              aria-hidden="true"
            >
              <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
              <span v-html="actionIconSvg('bell')"></span>
              <!-- ⚠ The same badge component as the bell's. Rule 3: a counter
                   written twice is a counter that disagrees with itself. -->
              <UnreadBadge :count="feed.unreadCount" />
            </span>
            {{ t('notifications.title') }}
          </span>
          <div class="fx-nall__tools">
            <button
              type="button"
              class="fx-nall__tool"
              :class="{ 'is-on': feed.mineUnreadOnly }"
              :aria-pressed="feed.mineUnreadOnly"
              data-testid="notifications-screen-unread"
              @click="toggleUnread"
            >
              {{ t('notifications.unreadOnly') }}
            </button>
            <button
              v-if="feed.hasUnread"
              type="button"
              class="fx-nall__tool"
              data-testid="notifications-screen-mark-all"
              @click="feed.markAllRead()"
            >
              {{ t('notifications.markAllRead') }}
            </button>
            <button
              type="button"
              class="fx-nall__close"
              :aria-label="t('modal.close')"
              data-testid="notifications-screen-close"
              @click="feed.closePanel()"
            >
              <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
              <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
            </button>
          </div>
        </header>

        <p
          v-if="feed.mineError"
          class="fx-nall__error"
          data-testid="notifications-screen-error"
        >
          {{ feed.mineError }}
        </p>

        <ul
          class="fx-nall__list"
          data-testid="notifications-screen-list"
        >
          <li
            v-if="!feed.mine.length && !feed.mineLoading"
            class="fx-nall__empty"
          >
            {{ t('notifications.empty') }}
          </li>
          <li
            v-for="n in feed.mine"
            :key="n.id"
            class="fx-nall__item"
          >
            <NotificationRow
              :item="n"
              :locale="locale"
              @open="open"
            />
            <button
              v-if="!n.read_at"
              type="button"
              class="fx-nall__read"
              :title="t('notifications.markRead')"
              :aria-label="t('notifications.markRead')"
              data-testid="notifications-screen-mark-one"
              @click="markOne(n)"
            >
              <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
              <span aria-hidden="true" v-html="actionIconSvg('check')"></span>
            </button>
          </li>
        </ul>

        <!-- ⚠ Shown whenever there is more than one page, including while a
             page is loading: a pager that disappears between pages moves the
             button out from under the cursor that is clicking it. -->
        <footer
          v-if="pages > 1"
          class="fx-nall__foot"
          data-testid="notifications-screen-pager"
        >
          <button
            type="button"
            class="fx-nall__page"
            :disabled="page <= 1"
            @click="go(page - 1)"
          >
            {{ t('common.prev') }}
          </button>
          <span class="fx-nall__count">{{ t('notifications.pageOf', { page, pages, total: feed.mineTotal }) }}</span>
          <button
            type="button"
            class="fx-nall__page"
            :disabled="page >= pages"
            @click="go(page + 1)"
          >
            {{ t('common.next') }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style>
/* ⚠ NOT scoped: the sheet is teleported to <body>, outside this component's
   subtree. Every class is `fx-nall`-prefixed.
   ⚠ Declared `--fe-*` tokens only — it opens over the explorer, under
   whatever palette the viewer chose, and a Tailwind colour here would be the
   one surface that ignores their theme. */
.fx-nall {
  position: fixed;
  /* Short of the host's own strip (the desktop app's title bar) — core
     base.css `.fe-modal__backdrop` says why. */
  inset: var(--fe-overlay-top, 0px) 0 0;
  /* Above the bell's own teleported popover (140) — it is opened FROM it. */
  z-index: 150;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 16px;
  font-family: var(--fe-font);
  font-size: var(--fe-text-md);
}
.fx-nall__scrim {
  position: absolute;
  inset: 0;
  background: rgb(0 0 0 / 45%);
}
.fx-nall__sheet {
  position: relative;
  display: flex;
  flex-direction: column;
  width: min(720px, 100%);
  max-height: min(80vh, 720px);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-md);
  background: var(--fe-bg);
  color: var(--fe-text);
  box-shadow: var(--fe-shadow-sm);
  overflow: hidden;
}
.fx-nall__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--fe-gap-sm);
  padding: 12px 16px;
  border-bottom: 1px solid var(--fe-border-soft);
}
.fx-nall__title {
  display: inline-flex;
  align-items: center;
  gap: var(--fe-gap-sm);
  font-weight: 600;
}
/* The icon is the badge's positioning context — the badge is absolute. */
.fx-nall__title-icon {
  position: relative;
  display: inline-flex;
  color: var(--fe-text-muted);
}
.fx-nall__title-icon svg {
  width: 18px;
  height: 18px;
  display: block;
  color: inherit;
}
.fx-nall__tools {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
/* ⚠ `height: auto` on every button here, for the reason the row gives
   (NotificationRow.vue): a host's own button rules reach this sheet. */
.fx-nall__tool {
  height: auto;
  padding: 4px 8px;
  border: 1px solid transparent;
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text-muted);
  font: inherit;
  font-size: var(--fe-text-xs);
  cursor: pointer;
}
.fx-nall__tool:hover {
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-nall__tool.is-on {
  border-color: var(--fe-border-strong);
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-nall__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--fe-h-md);
  height: var(--fe-h-md);
  border: 0;
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text-muted);
  cursor: pointer;
}
.fx-nall__close:hover {
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-nall__close svg {
  width: 16px;
  height: 16px;
  display: block;
}
.fx-nall__error {
  margin: 0;
  padding: 8px 16px;
  color: var(--fe-danger);
  font-size: var(--fe-text-sm);
}
.fx-nall__list {
  margin: 0;
  padding: 6px;
  list-style: none;
  overflow-y: auto;
  overscroll-behavior: contain;
  flex: 1 1 auto;
}
.fx-nall__item {
  display: flex;
  align-items: flex-start;
  gap: 4px;
}
.fx-nall__item > .fx-nrow {
  flex: 1 1 auto;
  min-width: 0;
}
.fx-nall__read {
  flex: 0 0 auto;
  margin-top: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 0;
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text-muted);
  cursor: pointer;
}
.fx-nall__read:hover {
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-nall__read svg {
  width: 14px;
  height: 14px;
  display: block;
}
.fx-nall__empty {
  padding: 32px 16px;
  text-align: center;
  color: var(--fe-text-muted);
}
.fx-nall__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--fe-gap-sm);
  padding: 8px 16px;
  border-top: 1px solid var(--fe-border-soft);
  font-size: var(--fe-text-xs);
}
.fx-nall__page {
  height: auto;
  padding: 4px 10px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text);
  font: inherit;
  cursor: pointer;
}
.fx-nall__page:disabled {
  opacity: 0.5;
  cursor: default;
}
.fx-nall__count {
  color: var(--fe-text-muted);
}
/* At phone width the sheet is the screen: a centred card with 24px of scrim
   around it wastes the only axis a 390px viewport has. */
@media (max-width: 560px) {
  .fx-nall {
    padding: 0;
  }
  .fx-nall__sheet {
    width: 100%;
    max-height: 100%;
    height: 100%;
    border: 0;
    border-radius: 0;
  }
}
</style>
