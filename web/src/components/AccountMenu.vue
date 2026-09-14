<script setup lang="ts">
/**
 * AccountMenu — one avatar, one menu, for this account.
 *
 * Owner's decision, 2026-09-13, verbatim: *"Tek avatar, menü açılsın."* The
 * explorer's header used to end in three separate icon buttons — admin panel,
 * user settings, sign out. Three glyphs for one subject is three chances to
 * misread the row; the reference shell draws one round avatar with a chevron
 * and puts the verbs inside it. The admin entry is still the one extra thing an
 * administrator gets, as a menu row rather than a fourth icon.
 *
 * ⚠ Rows come from the PARENT. This component knows who is signed in — that is
 * what an avatar is — and nothing about routes, modals or sessions. The page
 * owns those, and it also owns the second copy of this control in its
 * no-storages empty state, so one list of rows feeds both.
 *
 * ⚠⚠ The panel is TELEPORTED, and that is not a style choice: `.fe` (the
 * explorer's root) carries `overflow: hidden`, so a menu positioned inside the
 * header is clipped at the header's own edge. Measured — without the teleport
 * the rows below the first are simply not on screen. Position is taken from the
 * button's rectangle when it is pressed, anchored to its RIGHT edge, because
 * this control sits at the end of the row and a left-edge anchor hangs it off
 * the viewport.
 *
 * ⚠ Headless UI's `Menu`, the same primitive the admin panel's account menu
 * uses: roving focus, Esc, click-outside and the `open` state all come from it,
 * and `open` is a state we can actually observe — the thing a menu button must
 * not lie about in `aria-expanded`.
 */
import { computed, ref } from 'vue';
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/vue';
/* gorunum:v4-hostmenu — the explorer's OWN action glyphs, not a second set.
 * Owner's decision, 2026-09-13, verbatim: *"Bizim profil altındaki itemlere
 * ikon koymamız şart."* The rows in this menu now include the explorer's
 * settings rows, which are drawn four pixels from the explorer's own toolbar —
 * so they are drawn from the explorer's own vocabulary (stroked, 24×24,
 * `currentColor`), and never from an emoji or a second icon library. */
import { actionIconSvg } from '@brftech/filex-core';

import { useAuthStore } from '@/stores/auth';
import { anchorUnderRightEdge, refElement } from '@/lib/anchoredPanel';

export interface AccountAction {
  key: string;
  label: string;
  /** Draw a hairline above this row. */
  separated?: boolean;
  /**
   * An `actionIcons` key. Absent → the row's own `key` is tried, which is why
   * the explorer's rows (`refresh`, `theme`, `tour`, `shortcut-settings`, …)
   * need no mapping at all: their key IS the glyph's name. A key nothing is
   * drawn for renders an empty box of the same width, so a row with no glyph
   * still lines its label up with the rows that have one.
   */
  icon?: string;
}

const props = defineProps<{
  /** The rows, in order. */
  actions: AccountAction[];
  locale: 'en' | 'tr';
  /** Accessible name when this account has neither a name nor an address. */
  fallbackLabel: string;
}>();

const emit = defineEmits<{ (e: 'select', key: string): void }>();

const auth = useAuthStore();

/** The account's own words for itself, best first. */
const displayName = computed(() => {
  const u = auth.user;
  return (
    (u?.display_name || '').trim() ||
    (u?.username || '').trim() ||
    (u?.email || '').trim()
  );
});

const avatarUrl = computed(() => (auth.user?.avatar_url || '').trim());

/**
 * The letter in the circle.
 *
 * ⚠⚠ The empty case is not rare and has to be measured: an account an operator
 * created with only a login, or a session whose `/api/auth/me` has not landed
 * yet, has no display name AND no address. A blank circle in the corner of
 * every screen is worse than the three icons it replaced, so '' here means the
 * template draws a person glyph — never nothing.
 *
 * ⚠ `[...s][0]`, not `s[0]`: a name beginning with an emoji or any astral
 * character is a surrogate PAIR, and one code unit of it prints as �.
 *
 * ⚠ `toLocaleUpperCase` with the real locale: in Turkish the capital of `i` is
 * `İ`, and `toUpperCase()` would put an `I` on İsmail's avatar — the exact
 * class of mistake this project's Turkish-character rule exists to stop.
 */
const initial = computed(() => {
  const s = displayName.value;
  if (!s) return '';
  return [...s][0].toLocaleUpperCase(props.locale === 'tr' ? 'tr-TR' : 'en-US');
});

const label = computed(() => displayName.value || props.fallbackLabel);

/* ── where the teleported panel goes ─────────────────────────────────── */
const btnEl = ref<InstanceType<typeof MenuButton> | null>(null);
const pos = ref({ top: '0px', right: '0px' });

/**
 * Recorded from the button, not from the panel: the panel does not exist until
 * it opens. Bound to BOTH click and keydown because Headless UI opens on
 * ArrowDown/Enter without a click, and a stale rectangle would put the menu
 * wherever the button used to be. Neither handler intercepts the event.
 */
function syncPos() {
  const r = refElement(btnEl.value)?.getBoundingClientRect();
  if (!r) return;
  const { top, right } = anchorUnderRightEdge(r, { width: window.innerWidth, height: window.innerHeight });
  pos.value = { top, right };
}
</script>

<template>
  <Menu v-slot="{ open }" as="div" class="fx-account">
    <MenuButton
      ref="btnEl"
      class="fx-account__btn"
      :class="{ 'is-open': open }"
      :title="label"
      :aria-label="label"
      data-testid="explore-account"
      @click="syncPos"
      @keydown="syncPos"
    >
      <span class="fx-account__face" aria-hidden="true">
        <img v-if="avatarUrl" class="fx-account__img" :src="avatarUrl" alt="" />
        <span v-else-if="initial" class="fx-account__initial">{{ initial }}</span>
        <!-- Neither a picture nor a letter: a person, never an empty circle. -->
        <svg
          v-else
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          focusable="false"
        >
          <circle cx="12" cy="8.5" r="3.5" />
          <path d="M5.5 19.5a6.5 6.5 0 0 1 13 0" />
        </svg>
      </span>
      <svg
        class="fx-account__chev"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M7 10l5 5 5-5" />
      </svg>
    </MenuButton>

    <Teleport to="body">
      <transition
        enter-active-class="fx-acct-in"
        leave-active-class="fx-acct-out"
      >
        <MenuItems
          class="fx-acctmenu"
          :style="{ top: pos.top, right: pos.right }"
          data-testid="explore-account-menu"
        >
          <!-- The signed-in identity, as a heading rather than a row: it is
               what the avatar stands for, and a menu whose first line is not
               clickable is how you say "this is who you are" without offering
               an action that does not exist. -->
          <p class="fx-acctmenu__who">{{ label }}</p>
          <MenuItem v-for="a in actions" :key="a.key" v-slot="{ active }">
            <button
              type="button"
              class="fx-acctmenu__item"
              :class="{ 'is-active': active, 'is-separated': a.separated }"
              :data-testid="`explore-${a.key}`"
              @click="emit('select', a.key)"
            >
              <!-- eslint-disable-next-line vue/no-v-html — static markup from
                   @brftech/filex-core's lib/actionIcons, never user input -->
              <span
                class="fx-acctmenu__icon"
                aria-hidden="true"
                v-html="actionIconSvg(a.icon || a.key)"
              ></span>
              <span class="fx-acctmenu__label">{{ a.label }}</span>
            </button>
          </MenuItem>
        </MenuItems>
      </transition>
    </Teleport>
  </Menu>
</template>

<style scoped>
/* ⚠ Declared `--fe-*` tokens only — no Tailwind `brand-*`. This control sits
   inside the explorer's header and has to answer to the same palette as
   everything beside it (including the theme gallery and a host override); and
   Vite reads tailwind.config.js once at startup, so a `brand-*` colour measured
   on a long-running dev server can be a stale lie. */
.fx-account {
  display: inline-flex;
  position: relative;
}
.fx-account__btn {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  height: var(--fe-h-md);
  padding: 0 4px 0 2px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  color: var(--fe-text-muted);
  cursor: pointer;
}
.fx-account__btn:hover,
.fx-account__btn.is-open {
  background: var(--fe-bg-hover);
  color: var(--fe-text);
}
.fx-account__btn:focus-visible {
  outline: 2px solid var(--fe-primary);
  outline-offset: 1px;
}

/* The circle: a tinted ground with the initial in the primary colour — the
   shape every product draws for an account, in two theme-aware tokens. */
.fx-account__face {
  flex: 0 0 auto;
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  overflow: hidden;
  background: var(--fe-primary-soft);
  color: var(--fe-primary);
}
.fx-account__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.fx-account__initial {
  font-size: var(--fe-text-sm);
  font-weight: 600;
  line-height: 1;
}
.fx-account__face svg {
  width: 16px;
  height: 16px;
}
.fx-account__chev {
  width: 14px;
  height: 14px;
  opacity: 0.7;
}
</style>

<style>
/* ⚠ NOT scoped. The panel is teleported to <body>, which puts it outside this
   component's DOM subtree — a scoped rule's `[data-v-…]` attribute is stamped
   on the elements, but the whole block is easier to read as what it is: the
   product's menu, drawn in the product's tokens, living under <body>. */
.fx-acctmenu {
  position: fixed;
  /* Above the explorer's own overlays. Its context menus sit at 90 and its
     tour card at 96 (both appended to <body>, both fixed), and an account menu
     that opened underneath one of them would look like it had not opened. */
  z-index: 140;
  min-width: 210px;
  max-width: 280px;
  /* ⚠ The list is no longer three rows. Since gorunum:v4-hostmenu it also
     carries whatever the explorer's "⋯" was carrying, which at 390px with a
     selection standing is a dozen rows — and this panel is `position: fixed`
     under <body>, so nothing else would ever clip it: it would simply run off
     the bottom of the phone with the last rows unreachable. The cap is the
     viewport minus the header it hangs from. */
  max-height: calc(100vh - 76px);
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 4px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-md);
  background: var(--fe-bg);
  color: var(--fe-text);
  box-shadow: var(--fe-shadow-sm);
  font-family: var(--fe-font);
  font-size: var(--fe-text-md);
  outline: none;
}
.fx-acctmenu__who {
  margin: 0;
  padding: 6px 10px 8px;
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fx-acctmenu__item {
  display: flex;
  align-items: center;
  gap: var(--fe-gap-sm);
  width: 100%;
  padding: 7px 10px;
  border: 0;
  border-radius: var(--fe-radius-sm);
  background: transparent;
  color: var(--fe-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
/* ⚠ A fixed box, drawn or not. The glyph column has to be a column: a row
   whose key nothing is drawn for would otherwise slide its label 24px left and
   the menu would read as two lists. `--fe-text-muted` so the labels stay the
   loudest thing in the row — an icon is a landmark, not the message. */
.fx-acctmenu__icon {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--fe-text-muted);
}
/* ⚠ No `:deep()` — this block is deliberately NOT scoped (the panel is
   teleported to <body>), so a plain descendant selector is the right one and
   `:deep()` here would be a selector the browser drops. */
.fx-acctmenu__icon svg {
  width: 16px;
  height: 16px;
  display: block;
}
.fx-acctmenu__item.is-active .fx-acctmenu__icon {
  color: var(--fe-text);
}
.fx-acctmenu__label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fx-acctmenu__item.is-active {
  background: var(--fe-bg-hover);
}
.fx-acctmenu__item.is-separated {
  margin-top: 4px;
  border-top: 1px solid var(--fe-border-soft);
  border-top-left-radius: 0;
  border-top-right-radius: 0;
  padding-top: 11px;
}
.fx-acct-in {
  transition: opacity 0.09s ease;
}
.fx-acct-out {
  transition: opacity 0.07s ease;
  opacity: 0;
}
</style>
