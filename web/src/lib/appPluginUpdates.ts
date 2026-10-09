/**
 * What the Apps list says about an app's updates and its `filex` range —
 * the "Updates" cell, its sort rank, and whether the row offers "Review
 * update". Pure, so the words and the decisions are tested without a table.
 *
 * The facts are the server's (wasmplugin.Status: `update`, `update_source`,
 * `compat`, `previous`); nothing here decides what an update IS — only how
 * it is said:
 *
 *   · `available`      a newer version waits for an administrator
 *   · `needs_approval` it asks for new permissions, or a pack brings a module
 *   · `failed`         (a row filex 0.47 wrote) its automatic update was
 *                      tried and undone
 *   · `check_failed`   the source could not be read
 *   · `incompatible`   only newer versions that need a newer filex
 *   · an app with no source says so — an uploaded app is never checked
 *   · the version the last approval replaced, when one is kept to go back to
 *   · `compat.ok === false`: the installed version is outside its own range
 *     for THIS filex (it keeps running; the badge is the warning)
 *
 * ⚠⚠ Nothing updates itself (filex 0.48, owner's rule): there is no
 * "updated automatically" to say and no switch to say it is off.
 */
import { refusalOf, type AppPlugin } from '@/api/appPlugins';

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

/**
 * ⚠⚠ The lines are the SERVER's (handlers sayStatus, 0.55): `update_said`,
 * `compat.message` and `previous.message`, written in the reader's language.
 * This builds no sentence from the row's fields and decides none of them;
 * it picks the badges (status words) and lays the server's lines out.
 */
export function updateView(p: AppPlugin, t: Translate, _locale?: string): UpdateView {
  const view = updateFacts(p, t);
  if (p.previous?.message) view.lines.push(p.previous.message);
  return view;
}

function updateFacts(p: AppPlugin, t: Translate): UpdateView {
  const view: UpdateView = { badges: [], lines: [], reviewable: false };
  if (p.compat && !p.compat.ok) {
    view.badges.push({
      tone: 'rose',
      label: t('appPlugins.compat.bad'),
      title: p.compat.message || undefined,
      testid: `app-plugin-compat-${p.name}`,
    });
    if (p.compat.message) view.lines.push(p.compat.message);
  }
  if (!p.update_source) {
    // No source to check: the server says which (from a file, or from an
    // address filex did not keep).
    if (p.update_said) view.lines.push(p.update_said);
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
      if (p.update_said) parts.push(p.update_said);
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
        // The server's sentence, said in the reader's language when the
        // list was read (handlers sayStatus).
        title: refusal?.message || undefined,
        testid: `app-plugin-update-failed-${p.name}`,
      });
      if (p.update_said) view.lines.push(p.update_said);
      view.reviewable = true;
      return view;
    }
    case 'check_failed': {
      const refusal = refusalOf(u.refusal);
      const why = p.update_said || refusal?.message || '';
      view.badges.push({ tone: 'zinc', label: t('appPlugins.update.checkFailed'), title: why || undefined, testid: `app-plugin-update-unchecked-${p.name}` });
      if (why) view.lines.push(why);
      return view;
    }
    case 'incompatible':
      if (p.update_said) view.lines.push(p.update_said);
      return view;
  }
  view.lines.push(u?.status === 'current' ? t('appPlugins.update.upToDate') : t('appPlugins.update.notChecked'));
  return view;
}
