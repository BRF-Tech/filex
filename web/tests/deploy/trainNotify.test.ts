// Saying that a long step has ended (scripts/lib/notify.mjs): a JSON POST
// for a person, an MCP `notify` call that wakes the agent waiting for it.
// Used by the merge queue, the ship and when-done, and by the build host's
// test chain.
//
// ⚠ Why this exists (#179, #165 B8): through 0.52 the maintainer's session
// watched every long step itself - the chain, the release resume, the deploy -
// turn after turn. A step that says when it ends lets the session stop
// watching. The rules held here: a key is read from a file or a named
// variable and never logged; nothing throws (a release that went out does not
// turn red because a notification did not); and there is one poster, not one
// per tool.
//
// No request leaves the process: every test hands the module its own fetch.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { announce, notifySettings, postNotify, severityOf, wake, wakeRequest, wakeVerdict } from '../../../scripts/lib/notify.mjs';
import { listSetting, loadSettings, parseEnvFile } from '../../../scripts/lib/settings.mjs';
import { parseEnvFile as envParseEnvFile } from '../../../scripts/chain/env.mjs';
import { parseEnvFile as chainParseEnvFile } from '../../../scripts/chain/run.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true });
});
function keyFile(value: string) {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-notify-test-'));
  roots.push(d);
  const f = path.join(d, 'key');
  fs.writeFileSync(f, `${value}\n`);
  return f;
}

type Call = { url: string; init: { method: string; headers: Record<string, string>; body: string } };
function fakeFetch(answer: (c: Call) => { status: number; body?: string } = () => ({ status: 201, body: '{"ok":true}' })) {
  const calls: Call[] = [];
  const fn = async (url: string, init: Call['init']) => {
    const c = { url, init };
    calls.push(c);
    const a = answer(c);
    return { status: a.status, text: async () => a.body ?? '' };
  };
  return { fn, calls };
}

describe('settings', () => {
  it('one parser for every KEY=VALUE file: the chain\'s, which its installed nightly copy carries alone', () => {
    expect(parseEnvFile).toBe(envParseEnvFile);
    expect(chainParseEnvFile).toBe(envParseEnvFile);
    expect(parseEnvFile('# c\nexport A=1\nB="two words"\nC=\n')).toEqual({ A: '1', B: 'two words', C: '' });
    expect(() => parseEnvFile('not a pair')).toThrow(/KEY=VALUE/);
    expect(listSetting(' a, b  c ,')).toEqual(['a', 'b', 'c']);
  });

  it('the process environment wins over the file, and a named file that is not there is an error', () => {
    const d = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-settings-test-'));
    roots.push(d);
    const f = path.join(d, 'train.env');
    fs.writeFileSync(f, 'A=file\nB=file\n');
    const s = loadSettings({ file: f, env: { B: 'env' } });
    expect(s.env.A).toBe('file');
    expect(s.env.B).toBe('env');
    expect(loadSettings({ envVar: 'X', env: { X: f } }).file).toBe(f);
    expect(loadSettings({ fallback: 'none-such.env', env: {}, home: d }).file).toBe('');
    expect(() => loadSettings({ file: path.join(d, 'missing.env'), env: {} })).toThrow(/does not exist/);
  });

  it('a sink is off until its URL is set', () => {
    expect(notifySettings({})).toEqual({ notify: null, wake: null });
    const s = notifySettings({ FILEX_NOTIFY_URL: 'https://n.example/send', FILEX_WAKE_URL: 'https://w.example/mcp', FILEX_WAKE_AGENT: 'a, b', FILEX_WAKE_TASK: '178' });
    expect(s.notify).toMatchObject({ url: 'https://n.example/send', group: 'infra', source: 'filex-train' });
    expect(s.wake).toMatchObject({ agents: ['a', 'b'], from: 'filex-train', task: 178 });
  });
});

describe('a person: the JSON POST', () => {
  it('sends the Bearer key from its file and the fields the notify service takes', async () => {
    const f = fakeFetch();
    const cfg = { url: 'https://n.example/send', keyFile: keyFile('bn_secret_value'), group: 'infra', source: 'filex-train' };
    const logs: string[] = [];
    const r = await postNotify(cfg, { title: 'filex ship v0.53.0: deployed', message: 'all green', severity: 'success' }, { fetch: f.fn, log: (l: string) => logs.push(l) });
    expect(r).toEqual({ sent: true, status: 201 });
    expect(f.calls).toHaveLength(1);
    expect(f.calls[0].init.headers.authorization).toBe('Bearer bn_secret_value');
    expect(JSON.parse(f.calls[0].init.body)).toEqual({ group: 'infra', source: 'filex-train', severity: 'success', title: 'filex ship v0.53.0: deployed', message: 'all green' });
    expect(logs.join('\n')).not.toContain('bn_secret_value');
  });

  it('without its key it sends nothing, says which setting, and does not throw', async () => {
    const f = fakeFetch();
    const logs: string[] = [];
    const r = await postNotify({ url: 'https://n.example/send', keyFile: '/no/such/key', group: 'g', source: 's' }, { title: 't', message: 'm' }, { fetch: f.fn, log: (l: string) => logs.push(l) });
    expect(r.sent).toBe(false);
    expect(f.calls).toHaveLength(0);
    expect(logs.join('\n')).toContain('FILEX_NOTIFY_KEY_FILE');
  });

  it('a failing endpoint is logged, not thrown', async () => {
    const r = await postNotify({ url: 'https://n.example/send', keyFile: keyFile('k'), group: 'g', source: 's' }, { title: 't' }, {
      fetch: async () => {
        throw new Error('ECONNREFUSED');
      },
    });
    expect(r).toMatchObject({ sent: false });
  });

  it('maps a result to the severity the service knows', () => {
    expect([severityOf('ok'), severityOf('red'), severityOf('waiting'), severityOf('info')]).toEqual(['success', 'danger', 'warning', 'info']);
  });
});

describe('an agent: the MCP notify call', () => {
  it('is a tools/call of notify, with the task only beside its project', () => {
    expect(wakeRequest({ agent: 'release-session', from: 'filex-train', body: 'done', project: 'p-1', task: 178 })).toEqual({
      jsonrpc: '2.0',
      id: 1,
      method: 'tools/call',
      params: { name: 'notify', arguments: { agent: 'release-session', from: 'filex-train', body: 'done', project: 'p-1', task_id: 178 } },
    });
    expect(wakeRequest({ agent: 'a', from: 'f', body: 'b', task: 178 }).params.arguments).toEqual({ agent: 'a', from: 'f', body: 'b' });
  });

  it('reads plain JSON and server-sent events, and a refusing tool is a failure', () => {
    expect(wakeVerdict(200, '{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}')).toEqual({ ok: true });
    expect(wakeVerdict(200, 'event: message\ndata: {"jsonrpc":"2.0","id":1,"result":{"content":[]}}\n\n')).toEqual({ ok: true });
    expect(wakeVerdict(200, '{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"no such agent"}]}}').ok).toBe(false);
    expect(wakeVerdict(500, '').ok).toBe(false);
    expect(wakeVerdict(200, 'not json').ok).toBe(false);
  });

  it('wakes each agent with the token from the named variable, and never logs it', async () => {
    const f = fakeFetch(() => ({ status: 200, body: '{"jsonrpc":"2.0","id":1,"result":{"content":[]}}' }));
    const logs: string[] = [];
    const cfg = notifySettings({ FILEX_WAKE_URL: 'https://w.example/mcp', FILEX_WAKE_TOKEN_ENV: 'SESSION_TOKEN', FILEX_WAKE_AGENT: 'release-session,maintainer' }).wake;
    const out = await wake(cfg, 'the queue is done', { fetch: f.fn, log: (l: string) => logs.push(l), env: { SESSION_TOKEN: 'agt_value' } });
    expect(out).toEqual([
      { agent: 'release-session', sent: true },
      { agent: 'maintainer', sent: true },
    ]);
    expect(f.calls.map((c) => c.init.headers['x-access-token'])).toEqual(['agt_value', 'agt_value']);
    expect(f.calls.map((c) => JSON.parse(c.init.body).params.arguments.agent)).toEqual(['release-session', 'maintainer']);
    expect(logs.join('\n')).not.toContain('agt_value');
  });

  it('without a token it calls nothing', async () => {
    const f = fakeFetch();
    const cfg = notifySettings({ FILEX_WAKE_URL: 'https://w.example/mcp', FILEX_WAKE_AGENT: 'a' }).wake;
    const out = await wake(cfg, 'x', { fetch: f.fn, env: {} });
    expect(out).toEqual([{ agent: 'a', sent: false, why: 'no token' }]);
    expect(f.calls).toHaveLength(0);
  });

  it('announce reaches both sinks with one result', async () => {
    const f = fakeFetch((c) => (c.url.includes('mcp') ? { status: 200, body: '{"jsonrpc":"2.0","id":1,"result":{}}' } : { status: 201 }));
    const env = {
      FILEX_NOTIFY_URL: 'https://n.example/send',
      FILEX_NOTIFY_KEY_FILE: keyFile('k'),
      FILEX_WAKE_URL: 'https://w.example/mcp',
      FILEX_WAKE_TOKEN_FILE: keyFile('t'),
      FILEX_WAKE_AGENT: 'release-session',
    };
    const r = await announce(env, { title: 'merge queue: done', message: '3 merged', result: 'ok' }, { fetch: f.fn });
    expect(r.notify.sent).toBe(true);
    expect(r.wake).toEqual([{ agent: 'release-session', sent: true }]);
    expect(JSON.parse(f.calls.find((c) => !c.url.includes('mcp'))!.init.body).severity).toBe('success');
    expect(JSON.parse(f.calls.find((c) => c.url.includes('mcp'))!.init.body).params.arguments.body).toBe('merge queue: done\n\n3 merged');
  });
});

describe('one poster', () => {
  it('the build host chain posts through it, not with a poster of its own', () => {
    const src = fs.readFileSync(path.join(REPO, 'scripts', 'chain', 'run.mjs'), 'utf8');
    expect(src).toContain("from '../lib/notify.mjs'");
    expect(src).toContain('postNotify(');
    expect(src).not.toMatch(/Bearer/);
  });
});
