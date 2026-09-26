/**
 * What the Apps list says about an app's updates and its `filex` range —
 * the "Updates" cell, its sort rank, and whether the row offers "Review
 * update". Pure, so the words and the decisions are tested without a table.
 *
 * The facts are the server's (wasmplugin.Status: `update`, `update_source`,
 * `compat`, `auto_update`); nothing here decides what an update IS — only how
 * it is said:
 *
 *   · `available`      a newer version waits (automatic updates are off)
 *   · `needs_approval` it asks for new permissions, or a pack brings a module
 *   · `failed`         the automatic update was tried and undone
 *   · `check_failed`   the source could not be read
 *   · `incompatible`   only newer versions that need a newer filex
 *   · the last automatic update, for a week after it happened
 *   · an app with no source says so — an uploaded app is never checked
 *   · automatic updates switched off, said under whatever else is said
 *   · `compat.ok === false`: the installed version is outside its own range
 *     for THIS filex (it keeps running; the badge is the warning)
 */
import { refusalOf, type AppPlugin } from '@/api/appPlugins';
import { refusalSentence } from '@/lib/appPluginRefusal';
import { formatDate } from '@/lib/format';

type Translate = (key: string, values?: Record<string, unknown>) => string;

export type UpdateTone = 'emerald' | 'amber' | 'rose' | 'sky' | 'zinc';

export interface UpdateBadge {
  tone: UpdateTone;
  label: string;
  /** The longer explanation, as a tooltip. */
  title?: string;
  /** `app-plugin-update-<kind>-<name>` — what a browser test reads. */
  testid: string;
}

export interface UpdateView {
  badges: UpdateBadge[];
  /** The lines under the badges, in the reader's words. */
  lines: string[];
  /** A newer version the administrator can review and install from here. */
  reviewable: boolean;
}

/** How long "Updated automatically" stays on a row. */
export const AUTO_UPDATED_SHOWN_MS = 7 * 24 * 60 * 60 * 1000;

const RANK: Record<string, number> = {
  needs_approval: 0,
  failed: 1,
  available: 2,
  check_failed: 3,
  incompatible: 4,
};

/** Sort rank of a row by what its updates need: the most urgent first. */
export function updateRank(p: AppPlugin): number {
  if (p.compat && !p.compat.ok) return -1;
  return RANK[p.update?.status ?? ''] ?? 9;
}

export function updateView(p: AppPlugin, t: Translate, locale: string, now = Date.now()): UpdateView {
  const view = updateFacts(p, t, locale, now);
  if (p.update_source && p.auto_update === false) view.lines.push(t('appPlugins.update.autoOff'));
  return view;
}

function updateFacts(p: AppPlugin, t: Translate, locale: string, now: number): UpdateView {
  const view: UpdateView = { badges: [], lines: [], reviewable: false };
  if (p.compat && !p.compat.ok) {
    view.badges.push({
      tone: 'rose',
      label: t('appPlugins.compat.bad'),
      title: t('appPlugins.compat.needs', { requires: p.compat.requires, filex: p.compat.filex }),
      testid: `app-plugin-compat-${p.name}`,
    });
    view.lines.push(t('appPlugins.compat.needs', { requires: p.compat.requires, filex: p.compat.filex }));
  }
  if (!p.update_source) {
    // ⚠ Not "installed from a file" for every app without one: an app
    // installed from an ADDRESS before 0.47 has one, but filex did not keep
    // its manifest's address (manifest_url), and saying "from a file" about
    // it would be false.
    view.lines.push(t(p.source === 'url' ? 'appPlugins.update.noManifestAddress' : 'appPlugins.update.noSource'));
    return view;
  }
  const u = p.update;
  const jump = `${p.version} → ${u?.version ?? ''}`;
  switch (u?.status) {
    case 'available':
      view.badges.push({ tone: 'sky', label: t('appPlugins.update.available'), testid: `app-plugin-update-available-${p.name}` });
      view.lines.push(jump);
      view.reviewable = true;
      return view;
    case 'needs_approval': {
      const parts = [jump];
      if (u.added?.length) parts.push(t('appPlugins.update.newPermissions', { permissions: u.added.join(', ') }));
      if (u.adds_module) parts.push(t('appPlugins.update.addsModule'));
      view.badges.push({ tone: 'amber', label: t('appPlugins.update.needsApproval'), testid: `app-plugin-update-approval-${p.name}` });
      view.lines.push(parts.join(' · '));
      view.reviewable = true;
      return view;
    }
    case 'failed': {
      const refusal = refusalOf(u.refusal);
      view.badges.push({
        tone: 'rose',
        label: t('appPlugins.update.failed'),
        title: (refusal && refusalSentence(refusal, t)) || undefined,
        testid: `app-plugin-update-failed-${p.name}`,
      });
      view.lines.push(t('appPlugins.update.failedDetail', { version: u.version ?? '', current: p.version }));
      view.reviewable = true;
      return view;
    }
    case 'check_failed': {
      const refusal = refusalOf(u.refusal);
      const why = (refusal && refusalSentence(refusal, t)) || '';
      view.badges.push({ tone: 'zinc', label: t('appPlugins.update.checkFailed'), title: why || undefined, testid: `app-plugin-update-unchecked-${p.name}` });
      if (why) view.lines.push(why);
      return view;
    }
    case 'incompatible':
      view.lines.push(t('appPlugins.update.needsNewerFilex', { version: u.version ?? '', requires: u.requires ?? '' }));
      return view;
  }
  const auto = u?.auto;
  if (auto && auto.to === p.version && now - Date.parse(auto.at) < AUTO_UPDATED_SHOWN_MS) {
    view.badges.push({ tone: 'emerald', label: t('appPlugins.update.updated'), testid: `app-plugin-update-auto-${p.name}` });
    view.lines.push(t('appPlugins.update.autoFrom', { from: auto.from, when: formatDate(auto.at, locale) }));
    return view;
  }
  view.lines.push(u?.status === 'current' ? t('appPlugins.update.upToDate') : t('appPlugins.update.notChecked'));
  return view;
}
