// The language packs' nightly translation (#177): scripts/langpacks-nightly.mjs
// and scripts/lib/langpacks-nightly.mjs, with the prompt template
// scripts/lib/langpacks-prompt.md.
//
// ⚠ Why this exists: `langpacks.mjs` (status, todo, apply) made the packs
// able to follow `main`, but nothing ran it - CONTRIBUTING said "every night
// the test run calls todo", and no night did. The maintainers decided
// (2026-10-06) that an agent translates every night on the build host,
// after its nightly test run (a workstation is not on every night). That
// puts an agent next to the pack repositories with nobody watching, so this
// suite holds:
//   - the decisions: wait while the nightly run is going (and only then,
//     and not for ever); nothing pending -> no agent, no commit;
//     refused answers or a red validator -> the pack put back, the agent told
//     once more, and the night red when it still fails;
//   - the boundary: the agent's command line (no command or web tool, no MCP
//     server, file tools confined to its directory) and its own HOME; only
//     its answers cross into what `apply` reads; a write outside its
//     directory stops the night;
//   - the credential: the project's account from the work server, asked at
//     every run, never written into a message;
//   - the installer: every module the installed copy imports is installed.
//   - the prompt: the fixed names, the plural forms, the plain hyphen, the
//     language's own letters, one term per concept and the glossary.
//
// The agent here is a stand-in (LANGPACKS_AGENT_CMD): a node script that
// answers the worklist the way a plan says. No Claude Code session starts
// and nothing reaches the network; the packs are made on the spot
// (tests/helpers/langPacks.ts).

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { agentCredential, prepareTree, removeTree } from '../../../scripts/langpacks-nightly.mjs';
import {
  AGENT_TOOLS,
  DEFAULT_ROOT,
  DEFAULT_TRAILER,
  NIGHTLY_ROOT,
  accountEnv,
  agentPrompt,
  agentResult,
  applyOutcome,
  claudeArgs,
  composeReport,
  exitCode,
  fetchSpec,
  languageRows,
  nightDirName,
  nightVerdict,
  nightsToPrune,
  notifyWanted,
  pendingPacks,
  pickRef,
  readPromptTemplate,
  takeAnswers,
  translateSettings,
  waitStep,
} from '../../../scripts/lib/langpacks-nightly.mjs';
import { FIXED_NAMES, TRANSLATOR_RULES, checkoutRoots, jsonText, mainCheckout, packDiff, worklistItems } from '../../../scripts/lib/langpacks.mjs';
import { toolResult, wakeVerdict } from '../../../scripts/lib/notify.mjs';
import { type Catalogue, type Rows, filesOf, git, makePack, readJson, write, writeCatalogue } from '../helpers/langPacks';

const ROOT = path.resolve(__dirname, '../../..');
const DRIVER = path.join(ROOT, 'scripts', 'langpacks-nightly.mjs');
const SLOW = { timeout: 120_000 };
const EM = String.fromCharCode(0x2014);
const EN = String.fromCharCode(0x2013);

const temps: string[] = [];
function tmp(prefix: string): string {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  temps.push(d);
  return d;
}
afterAll(() => {
  // Retries: on Windows a directory can stay locked for a moment after the
  // last process in it ended (EBUSY on rmdir).
  for (const d of temps) fs.rmSync(d, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
});

// The pack was translated for PREV; this tree's catalogue is NEXT:
// `b.office` reworded, `d.new` added - two strings to translate.
const PREV: Catalogue = { 'a.title': 'Files', 'b.office': 'Open in ONLYOFFICE' };
const NEXT: Catalogue = { 'a.title': 'Files', 'b.office': 'Edit in ONLYOFFICE', 'd.new': 'New thing' };
const ROWS: Rows = {
  'a.title': { in: 'explorer', syntax: 'plain', tr: 'Dosyalar' },
  'b.office': { in: 'admin', syntax: 'vue-i18n', tr: "ONLYOFFICE'ta düzenle" },
  'd.new': { in: 'explorer', syntax: 'plain', tr: 'Yeni şey' },
};
const DE = { 'a.title': 'Dateien', 'b.office': 'In ONLYOFFICE öffnen' };
const GOOD = { 'b.office': 'In ONLYOFFICE bearbeiten', 'd.new': 'Neues Ding' };

/* The stand-in agent: reads the prompt on stdin, answers the worklist named
   by LANGPACKS_WORKLIST with the plan's round (FAKE_PLAN), and logs what it
   saw. A plan can also make it misbehave: tamper (rewrite everything but
   the answers, write a second worklist naming another pack, edit the
   catalogue copy), escape (write a file outside its directory), broken (write
   no JSON in the first round).
   ⚠ Windows: the driver starts LANGPACKS_AGENT_CMD through cmd.exe in a
   detached process, which has no console; the node it starts gets a console
   of its own, and its stdin is that console, not the driver's pipe. A read
   of fd 0 there waits for a key press for ever: the night hangs until the
   test's timeout, the stand-in outlives it holding its directory (EBUSY on
   cleanup). When stdin is a console the stand-in reads the copy of the
   prompt the driver writes beside the night's logs (logs/<pack>-prompt-<n>.md,
   two levels above its directory). */
const FAKE_AGENT = `import fs from 'node:fs';
import path from 'node:path';
import tty from 'node:tty';
const round = Number(process.env.LANGPACKS_ROUND || 0);
const promptCopy = path.join(process.cwd(), '..', '..', 'logs', path.basename(process.cwd()) + '-prompt-' + (round + 1) + '.md');
const prompt = tty.isatty(0) ? fs.readFileSync(promptCopy, 'utf8') : fs.readFileSync(0, 'utf8');
const file = process.env.LANGPACKS_WORKLIST;
const plan = JSON.parse(process.env.FAKE_PLAN || '{}');
if (process.env.FAKE_LOG) {
  fs.appendFileSync(process.env.FAKE_LOG, JSON.stringify({ round, cwd: process.cwd(), home: process.env.HOME, file, prompt, files: fs.readdirSync('.').sort() }) + '\\n');
}
const rounds = plan.rounds || [];
const answers = rounds[Math.min(round, rounds.length - 1)] || {};
const wl = JSON.parse(fs.readFileSync(file, 'utf8'));
for (const lang of Object.values(wl.languages)) for (const it of lang.items) if (typeof answers[it.key] === 'string') it.text = answers[it.key];
if (plan.tamper) {
  wl.pack.dir = plan.tamper;
  for (const lang of Object.values(wl.languages)) {
    for (const it of lang.items) it.en = 'tampered';
    lang.items.push({ key: 'z.extra', text: 'Extra' });
  }
  fs.writeFileSync('second.json', JSON.stringify({ ...wl, pack: { ...wl.pack, dir: plan.tamper } }, null, 2));
  fs.writeFileSync(path.join('catalogue', 'filex-catalogue-en.json'), JSON.stringify({ 'b.office': 'tampered' }));
}
fs.writeFileSync(file, plan.broken && round === 0 ? '{ not json' : JSON.stringify(wl, null, 2) + '\\n');
if (plan.escape) fs.writeFileSync(plan.escape, 'written outside its directory\\n');
process.exit(plan.exit ?? 0);
`;

const tools = tmp('filex-langpacks-night-tools-');
const FAKE = path.join(tools, 'fake-agent.mjs');
write(FAKE, FAKE_AGENT);

/** A pack that lacks two strings against NEXT, and the catalogue NEXT as a directory. */
function setup(prefix: string, { translation = DE, catalogue = PREV, glossary = '' }: { translation?: Record<string, string>; catalogue?: Catalogue; glossary?: string } = {}) {
  const base = tmp(prefix);
  const pack = makePack(base, { catalogue, rows: ROWS, translation, glossary, remote: false });
  const target = path.join(base, 'catalogue-now');
  writeCatalogue(target, NEXT, ROWS, '0.52.0+abcdef12');
  // LANGPACKS_ROOT: the nights, the records and the agent's HOME.
  const state = path.join(base, 'state');
  const envFile = path.join(base, 'empty.env');
  write(envFile, '# nothing here: the test sets every setting in the environment\n');
  return { base, pack, target, state, envFile };
}

type Night = ReturnType<typeof setup>;

function night(s: Night, { plan = {}, extra = {}, args = [] }: { plan?: object; extra?: Record<string, string>; args?: string[] } = {}) {
  const env: Record<string, string> = {
    ...(process.env as Record<string, string>),
    LANGPACKS_FILEX: ROOT,
    LANGPACKS_REF: 'none',
    LANGPACKS_PACKS: s.pack,
    LANGPACKS_CATALOGUE: s.target,
    LANGPACKS_ROOT: s.state,
    LANGPACKS_AGENT_CMD: `node "${FAKE}"`,
    LANGPACKS_NIGHT_FILE: '',
    LANGPACKS_NOTIFY: 'off',
    LANGPACKS_TZ: 'UTC',
    LANGPACKS_TRAILER: 'Co-Authored-By: Test Agent <agent@example.com>',
    FILEX_NOTIFY_URL: '',
    FILEX_WAKE_URL: '',
    FAKE_PLAN: JSON.stringify(plan),
    FAKE_LOG: path.join(s.base, 'fake-agent.jsonl'),
    ...extra,
  };
  const r = spawnSync(process.execPath, [DRIVER, 'run', '--env', s.envFile, ...args], { cwd: ROOT, encoding: 'utf8', env, timeout: 110_000 });
  return { code: r.status, out: r.stdout ?? '', err: r.stderr ?? '' };
}

const agentCalls = (s: Night) => {
  const f = path.join(s.base, 'fake-agent.jsonl');
  return fs.existsSync(f)
    ? fs
        .readFileSync(f, 'utf8')
        .split('\n')
        .filter(Boolean)
        .map((l) => JSON.parse(l))
    : [];
};
const last = (s: Night) => readJson(path.join(s.state, 'last.json'));

/* -- the decisions ---------------------------------------------------------- */

describe('after the nightly run', () => {
  const now = Date.parse('2026-10-07T01:30:00Z');
  const rec = (o: object) => JSON.stringify({ night: '2026-10-07', at: '2026-10-06T22:00:05Z', pid: 7, sha: '1a2b3c4d5e6f', ...o });

  it('reads tonight.json: over when done, going in any other phase while its process lives, gone when it died, none when there is no fresh record, unknown when there is no file', () => {
    expect(nightVerdict(rec({ phase: 'done', decision: 'ran', ok: true }), { nowMs: now })).toMatchObject({ state: 'over', sha: '1a2b3c4d5e6f' });
    expect(nightVerdict(rec({ phase: 'done', decision: 'ran', ok: true }), { nowMs: now }).why).toContain('green');
    expect(nightVerdict(rec({ phase: 'running' }), { nowMs: now })).toMatchObject({ state: 'going', phase: 'running' });
    expect(nightVerdict(rec({ phase: 'running' }), { nowMs: now, alive: true }).state).toBe('going');
    expect(nightVerdict(rec({ phase: 'running' }), { nowMs: now, alive: false })).toMatchObject({ state: 'gone', phase: 'running' });
    expect(nightVerdict(rec({ phase: 'running' }), { nowMs: now, alive: false }).why).toContain('pid 7');
    // A record from two days ago is not tonight: no night started.
    expect(nightVerdict(rec({ phase: 'running', at: '2026-10-05T22:00:00Z' }), { nowMs: now, maxAgeH: 20 }).state).toBe('none');
    expect(nightVerdict('', { nowMs: now }).state).toBe('unknown');
    expect(nightVerdict('not a record', { nowMs: now }).state).toBe('unknown');
    expect(nightVerdict(rec({ phase: 'done' }), { nowMs: now, error: 'no /var/lib/filex-nightly/nightly/tonight.json' })).toMatchObject({ state: 'unknown' });
  });

  it('waits only for a night that is going, and not past LANGPACKS_WAIT_MAX_MIN', () => {
    const going = nightVerdict(rec({ phase: 'running' }), { nowMs: now });
    expect(waitStep({ verdict: going, waitedMin: 10, maxWaitMin: 150 }).step).toBe('wait');
    expect(waitStep({ verdict: going, waitedMin: 150, maxWaitMin: 150 })).toMatchObject({ step: 'go', late: true });
    for (const state of ['over', 'gone', 'none', 'unknown']) {
      expect(waitStep({ verdict: { state, why: state }, waitedMin: 0, maxWaitMin: 150 })).toMatchObject({ step: 'go', late: false });
    }
  });

  it('translates against origin/main, the night commit for `night` (the fallback when it named none), or the checkout as it stands for `none`', () => {
    expect(pickRef({ ref: 'origin/main' }).ref).toBe('origin/main');
    expect(pickRef({ ref: 'none' }).ref).toBe('none');
    expect(pickRef({ ref: 'night', verdict: { sha: 'abc123', night: '2026-10-07' } }).ref).toBe('abc123');
    expect(pickRef({ ref: 'night', verdict: { state: 'none' } }).ref).toBe('origin/main');
    expect(pickRef({ ref: 'night', verdict: null, fallback: 'origin/next' }).ref).toBe('origin/next');
  });

  it('fetches a remote-tracking ref alone, the way the nightly run does, and nothing for anything else', () => {
    expect(fetchSpec('origin/main')).toEqual({ remote: 'origin', refspec: '+refs/heads/main:refs/remotes/origin/main' });
    expect(fetchSpec('origin/release/0.53', ['origin'])).toEqual({ remote: 'origin', refspec: '+refs/heads/release/0.53:refs/remotes/origin/release/0.53' });
    expect(fetchSpec('main')).toBeNull();
    expect(fetchSpec('1a2b3c4d')).toBeNull();
    // A name with a slash that is no remote of the checkout is a local branch.
    expect(fetchSpec('feat/x', ['origin'])).toBeNull();
    expect(fetchSpec('none')).toBeNull();
  });
});

describe('the settings', () => {
  it('lives in one directory of its own on the build host, next to the nightly run, and always names a model', () => {
    const cfg = translateSettings({});
    expect(cfg.root).toBe(path.resolve(DEFAULT_ROOT));
    expect(cfg.state).toBe(cfg.root);
    expect(cfg.filex).toBe(path.resolve(NIGHTLY_ROOT, 'src'));
    // The tree beside the packs, outside the nightly run's checkout.
    expect(cfg.worktree).toBe(path.join(cfg.root, 'tree'));
    expect(cfg.nightFile).toBe(path.join(NIGHTLY_ROOT, 'nightly', 'tonight.json'));
    expect(cfg.ref).toBe('origin/main');
    expect(cfg.fetch).toBe(true);
    expect(cfg.agent.model).toBe('opus');
    expect(cfg.agent.fixRounds).toBe(1);
    expect(cfg.agent.home).toBe(path.join(cfg.root, 'agent-home'));
    expect(cfg.trailer).toBe(DEFAULT_TRAILER);
    expect(cfg.notify).toBe('changes');
    expect(translateSettings({ LANGPACKS_FETCH: '0', LANGPACKS_NIGHT_FILE: '' })).toMatchObject({ fetch: false, nightFile: '' });
  });

  it('refuses a worktree inside the nightly run checkout, which that run cleans every night', () => {
    expect(() => translateSettings({ LANGPACKS_FILEX: '/work/src', LANGPACKS_WORKTREE: '/work/src/.claude/worktrees/x' })).toThrow(/inside LANGPACKS_FILEX/);
    expect(() => translateSettings({ LANGPACKS_FILEX: '/work/src', LANGPACKS_WORKTREE: '/work/src' })).toThrow(/inside LANGPACKS_FILEX/);
    expect(translateSettings({ LANGPACKS_FILEX: '/work/src', LANGPACKS_WORKTREE: '/work/src-tree' }).worktree).toBe(path.resolve('/work/src-tree'));
  });

  it('reads the pack list, an empty trailer, and refuses a setting it cannot use', () => {
    const cfg = translateSettings({ LANGPACKS_PACKS: ['/work/a', '/work/b'].join(path.delimiter), LANGPACKS_TRAILER: '' });
    expect(cfg.packs).toEqual([path.resolve('/work/a'), path.resolve('/work/b')]);
    expect(cfg.trailer).toBe('');
    expect(() => translateSettings({ LANGPACKS_BUDGET_MIN: 'soon' })).toThrow(/LANGPACKS_BUDGET_MIN/);
    expect(() => translateSettings({ LANGPACKS_NOTIFY: 'sometimes' })).toThrow(/LANGPACKS_NOTIFY/);
  });
});

describe("the agent's credential", () => {
  const SECRET = 'sk-ant-oat01-not-a-real-token';
  const answer = (account: unknown, { sse = false, status = 200, isError = false } = {}) => {
    const doc = { jsonrpc: '2.0', id: 1, result: { isError, content: [{ type: 'text', text: JSON.stringify({ project: 'p1', account }) }] } };
    const body = sse ? `event: message\ndata: ${JSON.stringify(doc)}\n\n` : JSON.stringify(doc);
    const calls: { url: string; init: { headers: Record<string, string>; body: string } }[] = [];
    const fetch = async (url: string, init: { headers: Record<string, string>; body: string }) => {
      calls.push({ url, init });
      return { status, text: async () => body };
    };
    return { fetch, calls };
  };
  const tokenFile = () => {
    const f = path.join(tmp('filex-langpacks-cred-'), 'work-mcp.token');
    write(f, 'project-token\n');
    return f;
  };
  const cfgWith = (env: Record<string, string>) => translateSettings({ LANGPACKS_ROOT: tmp('filex-langpacks-cred-root-'), ...env });

  it('reads the work server answer into the environment Claude Code takes, by the account type', () => {
    expect(accountEnv(JSON.stringify({ account: { label: 'Pro', type: 'subscription', credential: SECRET } }))).toEqual({ env: { CLAUDE_CODE_OAUTH_TOKEN: SECRET }, label: 'Pro', type: 'subscription' });
    expect(accountEnv(JSON.stringify({ account: { label: 'API', type: 'api_key', credential: 'sk-ant-api03-x' } })).env).toEqual({ ANTHROPIC_API_KEY: 'sk-ant-api03-x' });
    expect(accountEnv(JSON.stringify({ account: null, reason: 'no active account' }))).toMatchObject({ env: null, why: expect.stringContaining('no active account') });
    expect(accountEnv(JSON.stringify({ account: { label: 'Pro', type: 'subscription', credential: ' ' } })).env).toBeNull();
    expect(accountEnv('not json').env).toBeNull();
  });

  it('asks the work server for the project account at every run, with the project token from its file', async () => {
    const cfg = cfgWith({ LANGPACKS_WORK_URL: 'https://work.example.com/mcp/work', LANGPACKS_WORK_TOKEN_FILE: tokenFile() });
    for (const sse of [false, true]) {
      const { fetch, calls } = answer({ label: 'Pro', type: 'subscription', credential: SECRET }, { sse });
      const c = await agentCredential(cfg, { fetch });
      expect(c.env).toEqual({ CLAUDE_CODE_OAUTH_TOKEN: SECRET });
      expect(c.label).toContain('Pro');
      expect(c.label).not.toContain(SECRET);
      expect(calls[0].url).toBe('https://work.example.com/mcp/work');
      expect(calls[0].init.headers['x-access-token']).toBe('project-token');
      expect(JSON.parse(calls[0].init.body).params).toEqual({ name: 'account.credential', arguments: {} });
    }
    // A token that is not confined to the project names it.
    const named = answer({ label: 'Pro', type: 'subscription', credential: SECRET });
    await agentCredential({ ...cfg, agent: { ...cfg.agent, project: 'p1' } }, { fetch: named.fetch });
    expect(JSON.parse(named.calls[0].init.body).params.arguments).toEqual({ project: 'p1' });
  });

  it('stops the night with a reason - never the credential - when there is no account, no token or no answer', async () => {
    const cfg = cfgWith({ LANGPACKS_WORK_URL: 'https://work.example.com/mcp/work', LANGPACKS_WORK_TOKEN_FILE: tokenFile() });
    await expect(agentCredential(cfg, { fetch: answer(null).fetch })).rejects.toThrow(/no Claude account/);
    await expect(agentCredential(cfg, { fetch: answer({ label: 'x' }, { status: 401 }).fetch })).rejects.toThrow(/HTTP 401/);
    await expect(agentCredential(cfg, { fetch: answer({ label: 'x' }, { isError: true }).fetch })).rejects.toThrow(/refused/);
    const down = async () => {
      throw new Error('connect ECONNREFUSED');
    };
    await expect(agentCredential(cfg, { fetch: down })).rejects.toThrow(/could not be asked/);
    await expect(agentCredential(cfgWith({ LANGPACKS_WORK_URL: 'https://work.example.com/mcp/work', LANGPACKS_WORK_TOKEN_FILE: '/nonexistent/token' }))).rejects.toThrow(/LANGPACKS_WORK_TOKEN_FILE/);
    await expect(agentCredential(cfgWith({}))).rejects.toThrow(/no Claude credential/);
    try {
      await agentCredential(cfg, { fetch: answer({ label: 'Pro', type: 'unknown', credential: SECRET }).fetch });
      expect.unreachable();
    } catch (e) {
      expect(String((e as Error).message)).not.toContain(SECRET);
    }
  });

  it('takes a token file for a run by hand, and asks nothing for another agent', async () => {
    const f = path.join(tmp('filex-langpacks-cred-'), 'claude.token');
    write(f, `${SECRET}\n`);
    expect((await agentCredential(cfgWith({ LANGPACKS_CLAUDE_TOKEN_FILE: f }))).env).toEqual({ CLAUDE_CODE_OAUTH_TOKEN: SECRET });
    expect((await agentCredential(cfgWith({ LANGPACKS_AGENT_CMD: 'node agent.mjs' }))).env).toEqual({});
  });

  it('reads an MCP answer through the one reader wake uses too', () => {
    const ok = JSON.stringify({ jsonrpc: '2.0', id: 1, result: { content: [{ type: 'text', text: 'hello' }] } });
    expect(toolResult(200, ok)).toEqual({ ok: true, text: 'hello' });
    expect(toolResult(200, `data: ${ok}\n\n`)).toEqual({ ok: true, text: 'hello' });
    expect(toolResult(500, ok)).toEqual({ ok: false, why: 'HTTP 500' });
    expect(wakeVerdict(200, ok)).toEqual({ ok: true });
  });
});

describe('only the answers cross', () => {
  const original = {
    worklist: 1,
    pack: { dir: '/work/filex-lang-de', name: 'lang-de', catalogue_sha256: 'aa' },
    languages: {
      ar: {
        items: [
          { key: 'a.count', text: '', forms: {}, kind: 'missing', en: '{n} files', forms_needed: ['zero', 'few'] },
          { key: 'd.new', text: '', kind: 'missing', en: 'New thing' },
        ],
      },
    },
  };

  it("takes text and forms by key, and nothing else of the agent's copy", () => {
    const edited = structuredClone(original) as typeof original & { extra?: boolean };
    edited.pack.dir = '/work/filex';
    edited.pack.catalogue_sha256 = 'bb';
    edited.languages.ar.items[0].text = 'كل {n}';
    edited.languages.ar.items[0].forms = { few: 'قليل {n}', zero: 7 } as unknown as Record<string, string>;
    edited.languages.ar.items[1].en = 'Rewritten English';
    (edited.languages.ar.items as unknown[]).push({ key: 'z.extra', text: 'x' });
    const t = takeAnswers(original, edited);
    expect(t.worklist.pack).toEqual(original.pack);
    expect(t.worklist.languages.ar.items[0]).toMatchObject({ text: 'كل {n}', forms: { few: 'قليل {n}' } });
    expect(t.worklist.languages.ar.items[1]).toMatchObject({ text: '', en: 'New thing' });
    expect(t.worklist.languages.ar.items.map((i: { key: string }) => i.key)).toEqual(['a.count', 'd.new']);
    expect(t).toMatchObject({ answered: 1, left: 1, ignored: ['ar z.extra'] });
    // The original is not touched.
    expect(original.languages.ar.items[0].text).toBe('');
  });

  it('leaves every answer empty when the copy is not a worklist any more', () => {
    expect(takeAnswers(original, { nonsense: true })).toMatchObject({ answered: 0, left: 2 });
    expect(takeAnswers(original, null)).toMatchObject({ answered: 0, left: 2 });
  });
});

describe('what apply said', () => {
  it('tells committed, refused, put back, stopped and could not run apart - and only the first two kinds of red go back to the agent', () => {
    expect(applyOutcome(0, 'lang-de: committed 1a2b3c4d (5 of 5), not pushed')).toEqual({ state: 'committed', commit: '1a2b3c4d', retry: false, feedback: '' });
    expect(applyOutcome(1, 'lang-de: 1 answer(s) to fix, nothing written:\n    de NAME b.office: x')).toMatchObject({ state: 'refused', retry: true });
    expect(applyOutcome(1, 'lang-de: put back as it was - `validate` failed')).toMatchObject({ state: 'red', retry: true });
    expect(applyOutcome(1, 'lang-de: /work/filex-lang-de has uncommitted changes - commit or stash them first')).toMatchObject({ state: 'failed', retry: false });
    expect(applyOutcome(2, 'langpacks: no catalogue/')).toMatchObject({ state: 'error', retry: false });
  });

  it("reads Claude Code's JSON result, and nothing from other output", () => {
    expect(agentResult(JSON.stringify({ type: 'result', subtype: 'success', is_error: false, num_turns: 12, total_cost_usd: 0.5, result: 'done' }))).toEqual({
      isError: false,
      subtype: 'success',
      turns: 12,
      costUsd: 0.5,
      result: 'done',
    });
    expect(agentResult('not json')).toEqual({});
  });
});

describe('the report', () => {
  const before = {
    packs: [
      { dir: '/w/de', name: 'lang-de', pending: 2, languages: { de: { missing: 1, changed: 1, removed: 0 } } },
      { dir: '/w/ar', name: 'lang-ar', pending: 2, languages: { ar: { missing: 2, changed: 0, removed: 0 } } },
      { dir: '/w/fr', name: 'lang-fr', pending: 0, languages: { fr: { missing: 0, changed: 0, removed: 1 } } },
    ],
  };
  const after = {
    packs: [
      { dir: '/w/de', name: 'lang-de', pending: 0, languages: { de: { missing: 0, changed: 0 } } },
      { dir: '/w/ar', name: 'lang-ar', pending: 2, languages: { ar: { missing: 2, changed: 0 } } },
      { dir: '/w/fr', name: 'lang-fr', pending: 0, languages: { fr: { missing: 0, changed: 0 } } },
    ],
  };
  const tree = { ref: 'main', sha: '1a2b3c4d5e6f7a8b', subject: 'feat: something' };

  it('counts per language what was translated and what is left; without an after report nothing counts as translated', () => {
    expect(languageRows(before, after)).toEqual([
      { pack: 'lang-de', dir: '/w/de', tag: 'de', was: 2, left: 0, translated: 2 },
      { pack: 'lang-ar', dir: '/w/ar', tag: 'ar', was: 2, left: 2, translated: 0 },
      { pack: 'lang-fr', dir: '/w/fr', tag: 'fr', was: 0, left: 0, translated: 0 },
    ]);
    expect(languageRows(before, null).every((r: { translated: number }) => r.translated === 0)).toBe(true);
    expect(pendingPacks(before).map((p: { name: string }) => p.name)).toEqual(['lang-de', 'lang-ar']);
  });

  it('is red with the check words when a pack was refused, and names the commits that are not pushed', () => {
    const r = composeReport({
      day: '2026-10-07',
      tree,
      wait: { why: 'the night 2026-10-07 is over (green, 1a2b3c4d)' },
      rows: languageRows(before, after),
      packs: [
        { dir: '/w/de', state: 'committed', commit: 'c0ffee12', rounds: 1 },
        { dir: '/w/ar', state: 'refused', rounds: 2, feedback: 'lang-ar: 1 answer(s) to fix, nothing written:\n    ar NAME d.new: "OnlyOffice": the name is written "ONLYOFFICE"', agent: [{ round: 1, code: 0, timedOut: true }] },
      ],
      took: '41m 10s',
      where: '/state/nights/x',
    });
    expect(r.result).toBe('red');
    expect(r.title).toBe('filex language packs 2026-10-07: 2 translated, 2 left, RED (1a2b3c4d)');
    expect(r.message).toContain('lang-de de: 2 translated, 0 left - committed c0ffee12, not pushed');
    expect(r.message).toContain('lang-ar ar: 0 translated, 2 left - RED: answers refused after 2 rounds');
    expect(r.message).toContain('ar NAME d.new');
    expect(r.message).toContain('round 2 stopped at its time limit');
    expect(r.message).toContain('lang-fr fr: up to date');
    expect(r.message).toContain('Nothing is pushed or tagged');
    expect(exitCode(r)).toBe(1);
    expect(notifyWanted('changes', r.result)).toBe(true);
  });

  it('is green when everything pending was committed, quiet when nothing was pending, and says why it could not run', () => {
    const green = composeReport({ day: 'd', tree, rows: [{ pack: 'lang-de', dir: '/w/de', tag: 'de', was: 2, left: 0, translated: 2 }], packs: [{ dir: '/w/de', state: 'committed', commit: 'c0ffee12', rounds: 1 }] });
    expect(green.result).toBe('ok');
    expect(exitCode(green)).toBe(0);
    const quiet = composeReport({ day: 'd', tree, rows: [{ pack: 'lang-de', dir: '/w/de', tag: 'de', was: 0, left: 0, translated: 0 }] });
    expect(quiet).toMatchObject({ result: 'info', title: 'filex language packs d: up to date (1a2b3c4d)' });
    expect(notifyWanted('changes', quiet.result)).toBe(false);
    expect(notifyWanted('always', quiet.result)).toBe(true);
    expect(notifyWanted('off', 'red')).toBe(false);
    const late = composeReport({ day: 'd', tree, rows: [{ pack: 'lang-de', dir: '/w/de', tag: 'de', was: 2, left: 2, translated: 0 }], packs: [{ dir: '/w/de', state: 'time', rounds: 0 }] });
    expect(late.result).toBe('waiting');
    expect(late.message).toContain('no time left tonight');
    const broken = composeReport({ day: 'd', error: 'main is not a commit of /work/filex' });
    expect(broken).toMatchObject({ result: 'red', title: 'filex language packs d: could not run' });
    expect(exitCode({ result: broken.result, error: 'x' })).toBe(2);
  });

  it('keeps the newest nights, and only directories it named', () => {
    expect(nightDirName(Date.parse('2026-10-07T01:30:05Z'), '1a2b3c4d5e6f')).toBe('20261007-013005Z-1a2b3c4d');
    expect(nightDirName(0, '')).toBe('19700101-000000Z-tree');
    const names = ['20261005-013000Z-aaaaaaaa', '20261007-013000Z-cccccccc', 'notes', '20261006-013000Z-bbbbbbbb'];
    expect(nightsToPrune(names, 2)).toEqual(['20261005-013000Z-aaaaaaaa']);
  });
});

/* -- the boundary and the prompt ------------------------------------------- */

describe("the agent's command line", () => {
  it('gives the agent file tools in its working directory and nothing else', () => {
    const args = claudeArgs({ model: 'opus' });
    expect(args.slice(0, 3)).toEqual(['-p', '--model', 'opus']);
    for (const flag of ['--restricted', '--strict-mcp-config', '--no-session-persistence', '--disable-slash-commands']) expect(args).toContain(flag);
    expect(args[args.indexOf('--tools') + 1]).toBe(AGENT_TOOLS.join(','));
    expect(AGENT_TOOLS).toEqual(['Read', 'Edit', 'Write', 'Glob', 'Grep']);
    expect(args[args.indexOf('--permission-mode') + 1]).toBe('acceptEdits');
    expect(args[args.indexOf('--permission-prompts') + 1]).toBe('none');
    // Nothing that widens it: no extra directory (a pack would be writable),
    // no MCP config, no skipped permissions, no tool that runs a command.
    const text = args.join(' ');
    for (const never of ['--add-dir', '--mcp-config', '--dangerously-skip-permissions', 'bypassPermissions', 'Bash', 'PowerShell', 'WebFetch', 'WebSearch']) expect(text).not.toContain(never);
    expect(claudeArgs({ model: 'sonnet', extra: ['--effort', 'high'] }).slice(-2)).toEqual(['--effort', 'high']);
  });
});

describe('the prompt', () => {
  const template = readPromptTemplate();
  const fill = (round = 0, feedback = '') =>
    agentPrompt(template, { pack: 'lang-de', tag: 'de', worklist: 'filex-lang-de.json', items: 63, glossary: 'glossary.md', references: ['reference/de.json'], round, feedback });

  it('carries every rule: the fixed names, the plural forms, the plain hyphen, the own letters, one term per concept, the glossary', () => {
    const p = fill();
    for (const name of FIXED_NAMES) expect(p).toContain(name);
    expect(p).toContain('ONLYOFFICE (that spelling, never translated)');
    expect(p).toContain('forms_needed');
    expect(p).toContain('A dash is the plain hyphen "-"');
    expect(p).toContain("Write the language's own letters and punctuation");
    expect(p).toContain('One term per concept');
    expect(p).toContain('`glossary.md`');
    expect(p).toContain('`reference/de.json`');
    expect(p).toContain('`filex-lang-de.json`');
    expect(p).toContain('63 item(s)');
    expect(p).toContain('Do not run anything and do not write outside this directory');
    // Every rule the worklist carries is in it, word for word.
    for (const rule of TRANSLATOR_RULES) expect(p).toContain(rule);
  });

  it('has no long dash and no placeholder left, and says what was refused only from the second round', () => {
    const p = fill();
    expect(p.includes(EM) || p.includes(EN)).toBe(false);
    expect(template.includes(EM) || template.includes(EN)).toBe(false);
    expect(p).not.toMatch(/\{\{[A-Z_]+\}\}/);
    expect(p).not.toContain('round 2');
    const again = fill(1, 'lang-de: 1 answer(s) to fix, nothing written:\n    de NAME b.office: "OnlyOffice": the name is written "ONLYOFFICE"');
    expect(again).toContain('This is round 2');
    expect(again).toContain('    de NAME b.office');
    expect(() => agentPrompt('{{NOPE}}', { pack: 'p', tag: 't', worklist: 'w', items: 1 })).toThrow(/NOPE/);
  });

  it('the rules every worklist carries name the own letters and one term per concept too', () => {
    const all = TRANSLATOR_RULES.join('\n');
    expect(all).toContain("Write the language's own letters");
    expect(all).toContain('One term per concept');
    expect(all.includes(EM) || all.includes(EN)).toBe(false);
  });
});

describe('the worklist an agent answers', () => {
  it('puts each answer right after its key, so "key" then "text" is found once per item', () => {
    const d = packDiff({ prev: PREV, next: NEXT, translation: DE });
    const items = worklistItems({ lang: 'ar', diff: d, prev: PREV, next: { ...NEXT, 'c.n': '{n} files' }, context: { keys: { ...ROWS, 'c.n': { in: 'explorer', syntax: 'plain', plural: true } } }, translation: DE });
    for (const it of items) expect(Object.keys(it).slice(0, 2)).toEqual(['key', 'text']);
    const text = jsonText({ items });
    expect(text).toMatch(/"key": "d\.new",\n\s+"text": ""/);
    expect(text).toMatch(/"key": "b\.office",\n\s+"text": ""/);
    const withForms = worklistItems({
      lang: 'ar',
      diff: { missing: ['c.n'], changed: [], removed: [] },
      prev: {},
      next: { 'c.n': '{n} files' },
      context: { keys: { 'c.n': { in: 'explorer', syntax: 'plain', plural: true } } },
      translation: {},
    });
    expect(Object.keys(withForms[0]).slice(0, 3)).toEqual(['key', 'text', 'forms']);
  });

  it("from a worktree, finds the main checkout's packages too", () => {
    const checkout = path.join(os.tmpdir(), 'x-filex');
    const wt = path.join(checkout, '.claude', 'worktrees', 'langpacks-nightly');
    expect(mainCheckout(wt)).toBe(checkout);
    expect(mainCheckout(checkout)).toBeNull();
    expect(checkoutRoots(wt)).toEqual([wt, checkout]);
    expect(checkoutRoots(checkout)).toEqual([checkout]);
  });
});

describe('the tree: a worktree of the nightly checkout at the ref, outside it, removed after the run', () => {
  it('checks the ref out in its worktree, moves it the next night, leaves a stranger directory alone and removes its own', SLOW, () => {
    const repo = tmp('filex-langpacks-tree-');
    const root = tmp('filex-langpacks-tree-root-');
    const run = (...args: string[]) => git(repo, '-c', 'user.name=T', '-c', 'user.email=t@example.com', '-c', 'commit.gpgsign=false', ...args);
    spawnSync('git', ['init', '-q', repo]);
    // A global core.autocrlf=true (a Windows workstation) would check a.txt
    // out with CRLF in the worktree: the repository says no conversion.
    git(repo, 'config', 'core.autocrlf', 'false');
    git(repo, 'symbolic-ref', 'HEAD', 'refs/heads/main');
    write(path.join(repo, 'a.txt'), 'one\n');
    run('add', '-A');
    run('commit', '-q', '-m', 'one');
    const first = git(repo, 'rev-parse', 'HEAD');
    const cfg = translateSettings({ LANGPACKS_FILEX: repo, LANGPACKS_ROOT: root, LANGPACKS_REF: 'main', LANGPACKS_FALLBACK_REF: 'main' });
    expect(cfg.worktree).toBe(path.join(root, 'tree'));

    const t1 = prepareTree(cfg, null);
    expect(t1).toMatchObject({ dir: cfg.worktree, sha: first, made: true, ref: 'main' });
    expect(fs.readFileSync(path.join(cfg.worktree, 'a.txt'), 'utf8')).toBe('one\n');

    write(path.join(repo, 'a.txt'), 'two\n');
    run('commit', '-q', '-am', 'two');
    const t2 = prepareTree(cfg, null);
    expect(t2.sha).toBe(git(repo, 'rev-parse', 'HEAD'));
    expect(fs.readFileSync(path.join(cfg.worktree, 'a.txt'), 'utf8')).toBe('two\n');
    // The checkout's own working tree is untouched.
    expect(git(repo, 'status', '--porcelain', '--untracked-files=no')).toBe('');

    // `night` with a commit this checkout has not got: the fallback, and said so.
    const t3 = prepareTree({ ...cfg, ref: 'night' }, { sha: 'f'.repeat(40), night: '2026-10-07' });
    expect(t3.ref).toBe('main');

    removeTree(cfg, t3);
    expect(fs.existsSync(cfg.worktree)).toBe(false);
    expect(git(repo, 'worktree', 'list')).not.toContain('langpacks-nightly');

    // A directory at its place that is no worktree: left as it is.
    write(path.join(cfg.worktree, 'mine.txt'), 'somebody else\n');
    expect(() => prepareTree(cfg, null)).toThrow(/no worktree/);
    expect(fs.readFileSync(path.join(cfg.worktree, 'mine.txt'), 'utf8')).toBe('somebody else\n');
    // `none`: the checkout itself, nothing made.
    expect(prepareTree({ ...cfg, ref: 'none' }, null)).toMatchObject({ dir: path.resolve(repo), made: false });
  });

  it('fetches origin/main alone before it reads it, from the checkout own remote', SLOW, () => {
    const upstream = tmp('filex-langpacks-upstream-');
    const run = (dir: string, ...args: string[]) => git(dir, '-c', 'user.name=T', '-c', 'user.email=t@example.com', '-c', 'commit.gpgsign=false', ...args);
    spawnSync('git', ['init', '-q', upstream]);
    git(upstream, 'config', 'core.autocrlf', 'false');
    git(upstream, 'symbolic-ref', 'HEAD', 'refs/heads/main');
    write(path.join(upstream, 'a.txt'), 'one\n');
    run(upstream, 'add', '-A');
    run(upstream, 'commit', '-q', '-m', 'one');
    const src = path.join(tmp('filex-langpacks-src-'), 'src');
    // -c writes core.autocrlf=false into the clone's own config, which its
    // worktrees read (see the first test).
    spawnSync('git', ['clone', '-q', '-c', 'core.autocrlf=false', upstream, src]);
    write(path.join(upstream, 'a.txt'), 'two\n');
    run(upstream, 'commit', '-q', '-am', 'two');
    run(upstream, 'checkout', '-q', '-b', 'other');
    const tip = git(upstream, 'rev-parse', 'main');
    const cfg = translateSettings({ LANGPACKS_FILEX: src, LANGPACKS_ROOT: tmp('filex-langpacks-src-root-') });
    const t = prepareTree(cfg, null);
    expect(t).toMatchObject({ ref: 'origin/main', sha: tip, made: true });
    expect(fs.readFileSync(path.join(t.dir, 'a.txt'), 'utf8')).toBe('two\n');
    // Only main was fetched.
    expect(git(src, 'branch', '-r')).not.toContain('origin/other');
    removeTree(cfg, t);
    expect(fs.existsSync(t.dir)).toBe(false);
  });
});

describe('langpacks-nightly --help', () => {
  it('prints the usage from its own header and exits 0; an unknown option or command exits 2', SLOW, () => {
    const help = spawnSync(process.execPath, [DRIVER, '--help'], { cwd: ROOT, encoding: 'utf8' });
    expect(help.status).toBe(0);
    for (const c of ['run', 'status', 'check']) expect(help.stdout).toContain(`node scripts/langpacks-nightly.mjs ${c}`);
    expect(help.stdout).not.toContain('⚠');
    expect(spawnSync(process.execPath, [DRIVER, 'run', '--frobnicate'], { cwd: ROOT, encoding: 'utf8' }).status).toBe(2);
    expect(spawnSync(process.execPath, [DRIVER, 'translate'], { cwd: ROOT, encoding: 'utf8' }).status).toBe(2);
  });
});

describe('the timer and the installer', () => {
  const CHAIN = path.join(ROOT, 'scripts', 'chain');
  const read = (rel: string) => fs.readFileSync(path.join(CHAIN, rel), 'utf8');

  it('start the translation at night from the installed copy, a red night being no failed unit', () => {
    const service = read('systemd/filex-langpacks.service');
    expect(service).toMatch(/^ExecStart=@NODE@ @BIN@\/scripts\/langpacks-nightly\.mjs run --env @ENV@$/m);
    expect(service).toMatch(/^Type=oneshot$/m);
    expect(service).toMatch(/^SuccessExitStatus=1$/m);
    const timer = read('systemd/filex-langpacks.timer');
    expect(timer).toMatch(/^OnCalendar=\*-\*-\* @AT@$/m);
    expect(timer).toMatch(/^Persistent=false$/m);
  });

  it('installs every module the installed driver imports, and fills every placeholder the units carry', () => {
    const sh = read('install-langpacks.sh');
    const files = (/^FILES="([^"]+)"$/m.exec(sh)?.[1] ?? '').split(' ');
    // Follow the driver's relative imports (and the prompt template it
    // reads) through every module it reaches.
    const scripts = path.join(ROOT, 'scripts');
    const need = new Set<string>();
    const visit = (rel: string) => {
      if (need.has(rel)) return;
      need.add(rel);
      if (!rel.endsWith('.mjs')) return;
      const src = fs.readFileSync(path.join(scripts, rel), 'utf8');
      const refs = [...src.matchAll(/^(?:import|export) [^;]*? from '(\.{1,2}\/[^']+)';$/gms), ...src.matchAll(/new URL\('(\.{1,2}\/[^']+)', import\.meta\.url\)/g)].map((m) => m[1]);
      for (const r of refs) visit(path.posix.normalize(path.posix.join(path.posix.dirname(rel), r)));
    };
    visit('langpacks-nightly.mjs');
    for (const rel of need) expect(files, `the installed copy needs scripts/${rel}`).toContain(rel);
    for (const rel of files) expect(fs.existsSync(path.join(scripts, rel)), rel).toBe(true);
    for (const u of ['filex-langpacks.service', 'filex-langpacks.timer']) {
      expect(sh).toContain(u);
      for (const p of read(path.join('systemd', u)).match(/@[A-Z_]+@/g) ?? []) expect(sh, `${u}: ${p}`).toContain(`"s|${p}|`);
    }
    // It never overwrites the settings, never enables anything unasked,
    // deletes nothing and installs no agent.
    expect(sh).toMatch(/if \[ -e "\$ENV_FILE" \]; then/);
    expect(sh).toMatch(/if \[ "\$ENABLE" = 1 \]; then/);
    expect(sh).not.toMatch(/rm -rf|rm -r /);
    expect(sh).not.toMatch(/^\s*(run )?npm install/m);
    // Release day pushes the release commit back into the checked-out branch.
    expect(sh).toContain('receive.denyCurrentBranch updateInstead');
  });

  it('the settings example names every key the run reads, with the installer filling the root', () => {
    const example = fs.readFileSync(path.join(ROOT, 'scripts', 'langpacks-nightly.env.example'), 'utf8');
    const lib = fs.readFileSync(path.join(ROOT, 'scripts', 'lib', 'langpacks-nightly.mjs'), 'utf8');
    for (const m of lib.matchAll(/env\.(LANGPACKS_[A-Z_]+)/g)) expect(example, m[1]).toContain(m[1]);
    expect(example).toMatch(/^LANGPACKS_ROOT=/m);
    expect(read('install-langpacks.sh')).toContain('s|^LANGPACKS_ROOT=.*|LANGPACKS_ROOT=$ROOT|');
  });

  it('runs on the build host only: nothing of a Windows workstation is left in it', () => {
    for (const rel of ['langpacks-nightly.mjs', 'lib/langpacks-nightly.mjs', 'langpacks-nightly.env.example']) {
      const src = fs.readFileSync(path.join(ROOT, 'scripts', rel), 'utf8');
      for (const word of ['win32', 'taskkill', 'LOCALAPPDATA', 'schtasks', 'commandArgv', 'Git Bash']) expect(src, `${rel}: ${word}`).not.toContain(word);
    }
  });
});

/* -- a whole night ------------------------------------------------------------ */

describe('a night: nothing pending', () => {
  it('starts no agent, commits nothing and exits 0', SLOW, () => {
    const s = setup('filex-langpacks-night-none-', { catalogue: NEXT, translation: { ...DE, ...GOOD } });
    const head = git(s.pack, 'rev-parse', 'HEAD');
    const r = night(s, { plan: { rounds: [GOOD] } });
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(agentCalls(s)).toEqual([]);
    expect(git(s.pack, 'rev-parse', 'HEAD')).toBe(head);
    expect(last(s)).toMatchObject({ decision: 'up to date', result: 'info', exit: 0 });
  });
});

describe('a night: the agent answers', () => {
  it('commits the pack locally, counts per language, and the agent worked on copies in a directory of its own', SLOW, () => {
    const s = setup('filex-langpacks-night-green-', { glossary: '# Glossary\n\n| English | Deutsch |\n|---|---|\n| file | Datei |\n' });
    const r = night(s, { plan: { rounds: [GOOD] } });
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    const tr = readJson(path.join(s.pack, 'translations', 'de.json'));
    expect(tr['b.office']).toBe(GOOD['b.office']);
    expect(tr['d.new']).toBe(GOOD['d.new']);
    expect(readJson(path.join(s.pack, 'catalogue', 'filex-catalogue-en.json'))).toEqual(NEXT);
    expect(git(s.pack, 'status', '--porcelain')).toBe('');
    expect(git(s.pack, 'log', '-1', '--format=%s')).toMatch(/^Sync to filex abcdef12: /);
    expect(git(s.pack, 'log', '-1', '--format=%B')).toContain('Co-Authored-By: Test Agent <agent@example.com>');
    // Nothing is tagged.
    expect(git(s.pack, 'tag', '-l')).toBe('');

    const rec = last(s);
    expect(rec).toMatchObject({ decision: 'translated', result: 'ok', exit: 0 });
    expect(rec.rows).toEqual([{ pack: 'lang-de', dir: s.pack, tag: 'de', was: 2, left: 0, translated: 2 }]);
    expect(rec.packs[0]).toMatchObject({ name: 'lang-de', state: 'committed', rounds: 1 });

    const calls = agentCalls(s);
    expect(calls).toHaveLength(1);
    expect(path.relative(rec.night, calls[0].cwd)).toBe(path.join('agent', 'filex-lang-de'));
    expect(calls[0].files).toEqual(['AGENT.md', 'catalogue', 'filex-lang-de.json', 'glossary.md', 'reference']);
    // A HOME of its own: nothing of the host user's ~/.claude reaches it.
    expect(calls[0].home).toBe(path.join(s.state, 'agent-home'));
    expect(calls[0].prompt).toContain('`filex-lang-de.json` - the worklist: 2 item(s)');
    // The guide the agent reads names its own directory, not the run's.
    const guide = fs.readFileSync(path.join(calls[0].cwd, 'AGENT.md'), 'utf8');
    expect(guide).not.toContain(path.join(rec.night, 'todo', 'filex-lang-de').split(path.sep).join('/'));
    expect(fs.existsSync(path.join(rec.night, 'report.txt'))).toBe(true);
  });
});

describe('a night: refused answers', () => {
  it('go back to the agent once with what the check said, and the fixed answers are committed', SLOW, () => {
    const s = setup('filex-langpacks-night-fix-');
    const r = night(s, { plan: { rounds: [{ ...GOOD, 'b.office': 'In OnlyOffice bearbeiten' }, GOOD] } });
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    const calls = agentCalls(s);
    expect(calls).toHaveLength(2);
    expect(calls[1].prompt).toContain('This is round 2');
    expect(calls[1].prompt).toContain('NAME');
    expect(calls[1].prompt).toContain('b.office');
    expect(readJson(path.join(s.pack, 'translations', 'de.json'))['b.office']).toBe(GOOD['b.office']);
    expect(last(s).packs[0]).toMatchObject({ state: 'committed', rounds: 2 });
  });

  it('a worklist the agent left as no JSON is a refusal too: its copy is put back, and the next round answers again', SLOW, () => {
    const s = setup('filex-langpacks-night-broken-');
    const r = night(s, { plan: { rounds: [GOOD], broken: true } });
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    const calls = agentCalls(s);
    expect(calls).toHaveLength(2);
    expect(calls[1].prompt).toContain('was not valid JSON after your edit');
    const rec = last(s);
    expect(rec.packs[0]).toMatchObject({ state: 'committed', rounds: 2 });
    // What it wrote is kept for a person.
    expect(fs.readFileSync(path.join(rec.night, 'logs', 'filex-lang-de-not-json-1.json'), 'utf8')).toBe('{ not json');
  });
});

describe('a night: the pack validators go red', () => {
  it('puts the pack back, tells the agent, and the night is red when it stays red', SLOW, () => {
    const s = setup('filex-langpacks-night-red-');
    const before = filesOf(s.pack);
    const head = git(s.pack, 'rev-parse', 'HEAD');
    const r = night(s, { plan: { rounds: [GOOD] }, extra: { LANGPACK_TEST_RED: '1' } });
    expect(r.code).toBe(1);
    expect(git(s.pack, 'rev-parse', 'HEAD')).toBe(head);
    expect(git(s.pack, 'status', '--porcelain')).toBe('');
    expect(filesOf(s.pack)).toEqual(before);
    const calls = agentCalls(s);
    expect(calls).toHaveLength(2);
    expect(calls[1].prompt).toContain('put back as it was');
    const rec = last(s);
    expect(rec).toMatchObject({ result: 'red', exit: 1 });
    expect(rec.packs[0]).toMatchObject({ state: 'red', rounds: 2 });
    expect(rec.rows[0]).toMatchObject({ translated: 0, left: 2 });
    expect(r.out).toContain('RED');
  });
});

describe('a night: the boundary', () => {
  it("takes only the answers: a rewritten worklist, a second worklist and an edited catalogue in the agent's directory reach no pack", SLOW, () => {
    const s = setup('filex-langpacks-night-tamper-');
    const other = makePack(path.join(s.base, 'elsewhere'), { catalogue: PREV, rows: ROWS, translation: DE, remote: false });
    const otherBefore = filesOf(other);
    const otherHead = git(other, 'rev-parse', 'HEAD');
    const r = night(s, { plan: { rounds: [GOOD], tamper: other } });
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    const tr = readJson(path.join(s.pack, 'translations', 'de.json'));
    expect(tr['d.new']).toBe(GOOD['d.new']);
    expect(tr['z.extra']).toBeUndefined();
    expect(readJson(path.join(s.pack, 'catalogue', 'filex-catalogue-en.json'))).toEqual(NEXT);
    expect(filesOf(other)).toEqual(otherBefore);
    expect(git(other, 'rev-parse', 'HEAD')).toBe(otherHead);
  });

  it('stops the night when something outside the agent directory changed while it ran, and applies nothing', SLOW, () => {
    const s = setup('filex-langpacks-night-escape-');
    const head = git(s.pack, 'rev-parse', 'HEAD');
    const stray = path.join(s.pack, 'stray.txt');
    const r = night(s, { plan: { rounds: [GOOD], escape: stray } });
    expect(r.code).toBe(1);
    expect(git(s.pack, 'rev-parse', 'HEAD')).toBe(head);
    expect(readJson(path.join(s.pack, 'translations', 'de.json'))['d.new']).toBeUndefined();
    const rec = last(s);
    expect(rec.packs[0]).toMatchObject({ state: 'guard' });
    expect(rec.result).toBe('red');
    // It deletes nothing it did not make: the stray file is there for a person.
    expect(fs.existsSync(stray)).toBe(true);
  });
});

describe('a night: --dry-run, the wait and the lock', () => {
  it('--dry-run measures and stops: no agent, no commit', SLOW, () => {
    const s = setup('filex-langpacks-night-dry-');
    const head = git(s.pack, 'rev-parse', 'HEAD');
    const r = night(s, { args: ['--dry-run'] });
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(agentCalls(s)).toEqual([]);
    expect(git(s.pack, 'rev-parse', 'HEAD')).toBe(head);
    expect(last(s)).toMatchObject({ decision: 'dry run', exit: 0 });
    expect(r.out).toContain('would translate lang-de (2)');
  });

  it('reads the nightly run file on this host: goes when the night is over or its process is gone, late when it is still going after the wait', SLOW, () => {
    const s = setup('filex-langpacks-night-wait-');
    const file = path.join(s.base, 'tonight.json');
    const tonight = (o: object) => write(file, JSON.stringify({ night: '2026-10-07', at: new Date().toISOString(), sha: '1a2b3c4d', ...o }));
    // A process that has ended: its pid names nobody alive.
    const deadPid = spawnSync(process.execPath, ['-e', '']).pid;
    const withFile = { LANGPACKS_NIGHT_FILE: file };

    tonight({ phase: 'done', decision: 'ran', ok: true, pid: deadPid });
    const over = night(s, { args: ['--dry-run'], extra: withFile });
    expect(over.code, `${over.out}${over.err}`).toBe(0);
    expect(last(s).wait).toMatchObject({ state: 'over', step: 'go', late: false });

    tonight({ phase: 'running', decision: 'ran', pid: process.pid });
    const going = night(s, { args: ['--dry-run'], extra: { ...withFile, LANGPACKS_WAIT_MAX_MIN: '0' } });
    expect(going.code, `${going.out}${going.err}`).toBe(0);
    expect(last(s).wait).toMatchObject({ state: 'going', step: 'go', late: true });

    tonight({ phase: 'running', decision: 'ran', pid: deadPid });
    night(s, { args: ['--dry-run'], extra: withFile });
    expect(last(s).wait).toMatchObject({ state: 'gone', step: 'go', late: false });

    // No file: no night started on this host, nothing to wait for.
    night(s, { args: ['--dry-run'], extra: { LANGPACKS_NIGHT_FILE: path.join(s.base, 'none.json') } });
    expect(last(s).wait).toMatchObject({ state: 'unknown', step: 'go' });

    // --no-wait does not look.
    tonight({ phase: 'running', pid: process.pid });
    night(s, { args: ['--dry-run', '--no-wait'], extra: withFile });
    expect(last(s).wait).toMatchObject({ state: 'skipped' });
  });

  it('refuses a second run while one holds the lock', SLOW, () => {
    const s = setup('filex-langpacks-night-lock-');
    write(path.join(s.state, 'run.lock'), `${process.pid}\n`);
    const r = night(s);
    expect(r.code).toBe(2);
    expect(r.err).toContain('another run');
    expect(agentCalls(s)).toEqual([]);
  });
});
