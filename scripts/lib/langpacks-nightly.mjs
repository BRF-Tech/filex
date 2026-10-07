// The language packs' nightly translation (task #177), as pure decisions:
// what the build host does once its nightly test run is over.
// scripts/langpacks-nightly.mjs runs them, and
// web/tests/i18n/langPacksNightly.test.ts holds them. No process, no network
// and no clock of their own - the caller reads and passes them - apart from
// reading the prompt's template file.
//
// The night, in order (docs/CONTRIBUTING.md -> Translations and language packs):
//   1. wait while the nightly run is going (its tonight.json, a file on the
//      same host, LANGPACKS_NIGHT_FILE), at most LANGPACKS_WAIT_MAX_MIN; a
//      night that did not start, or whose process is gone, is not waited for
//      (nightVerdict, waitStep);
//   2. a worktree of the nightly run's checkout at LANGPACKS_REF, after
//      fetching it (pickRef, fetchSpec);
//   3. `langpacks.mjs status --check`: nothing pending, nothing is done;
//   4. per pack that lacks something (pendingPacks): `todo` into a directory
//      of its own, the glossary and the pack's translation copied beside the
//      worklist, an agent session confined to that directory (claudeArgs,
//      agentPrompt), only the answers taken from what it wrote (takeAnswers),
//      the packs and the tree measured unchanged around it, then
//      `apply --commit`; refused answers or a red validator go back to the
//      agent with what was said, LANGPACKS_FIX_ROUNDS times (applyOutcome);
//   5. `status` again, and one notification: per language, what was
//      translated and what is left (languageRows, composeReport).
// Nothing is pushed or tagged: the commits stay in the build host's pack
// checkouts, and release day fetches them from there (`langpacks.mjs pull`).
//
// The agent's Claude credential is the project's account, asked from the
// work server's MCP `account.credential` with a project token read from a
// file at each run (accountEnv) - never a copy of the credential on the
// host - or, for a run by hand, a token file.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { FIXED_NAMES, TRANSLATOR_RULES } from './langpacks.mjs';

// The directory of this module, not a URL resolved against import.meta.url:
// Vite rewrites that form into an asset URL (not file:) when a web test
// imports this module, and fileURLToPath throws.
/** The agent's prompt, a template: {{NAME}} placeholders, filled by agentPrompt. */
export const PROMPT_FILE = path.join(path.dirname(fileURLToPath(import.meta.url)), 'langpacks-prompt.md');

/** The commit trailer of the packs' commits when LANGPACKS_TRAILER does not say. */
export const DEFAULT_TRAILER = 'Co-Authored-By: Claude <noreply@anthropic.com>';

/**
 * The only tools the agent has: reading and editing files. Nothing that runs
 * a command, nothing that reaches the network.
 */
export const AGENT_TOOLS = ['Read', 'Edit', 'Write', 'Glob', 'Grep'];

const short = (sha) => String(sha ?? '').slice(0, 8) || '?';

// ── settings ────────────────────────────────────────────────────────────────

/** Where everything of the run lives when LANGPACKS_ROOT does not say. */
export const DEFAULT_ROOT = '/var/lib/filex-langpacks';
/** The nightly test run's own directory (scripts/chain/nightly.env.example CHAIN_ROOT). */
export const NIGHTLY_ROOT = '/var/lib/filex-nightly';

/**
 * The run's settings, from a KEY=VALUE file under the process environment
 * (scripts/lib/settings.mjs; every key is in scripts/langpacks-nightly.env.example).
 *
 * LANGPACKS_ROOT holds it all, outside every repository: the pack checkouts
 * (filex-lang-*, found there as the siblings of the tree), the tree (a
 * worktree of LANGPACKS_FILEX, made and removed per run), the nights, the
 * agent's own home. LANGPACKS_FILEX is the nightly run's checkout, whose
 * objects - and fetch credential - the tree is made from.
 */
export function translateSettings(env, { tz = '' } = {}) {
  const num = (key, def) => {
    const v = env[key];
    if (v === undefined || v === '') return def;
    const n = Number(v);
    if (!Number.isFinite(n) || n < 0) throw new Error(`${key}=${v} is not a number`);
    return n;
  };
  const oneOf = (key, def, allowed) => {
    const v = env[key] || def;
    if (!allowed.includes(v)) throw new Error(`${key}=${v}: one of ${allowed.join(', ')}`);
    return v;
  };
  const root = path.resolve(env.LANGPACKS_ROOT || DEFAULT_ROOT);
  const filex = path.resolve(env.LANGPACKS_FILEX || path.join(NIGHTLY_ROOT, 'src'));
  // The tree is a worktree, but never inside the checkout: the nightly run
  // cleans its checkout with `git clean -ffdx`, which removes a worktree
  // living there.
  const worktree = path.resolve(env.LANGPACKS_WORKTREE || path.join(root, 'tree'));
  if (worktree === filex || worktree.startsWith(`${filex}${path.sep}`)) {
    throw new Error(`LANGPACKS_WORKTREE=${worktree} is inside LANGPACKS_FILEX: the nightly run cleans that checkout and would remove it`);
  }
  return {
    root,
    filex,
    ref: env.LANGPACKS_REF || 'origin/main',
    // `night` falls back to this when the night named no commit, or one the
    // checkout has not got.
    fallbackRef: env.LANGPACKS_FALLBACK_REF || 'origin/main',
    fetch: env.LANGPACKS_FETCH !== '0',
    worktree,
    keepTree: env.LANGPACKS_KEEP_TREE === '1',
    packs: String(env.LANGPACKS_PACKS ?? '')
      .split(path.delimiter)
      .map((s) => s.trim())
      .filter(Boolean)
      .map((p) => path.resolve(p)),
    catalogue: env.LANGPACKS_CATALOGUE || '',
    state: root,
    keepNights: num('LANGPACKS_KEEP_NIGHTS', 14),
    tz: env.LANGPACKS_TZ || tz || 'UTC',
    nightFile: 'LANGPACKS_NIGHT_FILE' in env ? String(env.LANGPACKS_NIGHT_FILE ?? '') : path.join(NIGHTLY_ROOT, 'nightly', 'tonight.json'),
    waitMaxMin: num('LANGPACKS_WAIT_MAX_MIN', 150),
    waitPollMin: Math.max(1, num('LANGPACKS_WAIT_POLL_MIN', 5)),
    nightMaxAgeH: num('LANGPACKS_NIGHT_MAX_AGE_H', 20),
    budgetMin: num('LANGPACKS_BUDGET_MIN', 330),
    agent: {
      cmd: env.LANGPACKS_AGENT_CMD || '',
      bin: env.LANGPACKS_AGENT_BIN || 'claude',
      // ⚠ Always a model by name: a session started without one takes the
      // CLI's default, and a default the account has no plan for answers
      // "credits required" and translates nothing.
      model: env.LANGPACKS_AGENT_MODEL || 'opus',
      args: String(env.LANGPACKS_AGENT_ARGS ?? '')
        .split(/\s+/)
        .filter(Boolean),
      timeoutMin: num('LANGPACKS_AGENT_TIMEOUT_MIN', 45),
      fixRounds: num('LANGPACKS_FIX_ROUNDS', 1),
      // The agent's HOME: none of the host user's settings, hooks, MCP
      // servers or CLAUDE.md reach it.
      home: path.resolve(env.LANGPACKS_AGENT_HOME || path.join(root, 'agent-home')),
      // Its credential: the project's account through the work MCP
      // (url + a file holding a project token), else a token file.
      workUrl: env.LANGPACKS_WORK_URL || '',
      workTokenFile: env.LANGPACKS_WORK_TOKEN_FILE || '',
      project: env.LANGPACKS_WORK_PROJECT || '',
      tokenFile: env.LANGPACKS_CLAUDE_TOKEN_FILE || '',
    },
    trailer: 'LANGPACKS_TRAILER' in env ? String(env.LANGPACKS_TRAILER ?? '') : DEFAULT_TRAILER,
    notify: oneOf('LANGPACKS_NOTIFY', 'changes', ['changes', 'always', 'off']),
  };
}

// ── after the nightly run ───────────────────────────────────────────────────

/**
 * What the nightly run's tonight.json (scripts/chain/nightly.mjs) says:
 *
 *   over     phase `done`: the night ran, or decided not to;
 *   going    any other phase, in a record from the last `maxAgeH` hours,
 *            while its process is alive (`alive`: undefined when not known);
 *   gone     such a phase, but its process is not alive: it died;
 *   none     no record from the last `maxAgeH` hours: no night started;
 *   unknown  the file could not be read (`error`), or holds no record.
 *
 * Returns { state, why, night, sha, phase, decision }.
 */
export function nightVerdict(text, { nowMs, maxAgeH = 20, error = '', alive = undefined } = {}) {
  if (error) return { state: 'unknown', why: `the night's record could not be read: ${error}` };
  let t = null;
  try {
    t = JSON.parse(String(text ?? ''));
  } catch {
    t = null;
  }
  if (!t || typeof t !== 'object' || Array.isArray(t)) return { state: 'unknown', why: "the night's file holds no night record" };
  const facts = {
    night: String(t.night ?? ''),
    sha: typeof t.sha === 'string' ? t.sha : '',
    phase: String(t.phase ?? ''),
    decision: String(t.decision ?? ''),
  };
  const at = Date.parse(t.at);
  if (!Number.isFinite(at) || nowMs - at > maxAgeH * 3_600_000) {
    return { state: 'none', why: `no night recorded in the last ${maxAgeH} h`, ...facts };
  }
  if (t.phase === 'done') {
    const how = facts.decision === 'ran' ? (t.ok === true ? 'green' : 'red') : facts.decision || 'done';
    return { state: 'over', why: `the night ${facts.night} is over (${how}, ${short(facts.sha)})`, ...facts };
  }
  if (alive === false) return { state: 'gone', why: `the night ${facts.night} stopped at "${facts.phase || '?'}": its process (pid ${t.pid ?? '?'}) is gone`, ...facts };
  return { state: 'going', why: `the night ${facts.night} is at "${facts.phase || '?'}"`, ...facts };
}

/**
 * Go now, or wait and ask again. Only a night that is going is waited for,
 * and only `maxWaitMin`: then the run goes anyway and says so (`late`).
 */
export function waitStep({ verdict, waitedMin, maxWaitMin }) {
  if (verdict.state !== 'going') return { step: 'go', late: false, why: verdict.why };
  if (waitedMin >= maxWaitMin) return { step: 'go', late: true, why: `${verdict.why} after ${maxWaitMin} min of waiting: translating anyway` };
  return { step: 'wait', late: false, why: verdict.why };
}

/**
 * The ref the tree is checked out at. `night` is the commit the nightly run
 * ran on, and `fallback` when the night named none; `none` is the checkout's
 * working tree as it stands (a run by hand); anything else is a ref of the
 * checkout - origin/main by default, fetched first (fetchSpec).
 */
export function pickRef({ ref, verdict = null, fallback = 'origin/main' }) {
  if (ref !== 'night') return { ref, why: ref === 'none' ? 'the checkout as it stands' : ref };
  if (verdict?.sha) return { ref: verdict.sha, why: verdict.night ? `the commit the night ${verdict.night} ran on` : 'the commit the night ran on' };
  return { ref: fallback, why: `${fallback}: the night named no commit` };
}

/**
 * What to fetch before a ref is read: a remote-tracking ref `<remote>/<branch>`
 * is fetched alone, the way the nightly run fetches it
 * (`+refs/heads/<branch>:refs/remotes/<remote>/<branch>`) - not every branch
 * of the remote. Anything else (a commit, a local branch, `none`) fetches
 * nothing: null.
 */
export function fetchSpec(ref, remotes = ['origin']) {
  const m = /^([A-Za-z0-9._-]+)\/(.+)$/.exec(String(ref ?? ''));
  if (!m || !remotes.includes(m[1])) return null;
  return { remote: m[1], refspec: `+refs/heads/${m[2]}:refs/remotes/${m[1]}/${m[2]}` };
}

/**
 * The agent's credential from the work MCP `account.credential` answer (the
 * tool's text: { project, account: { label, type, credential } | null,
 * reason }): the environment Claude Code reads it from - a subscription's
 * token as CLAUDE_CODE_OAUTH_TOKEN, an API key as ANTHROPIC_API_KEY - and the
 * account's label. { env: null, why } when there is none. The credential is
 * returned, never written into `why`.
 */
export function accountEnv(text) {
  let doc = null;
  try {
    doc = JSON.parse(String(text ?? ''));
  } catch {
    return { env: null, why: 'the work server answered no account record' };
  }
  const acc = doc?.account;
  if (!acc) return { env: null, why: `no Claude account for the project${doc?.reason ? `: ${doc.reason}` : ''}` };
  const cred = typeof acc.credential === 'string' ? acc.credential.trim() : '';
  if (!cred) return { env: null, why: `the account ${acc.label ?? '?'} has no credential` };
  if (acc.type === 'api_key') return { env: { ANTHROPIC_API_KEY: cred }, label: String(acc.label ?? ''), type: 'api_key' };
  if (acc.type === 'subscription') return { env: { CLAUDE_CODE_OAUTH_TOKEN: cred }, label: String(acc.label ?? ''), type: 'subscription' };
  return { env: null, why: `the account ${acc.label ?? '?'} is of a type this run does not know: ${acc.type}` };
}

// ── the packs ───────────────────────────────────────────────────────────────

/** The packs of a `langpacks.mjs status --json` report that lack something, with their languages. */
export function pendingPacks(report) {
  return (report?.packs ?? [])
    .filter((p) => p.pending > 0)
    .map((p) => ({ dir: p.dir, name: p.name, langs: Object.keys(p.languages ?? {}), pending: p.pending }));
}

/**
 * The worklist the agent's answers are taken into: the one `todo` wrote,
 * with only `text` and `forms` of its items taken from the agent's copy, by
 * key. Every other field - the English, the pack block that names the pack
 * `apply` writes, the catalogue's digest - stays as `todo` wrote it, so an
 * answer is the only thing an agent can hand on.
 *
 * Returns { worklist, answered, left, ignored } (ignored: keys the agent's
 * copy has that the worklist does not).
 */
export function takeAnswers(original, edited) {
  const worklist = structuredClone(original);
  let answered = 0;
  let left = 0;
  const ignored = [];
  for (const [tag, lang] of Object.entries(worklist.languages ?? {})) {
    const theirs = new Map();
    const items = edited?.languages?.[tag]?.items;
    for (const it of Array.isArray(items) ? items : []) if (it && typeof it.key === 'string') theirs.set(it.key, it);
    const own = new Set((lang.items ?? []).map((it) => it.key));
    for (const k of theirs.keys()) if (!own.has(k)) ignored.push(`${tag} ${k}`);
    for (const it of lang.items ?? []) {
      const t = theirs.get(it.key);
      it.text = typeof t?.text === 'string' ? t.text : '';
      if (Array.isArray(it.forms_needed) || it.forms !== undefined) {
        const given = t?.forms && typeof t.forms === 'object' && !Array.isArray(t.forms) ? t.forms : {};
        it.forms = Object.fromEntries(Object.entries(given).filter(([, v]) => typeof v === 'string'));
      }
      if (it.text.trim()) answered += 1;
      else left += 1;
    }
  }
  return { worklist, answered, left, ignored };
}

/**
 * What `langpacks.mjs apply` said for one pack's worklist directory:
 *
 *   committed  exit 0 (and the commit it made);
 *   refused    an answer failed a check - nothing written; the agent may fix it;
 *   red        the pack's own validators failed and it was put back; the agent may fix it;
 *   failed     anything else that stopped it (uncommitted changes in the
 *              pack, a catalogue that moved): not the agent's to fix;
 *   error      apply could not run at all.
 */
export function applyOutcome(code, output) {
  const text = String(output ?? '').trim();
  if (code === 0) return { state: 'committed', commit: /committed ([0-9a-f]{7,40})/.exec(text)?.[1] ?? '', retry: false, feedback: '' };
  if (code !== 1) return { state: 'error', retry: false, feedback: text };
  if (/answer\(s\) to fix, nothing written/.test(text)) return { state: 'refused', retry: true, feedback: text };
  if (/put back as it was/.test(text)) return { state: 'red', retry: true, feedback: text };
  return { state: 'failed', retry: false, feedback: text };
}

/** What Claude Code's `--output-format json` result says, in a few fields; {} when it is not one. */
export function agentResult(text) {
  let doc = null;
  try {
    doc = JSON.parse(String(text ?? '').trim());
  } catch {
    return {};
  }
  if (!doc || typeof doc !== 'object') return {};
  const out = {};
  if (typeof doc.is_error === 'boolean') out.isError = doc.is_error;
  if (typeof doc.subtype === 'string') out.subtype = doc.subtype;
  if (Number.isFinite(doc.num_turns)) out.turns = doc.num_turns;
  if (Number.isFinite(doc.total_cost_usd)) out.costUsd = doc.total_cost_usd;
  if (typeof doc.result === 'string') out.result = doc.result.slice(0, 300);
  return out;
}

// ── the agent ───────────────────────────────────────────────────────────────

/**
 * Claude Code's command line for one pack. The prompt goes on stdin; the
 * working directory is the pack's worklist directory.
 *
 * ⚠ This is the boundary, and each flag is part of it:
 *   --restricted           no tool that runs a command or code, no web fetch,
 *                          the host user's settings files (their hooks, their
 *                          permissions) ignored, and the file tools confined
 *                          to the working directory;
 *   --strict-mcp-config    none of the host user's MCP servers (no --mcp-config
 *                          is given: no server at all);
 *   --tools                reading and editing files, nothing else;
 *   --permission-mode acceptEdits with --permission-prompts none
 *                          edits in the working directory go through, and
 *                          anything that would ask is refused - there is
 *                          nobody to ask at night.
 * No --add-dir: a pack directory given as a working directory would be
 * writable. The glossary and the translation the agent reads are copies. And
 * the session runs with a HOME of its own (LANGPACKS_AGENT_HOME), so nothing
 * of the host user's ~/.claude - settings, CLAUDE.md, credentials - is read.
 */
export function claudeArgs({ model = 'opus', extra = [] } = {}) {
  return [
    '-p',
    '--model',
    model,
    '--output-format',
    'json',
    '--no-session-persistence',
    '--restricted',
    '--strict-mcp-config',
    '--tools',
    AGENT_TOOLS.join(','),
    '--permission-mode',
    'acceptEdits',
    '--permission-prompts',
    'none',
    '--disable-slash-commands',
    ...extra,
  ];
}

/** The prompt's template, as the repository has it. */
export function readPromptTemplate(file = PROMPT_FILE) {
  return fs.readFileSync(file, 'utf8').replace(/\r\n/g, '\n');
}

/**
 * The prompt of one agent session: the template with the pack, its
 * worklist, the files beside it and the rules (lib/langpacks.mjs
 * TRANSLATOR_RULES - the rules every worklist carries - and FIXED_NAMES, the
 * names the checker holds an answer to). From the second round on it carries
 * what `apply` said about the answers.
 */
export function agentPrompt(template, { pack, tag, worklist, items, glossary = '', references = [], round = 0, feedback = '' }) {
  const values = {
    PACK: pack,
    TAG: tag,
    WORKLIST: worklist,
    ITEMS: String(items),
    GLOSSARY: glossary
      ? `\`${glossary}\` - the pack's glossary: its terms, its voice and its typography decide.`
      : "The pack has no glossary: its translation (below) shows its terms, its voice and its typography.",
    REFERENCE: references.length
      ? `${references.map((r) => `\`${r}\``).join(', ')} - the pack's translation as it stands: how it already words things. Search it for an English term before you choose a word.`
      : 'The pack has no translation yet to read.',
    RULES: TRANSLATOR_RULES.map((r) => `- ${r}`).join('\n'),
    NAMES: FIXED_NAMES.join(', '),
    FEEDBACK: round > 0 ? feedbackBlock(round, feedback) : '',
  };
  const out = String(template).replace(/\{\{([A-Z_]+)\}\}/g, (m, k) => {
    if (!(k in values)) throw new Error(`the prompt template names {{${k}}}, which nothing fills`);
    return values[k];
  });
  return `${out.replace(/\n{3,}/g, '\n\n').trimEnd()}\n`;
}

function feedbackBlock(round, feedback) {
  const said = String(feedback ?? '')
    .trim()
    .split('\n')
    .slice(-80)
    .map((l) => `    ${l}`)
    .join('\n');
  return [
    `This is round ${round + 1}. Your answers in the worklist were checked and refused, and nothing was written to the pack.`,
    'Fix exactly what the check says, in the same worklist, and leave the answers it did not name as they are:',
    '',
    said || '    (the check said nothing more)',
  ].join('\n');
}

// ── the record and the report ───────────────────────────────────────────────

/**
 * One row per language of every pack the night looked at: what it lacked
 * before (missing + changed), what it lacks after, what was translated.
 * Without an `after` report nothing counts as translated.
 */
export function languageRows(before, after) {
  const rows = [];
  for (const p of before?.packs ?? []) {
    const a = (after?.packs ?? []).find((x) => x.dir === p.dir);
    for (const [tag, l] of Object.entries(p.languages ?? {})) {
      const was = (l.missing ?? 0) + (l.changed ?? 0);
      const al = a?.languages?.[tag];
      const left = al ? (al.missing ?? 0) + (al.changed ?? 0) : was;
      rows.push({ pack: p.name, dir: p.dir, tag, was, left, translated: Math.max(0, was - left) });
    }
  }
  return rows;
}

const RED_STATES = new Set(['refused', 'red', 'failed', 'error', 'guard', 'agent']);

function outcomeText(o) {
  const rounds = o.rounds > 1 ? ` after ${o.rounds} rounds` : '';
  switch (o.state) {
    case 'committed':
      return `committed ${short(o.commit)}${rounds}, not pushed`;
    case 'refused':
      return `RED: answers refused${rounds}, nothing written`;
    case 'red':
      return `RED: the pack's validators failed${rounds}, put back as it was`;
    case 'failed':
      return 'RED: apply stopped, nothing written';
    case 'error':
      return 'RED: could not run';
    case 'guard':
      return 'RED: something outside the agent\'s directory changed while it ran, nothing applied';
    case 'agent':
      return 'RED: the agent did not start';
    case 'time':
      return 'not started: no time left tonight';
    case 'skipped':
      return 'not started: the night stopped';
    default:
      return o.state || '?';
  }
}

const lastLines = (text, n) =>
  String(text ?? '')
    .trim()
    .split('\n')
    .filter((l) => l.trim())
    .slice(-n);

/**
 * The night's one notification.
 *
 *   day      the night's date
 *   tree     { ref, sha, subject } it translated against
 *   wait     the nightly run, as the wait left it ({ why, late })
 *   rows     languageRows
 *   packs    each pack's outcome { dir, state, commit, rounds, feedback, agent }
 *   error    why the night could not run, when it could not
 *   took     how long it took; where: the night's directory
 *
 * Returns { result, title, message }: result `ok` (everything pending was
 * translated and committed), `red` (a pack red, or the night could not run),
 * `waiting` (something left, nothing red: no time) or `info` (nothing was
 * pending).
 */
export function composeReport({ day, tree = null, wait = null, rows = [], packs = [], error = '', took = '', where = '' }) {
  const lines = [];
  const translated = rows.reduce((n, r) => n + r.translated, 0);
  const left = rows.reduce((n, r) => n + r.left, 0);
  const red = !!error || packs.some((p) => RED_STATES.has(p.state));
  const pending = rows.some((r) => r.was > 0) || packs.length > 0;
  if (tree) lines.push(`tree: ${tree.ref} at ${short(tree.sha)}${tree.subject ? ` ${tree.subject}` : ''}`);
  if (wait?.why) lines.push(`the nightly run: ${wait.why}`);
  if (error) lines.push(`could not run: ${error}`);
  for (const r of rows) {
    const o = packs.find((p) => p.dir === r.dir);
    if (!o && r.was === 0) {
      lines.push(`${r.pack} ${r.tag}: up to date`);
      continue;
    }
    lines.push(`${r.pack} ${r.tag}: ${r.translated} translated, ${r.left} left${o ? ` - ${outcomeText(o)}` : ''}`);
    if (o && o.state !== 'committed') for (const l of lastLines(o.feedback, 8)) lines.push(`    ${l}`);
    const trouble = (o?.agent ?? []).filter((a) => a.timedOut || a.code !== 0);
    if (trouble.length) lines.push(`    agent: ${trouble.map((a) => `round ${a.round + 1} ${a.timedOut ? 'stopped at its time limit' : `exit ${a.code}`}`).join(', ')}`);
  }
  if (pending && !error) lines.push('Nothing is pushed or tagged: the commits wait in these pack checkouts for release day (node scripts/langpacks.mjs pull, then release X.Y.Z).');
  if (where) lines.push(`worklists and logs: ${where}`);
  if (took) lines.push(`took ${took}`);

  let result = 'info';
  if (red) result = 'red';
  else if (left > 0) result = 'waiting';
  else if (translated > 0) result = 'ok';
  const at = tree?.sha ? ` (${short(tree.sha)})` : '';
  let title;
  if (error) title = `filex language packs ${day}: could not run`;
  else if (!pending) title = `filex language packs ${day}: up to date${at}`;
  else title = `filex language packs ${day}: ${translated} translated, ${left} left${red ? ', RED' : ''}${at}`;
  return { result, title, message: lines.join('\n').slice(0, 7900) };
}

/** Whether the night is told: `off` never, `always` always, `changes` unless nothing was pending. */
export function notifyWanted(mode, result) {
  if (mode === 'off') return false;
  if (mode === 'always') return true;
  return result !== 'info';
}

/** The exit code of a night: 0 green or nothing to do, 1 something left or red, 2 could not run. */
export function exitCode({ result, error = '' }) {
  if (error) return 2;
  return result === 'ok' || result === 'info' ? 0 : 1;
}

// ── the nights kept ─────────────────────────────────────────────────────────

/** A night's directory name under <state>/nights: sortable by time, with the commit. */
export function nightDirName(nowMs, sha) {
  const stamp = new Date(nowMs).toISOString().replace(/[-:]/g, '').replace(/\..*/, '').replace('T', '-');
  return `${stamp}Z-${String(sha || 'tree').slice(0, 8)}`;
}

/** The night directories to remove: every one of ours but the newest `keep`. */
export function nightsToPrune(names, keep) {
  return names
    .filter((n) => /^\d{8}-\d{6}Z-[0-9a-z]+$/.test(n))
    .sort()
    .reverse()
    .slice(keep);
}
