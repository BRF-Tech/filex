/**
 * The mega menu's model - what a host hands `MegaMenu.vue`, and the rules that
 * do not depend on how it is drawn.
 *
 * GitHub #82 (0.51.0): the admin panel's flat sidebar of 37 pages became a
 * mega menu - a few top entries, each opening a panel whose pages sit in
 * named sections, with a short line under each page. The owner chose the
 * shape (2026-10-03): three top entries with the sections as columns, the
 * dashboard as a plain link before them, a headed list per entry on a phone.
 *
 * ⚠ The menu knows nothing about routes or permissions. The HOST decides
 * which pages a person may open and passes only those; this module's one
 * rule about visibility is the one every host would otherwise write again:
 * a section with no page left is not drawn, and neither is an entry with no
 * section left (a heading over nothing is a promise the deployment cannot
 * keep - the old sidebar's own rule for its Apps heading).
 *
 * Pure: no DOM, and Vue only for the type of a host's icon component.
 */
import type { Component } from 'vue';

import { inlineKeyStep, type TextDirection } from './direction';

/** One page of a section. */
export interface MegaMenuItem {
  /** Stable key and test hook (`nav-<id>`); the admin panel uses the route name. */
  id: string;
  label: string;
  /** The short line under the label. */
  hint?: string;
  /** Where the item goes. A real address, so a middle click opens a tab. */
  href: string;
  /** The page being looked at (or one of its own sub-pages). */
  active?: boolean;
  /** A glyph from the HOST's icon set, drawn as a component. */
  icon?: Component;
  /**
   * Or a glyph from the core icon library, by NAME (`lib/actionIcons`). Only
   * the name crosses this boundary - never markup - so the menu draws static
   * SVG this package wrote, whoever the host is.
   */
  iconName?: string;
}

/** A named group of pages inside an entry's panel (a column on a wide screen). */
export interface MegaMenuSection {
  id: string;
  label: string;
  items: MegaMenuItem[];
}

/**
 * A top entry: either a panel of sections, or - with `href` and no sections -
 * a plain link (the dashboard).
 */
export interface MegaMenuEntry {
  id: string;
  label: string;
  sections?: MegaMenuSection[];
  href?: string;
  active?: boolean;
  icon?: Component;
}

/** True for an entry drawn as a plain link rather than a panel. */
export function isLinkEntry(entry: MegaMenuEntry): boolean {
  return !entry.sections && typeof entry.href === 'string';
}

/**
 * The entries as they are drawn: sections with no item dropped, then entries
 * with no section dropped. A plain-link entry stays as it is.
 */
export function pruneMegaMenu(entries: MegaMenuEntry[]): MegaMenuEntry[] {
  const out: MegaMenuEntry[] = [];
  for (const entry of entries) {
    if (isLinkEntry(entry)) {
      out.push(entry);
      continue;
    }
    const sections = (entry.sections ?? []).filter((s) => s.items.length > 0);
    if (sections.length > 0) out.push({ ...entry, sections });
  }
  return out;
}

/** Does this entry hold the page being looked at? */
export function entryIsCurrent(entry: MegaMenuEntry): boolean {
  if (isLinkEntry(entry)) return entry.active === true;
  return (entry.sections ?? []).some((s) => s.items.some((i) => i.active === true));
}

/**
 * Where a key press inside a run of links moves focus: the next or previous
 * one, the first or the last. `null` for a key this does not handle. The
 * run does not wrap - Down on the last link stays there - which is how a
 * reader knows they reached the end.
 */
export function stepFocus(key: string, index: number, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case 'ArrowDown':
      return Math.min(index + 1, count - 1);
    case 'ArrowUp':
      return Math.max(index - 1, 0);
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return null;
  }
}

/**
 * Left and right along the top entries, in the direction the interface is
 * drawn: in a right-to-left interface the right arrow goes to the PREVIOUS
 * entry. Wraps, as a menu bar does. `null` for any other key.
 */
export function stepAlongBar(key: string, index: number, count: number, dir: TextDirection = 'ltr'): number | null {
  if (count <= 0) return null;
  const step = inlineKeyStep(key, dir);
  if (step !== 0) return (index + step + count) % count;
  if (key === 'Home') return 0;
  if (key === 'End') return count - 1;
  return null;
}

/**
 * A plain left click that the HOST should turn into an in-app navigation. A
 * click with a modifier (new tab, new window, download) or another button is
 * left to the browser, which is what makes the items real links.
 */
export function isPlainClick(ev: Pick<MouseEvent, 'button' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey' | 'defaultPrevented'>): boolean {
  return ev.button === 0 && !ev.metaKey && !ev.ctrlKey && !ev.shiftKey && !ev.altKey && !ev.defaultPrevented;
}
