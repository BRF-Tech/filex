/**
 * Default apps (filex 0.50) as the admin panel says them: one name for a
 * handler wherever it is drawn (the Default apps table and its editor, the
 * install review's File types group, the thumbnail repair's handler chain),
 * and the rule an editor's list turns into.
 */
import { pluginLabelOf } from '@brftech/filex-core';

import { BUILTIN_HANDLER, ONLYOFFICE_HANDLER, type FileTypeHandler, type FileTypeRule } from '@/api/fileTypes';
import type { AppPluginInstallKind, AppPluginPlace, AppPluginPlacement } from '@/api/appPlugins';

type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * A handler as a person reads it: filex's own is "filex (built-in)", the
 * document server that draws office pages (0.50) is "OnlyOffice"; an app's
 * is its label in the reader's language (the view's label for an app that
 * opens files), else its name, else the id.
 */
export function handlerLabel(h: FileTypeHandler, t: Translate, locale: string): string {
  if (h.id === BUILTIN_HANDLER) return t('defaultApps.builtin');
  if (h.id === ONLYOFFICE_HANDLER) return t('defaultApps.onlyoffice');
  return pluginLabelOf(h.label, locale) || h.app || h.id;
}

/** The label with the app's version, where telling versions apart matters
 *  (the editor, the repair's chain). */
export function handlerLabelVersioned(h: FileTypeHandler, t: Translate, locale: string): string {
  const name = handlerLabel(h, t, locale);
  return h.id !== BUILTIN_HANDLER && h.version ? `${name} ${h.version}` : name;
}

/** One handler of an editor's list: on or off, in the order drawn. */
export interface HandlerItem {
  handler: FileTypeHandler;
  on: boolean;
}

/**
 * The rule a list says: the handlers that are on, in the list's order, and
 * the ones switched off. Everything is named, so the order is the list's and
 * not the default's.
 */
export function ruleOf(items: HandlerItem[]): FileTypeRule {
  return {
    order: items.filter((i) => i.on).map((i) => i.handler.id),
    off: items.filter((i) => !i.on).map((i) => i.handler.id),
  };
}

/** An editor's list from what the server says is on and off now. */
export function itemsOf(on: FileTypeHandler[], off: FileTypeHandler[]): HandlerItem[] {
  return [...on.map((handler) => ({ handler, on: true })), ...off.map((handler) => ({ handler, on: false }))];
}

/** Two lists say the same thing (the same handlers, in the same order, each
 *  on or off the same way). */
export function sameItems(a: HandlerItem[], b: HandlerItem[]): boolean {
  return a.length === b.length && a.every((x, i) => x.handler.id === b[i].handler.id && x.on === b[i].on);
}

/* ── the File types group (an install, an upgrade, an approved request) ── */

/** One row of the File types group, as a key. */
export function fileTypeKey(r: AppPluginInstallKind): string {
  return `${r.capability}:${r.ext}:${r.handler.id}`;
}

/** Each row's choice as the server proposes it: its default place. */
export function defaultPlaces(rows: AppPluginInstallKind[] | undefined): Record<string, AppPluginPlace> {
  return Object.fromEntries((rows ?? []).map((r) => [fileTypeKey(r), r.default]));
}

/**
 * What is sent: only the rows changed from the default. Nothing changed is
 * nothing sent, so the request body is what it was before the group existed.
 */
export function changedPlacements(
  rows: AppPluginInstallKind[] | undefined,
  places: Record<string, AppPluginPlace>,
): AppPluginPlacement[] {
  return (rows ?? [])
    .filter((r) => places[fileTypeKey(r)] && places[fileTypeKey(r)] !== r.default)
    .map((r) => ({ capability: r.capability, ext: r.ext, handler: r.handler.id, place: places[fileTypeKey(r)] }));
}
