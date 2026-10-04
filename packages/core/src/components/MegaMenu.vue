<script setup lang="ts">
/**
 * MegaMenu - a few top entries, each opening a panel of named sections, each
 * section a list of pages with a short line under each (GitHub #82, 0.51.0).
 *
 * Drawn by the admin panel's top bar (web/src/components/AdminNav.vue), and
 * written here rather than there because it is a navigation PATTERN, not the
 * admin panel's: the host passes the entries, already filtered to what the
 * reader may open, and gets `navigate` back. Nothing in this file names a
 * route, a permission or a page.
 *
 * Two ways to draw the same entries:
 *
 *   bar   a wide screen. Each entry is a button that opens its panel under
 *         the bar; a plain-link entry (the dashboard) is a link. One panel
 *         open at a time; the sections are its columns.
 *   list  a phone's drawer. Every entry as a heading with its sections under
 *         it, all open: the menu button and the page are the only two taps.
 *         The short lines are left out there - on a phone the list is long
 *         enough with the labels alone.
 *
 * ⚠ Keyboard and screen reader: the WAI-ARIA "disclosure navigation" pattern,
 * NOT `role="menu"`. These are links to pages, and a `menu` role would take
 * away the link semantics (no "open in new tab", a screen reader announcing
 * menu items instead of links). Each top button carries `aria-expanded` and
 * `aria-controls`; each section's list is labelled by its heading; the page
 * being looked at carries `aria-current="page"`.
 *   ← / →        along the top entries (mirrored in a right-to-left interface)
 *   Home / End   first / last top entry, or first / last page in a panel
 *   ↓            on a top button: open its panel and go to its first page;
 *                in a panel: the next page
 *   ↑            in a panel: the previous page, from the first one back to
 *                the panel's button
 *   Esc          close the panel and give focus back to its button
 * Tab moves through everything in document order; focus leaving the menu, a
 * click outside it and a navigation all close the open panel.
 *
 * ⚠ Opens on a click, never on hover (owner's choice, 2026-10-03): a panel
 * that opens when the pointer crosses the bar on its way to the page is a
 * panel in the way, and a touch screen has no hover to agree with.
 *
 * ⚠ Not `position: fixed; inset: 0` anywhere (web/tests/api/overlayTop.test.ts):
 * the panel hangs under the bar inside the menu's own box, so nothing here
 * covers the window.
 *
 * ⚠⚠ Its rules are in styles/base.css (`.fx-mega`), NOT in a `<style>` block
 * here. A single-file component's styles reach only a bundle that imports it,
 * and the web-component distribution does not import this one - so the two
 * shipped stylesheets drifted while they lived here
 * (web/tests/deploy/packageLook.test.ts, 2026-10-03; ConnectionNotice learned
 * the same on 2026-09-24).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { actionIconSvg } from '../lib/actionIcons';
import { dirOfElement } from '../lib/direction';
import {
  entryIsCurrent,
  isLinkEntry,
  isPlainClick,
  pruneMegaMenu,
  stepAlongBar,
  stepFocus,
  type MegaMenuEntry,
} from '../lib/megaMenu';

const props = withDefaults(
  defineProps<{
    /** The entries, already limited to what the reader may open. */
    entries: MegaMenuEntry[];
    /** The navigation's accessible name. */
    label: string;
    mode?: 'bar' | 'list';
  }>(),
  { mode: 'bar' },
);

const emit = defineEmits<{
  /** A plain click on a page: the host navigates (a modified click is the browser's). */
  (e: 'navigate', target: { id: string; href: string }): void;
}>();

/* The ids that tie a button to its panel and a list to its heading. Random,
   not a counter: two menus on one page (a test, a host drawing both modes)
   must not share them, and `useId` is Vue 3.5 while the package asks 3.4. */
const uid = `fx-mega-${Math.random().toString(36).slice(2, 10)}`;

const shown = computed(() => pruneMegaMenu(props.entries));

const root = ref<HTMLElement | null>(null);
const openId = ref<string | null>(null);

const panelId = (entry: MegaMenuEntry) => `${uid}-${entry.id}`;
const headingId = (entry: MegaMenuEntry, sectionId: string) => `${uid}-${entry.id}-${sectionId}`;

function close(): void {
  openId.value = null;
}

function toggle(id: string): void {
  openId.value = openId.value === id ? null : id;
}

/* ── Focus helpers ──────────────────────────────────────────────────────── */

function topControls(): HTMLElement[] {
  return Array.from(root.value?.querySelectorAll<HTMLElement>('[data-mega-top]') ?? []);
}

function panelLinks(entryId: string): HTMLElement[] {
  const panel = root.value?.querySelector<HTMLElement>(`[data-mega-panel="${entryId}"]`);
  return Array.from(panel?.querySelectorAll<HTMLElement>('a.fx-mega__item') ?? []);
}

function topButton(entryId: string): HTMLElement | null {
  return root.value?.querySelector<HTMLElement>(`[data-mega-top="${entryId}"]`) ?? null;
}

async function openAndFocus(entryId: string, which: 'first' | 'last'): Promise<void> {
  openId.value = entryId;
  await nextTick();
  const links = panelLinks(entryId);
  (which === 'first' ? links[0] : links[links.length - 1])?.focus();
}

/* ── Keyboard ───────────────────────────────────────────────────────────── */

function onTopKey(ev: KeyboardEvent, index: number, entry: MegaMenuEntry): void {
  const controls = topControls();
  const next = stepAlongBar(ev.key, index, controls.length, dirOfElement(root.value));
  if (next !== null) {
    ev.preventDefault();
    close();
    controls[next]?.focus();
    return;
  }
  if (isLinkEntry(entry)) return;
  if (ev.key === 'ArrowDown') {
    ev.preventDefault();
    void openAndFocus(entry.id, 'first');
  } else if (ev.key === 'ArrowUp') {
    ev.preventDefault();
    void openAndFocus(entry.id, 'last');
  } else if (ev.key === 'Escape' && openId.value === entry.id) {
    ev.preventDefault();
    close();
  }
}

function onPanelKey(ev: KeyboardEvent, entry: MegaMenuEntry): void {
  if (ev.key === 'Escape') {
    ev.preventDefault();
    ev.stopPropagation();
    close();
    topButton(entry.id)?.focus();
    return;
  }
  const links = panelLinks(entry.id);
  const at = links.indexOf(document.activeElement as HTMLElement);
  if (at < 0) return;
  if (ev.key === 'ArrowUp' && at === 0) {
    ev.preventDefault();
    topButton(entry.id)?.focus();
    return;
  }
  const next = stepFocus(ev.key, at, links.length);
  if (next === null) return;
  ev.preventDefault();
  links[next]?.focus();
}

/* ── Pointer and focus leaving ──────────────────────────────────────────── */

function onItemClick(ev: MouseEvent, target: { id: string; href?: string }): void {
  if (!target.href || !isPlainClick(ev)) return;
  ev.preventDefault();
  close();
  emit('navigate', { id: target.id, href: target.href });
}

function onFocusOut(ev: FocusEvent): void {
  const to = ev.relatedTarget as Node | null;
  // ⚠ A null `relatedTarget` is focus going nowhere in particular - the page
  // body after a click on empty space, or the window losing focus. The click
  // case is the document listener's; closing here too would close a panel
  // the moment its own button is clicked in a browser that blurs first.
  if (to && root.value && !root.value.contains(to)) close();
}

function onDocPointer(ev: PointerEvent): void {
  if (openId.value === null) return;
  const t = ev.target as Node | null;
  if (t && root.value && !root.value.contains(t)) close();
}

onMounted(() => document.addEventListener('pointerdown', onDocPointer, true));
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocPointer, true));

/* A navigation the menu did not make (Back, a link on the page) still closes it. */
const currentKey = computed(() =>
  shown.value
    .flatMap((e) =>
      isLinkEntry(e) ? (e.active ? [e.id] : []) : (e.sections ?? []).flatMap((s) => s.items.filter((i) => i.active).map((i) => i.id)),
    )
    .join('|'),
);
watch(currentKey, close);
</script>

<template>
  <nav
    ref="root"
    :class="['fx-mega', `fx-mega--${mode}`]"
    :aria-label="label"
    data-testid="mega-menu"
    @focusout="onFocusOut"
  >
    <!-- ── Wide screen: the bar ─────────────────────────────────────────── -->
    <ul v-if="mode === 'bar'" class="fx-mega__bar">
      <li
        v-for="(entry, i) in shown"
        :key="entry.id"
        class="fx-mega__top"
      >
        <a
          v-if="isLinkEntry(entry)"
          :href="entry.href"
          :class="['fx-mega__toplink', entry.active && 'is-current']"
          :aria-current="entry.active ? 'page' : undefined"
          :data-mega-top="entry.id"
          :data-testid="`nav-${entry.id}`"
          @click="onItemClick($event, entry)"
          @keydown="onTopKey($event, i, entry)"
        >{{ entry.label }}</a>
        <template v-else>
          <button
            type="button"
            :class="['fx-mega__topbtn', openId === entry.id && 'is-open', entryIsCurrent(entry) && 'is-current']"
            :aria-expanded="openId === entry.id ? 'true' : 'false'"
            :aria-controls="panelId(entry)"
            :data-mega-top="entry.id"
            :data-testid="`nav-top-${entry.id}`"
            @click="toggle(entry.id)"
            @keydown="onTopKey($event, i, entry)"
          >
            <span>{{ entry.label }}</span>
            <svg
              class="fx-mega__chev"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            ><path d="m6 9 6 6 6-6" /></svg>
          </button>
          <div
            v-show="openId === entry.id"
            :id="panelId(entry)"
            class="fx-mega__panel"
            :data-mega-panel="entry.id"
            :data-testid="`nav-panel-${entry.id}`"
            @keydown="onPanelKey($event, entry)"
          >
            <div
              v-for="section in entry.sections"
              :key="section.id"
              class="fx-mega__section"
              :data-testid="`nav-group-${section.id}`"
            >
              <p :id="headingId(entry, section.id)" class="fx-mega__heading">{{ section.label }}</p>
              <ul class="fx-mega__items" :aria-labelledby="headingId(entry, section.id)">
                <li v-for="item in section.items" :key="item.id">
                  <a
                    :href="item.href"
                    :class="['fx-mega__item', item.active && 'is-current']"
                    :aria-current="item.active ? 'page' : undefined"
                    :data-testid="`nav-${item.id}`"
                    @click="onItemClick($event, item)"
                  >
                    <span class="fx-mega__icon" aria-hidden="true">
                      <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons, chosen by name -->
                      <span v-if="item.iconName" class="fx-mega__glyph" v-html="actionIconSvg(item.iconName)"></span>
                      <component :is="item.icon" v-else-if="item.icon" class="fx-mega__glyph" />
                    </span>
                    <span class="fx-mega__text">
                      <span class="fx-mega__label">{{ item.label }}</span>
                      <span v-if="item.hint" class="fx-mega__hint">{{ item.hint }}</span>
                    </span>
                  </a>
                </li>
              </ul>
            </div>
          </div>
        </template>
      </li>
    </ul>

    <!-- ── Phone: every entry as a headed list, all open ────────────────── -->
    <div v-else class="fx-mega__list">
      <template v-for="entry in shown" :key="entry.id">
        <a
          v-if="isLinkEntry(entry)"
          :href="entry.href"
          :class="['fx-mega__item', 'fx-mega__item--top', entry.active && 'is-current']"
          :aria-current="entry.active ? 'page' : undefined"
          :data-testid="`nav-${entry.id}`"
          @click="onItemClick($event, entry)"
        >
          <span class="fx-mega__icon" aria-hidden="true">
            <component :is="entry.icon" v-if="entry.icon" class="fx-mega__glyph" />
          </span>
          <span class="fx-mega__text"><span class="fx-mega__label">{{ entry.label }}</span></span>
        </a>
        <section
          v-else
          class="fx-mega__group"
          :aria-labelledby="panelId(entry)"
          :data-testid="`nav-top-${entry.id}`"
        >
          <p :id="panelId(entry)" class="fx-mega__grouphead">{{ entry.label }}</p>
          <div
            v-for="section in entry.sections"
            :key="section.id"
            class="fx-mega__section"
            :data-testid="`nav-group-${section.id}`"
          >
            <p :id="headingId(entry, section.id)" class="fx-mega__heading">{{ section.label }}</p>
            <ul class="fx-mega__items" :aria-labelledby="headingId(entry, section.id)">
              <li v-for="item in section.items" :key="item.id">
                <a
                  :href="item.href"
                  :class="['fx-mega__item', item.active && 'is-current']"
                  :aria-current="item.active ? 'page' : undefined"
                  :data-testid="`nav-${item.id}`"
                  @click="onItemClick($event, item)"
                >
                  <span class="fx-mega__icon" aria-hidden="true">
                    <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons, chosen by name -->
                    <span v-if="item.iconName" class="fx-mega__glyph" v-html="actionIconSvg(item.iconName)"></span>
                    <component :is="item.icon" v-else-if="item.icon" class="fx-mega__glyph" />
                  </span>
                  <span class="fx-mega__text"><span class="fx-mega__label">{{ item.label }}</span></span>
                </a>
              </li>
            </ul>
          </div>
        </section>
      </template>
    </div>
  </nav>
</template>
