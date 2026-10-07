// Saying that a long step has ended: to a person, and to the agent that is
// waiting for it.
//
// Two sinks, each optional, each switched on by settings (KEY=VALUE, read
// through scripts/lib/settings.mjs; the keys are in
// scripts/train/train.env.example):
//
//   notify  a JSON POST {group, source, severity, title, message} with a
//           Bearer key read from a file: FILEX_NOTIFY_URL, FILEX_NOTIFY_KEY_FILE,
//           FILEX_NOTIFY_GROUP, FILEX_NOTIFY_SOURCE. The build host's test
//           chain posts the same shape (scripts/chain/run.mjs, CHAIN_NOTIFY_*).
//   wake    an MCP server's `notify` tool, called over HTTP (JSON-RPC
//           `tools/call`), once for every agent FILEX_WAKE_AGENT names: an
//           agent whose session waits on its inbox wakes up on it, and a
//           person's name there reaches that person. FILEX_WAKE_URL,
//           FILEX_WAKE_TOKEN_FILE or FILEX_WAKE_TOKEN_ENV (the variable that
//           holds the token), FILEX_WAKE_AGENT, FILEX_WAKE_FROM,
//           FILEX_WAKE_PROJECT, FILEX_WAKE_TASK.
//
// ⚠ Why this exists (#165 B8, #179): through 0.52 the maintainer's session
// watched every long step itself - a four-hour chain, a release resume, a
// deploy - turn after turn, and each turn of watching was context spent on
// nothing. A step that says when it ends lets the session stop watching.
//
// ⚠ A key or token is read from a file or a named variable when it is used,
// and is never written to a log, an argument list or an error message.
//
// ⚠ Nothing here throws. A notification that cannot be sent is logged, and
// the step it reports on keeps its own result: a release that went out does
// not turn red because a phone did not buzz.

import fs from 'node:fs';

import { listSetting } from './settings.mjs';

const MESSAGE_MAX = 7900;
const WAKE_MAX = 4000;

/** The severity a sink is sent for a result: green, red, or waiting for a person. */
export function severityOf(result) {
  if (result === 'ok' || result === true) return 'success';
  if (result === 'waiting') return 'warning';
  if (result === 'info') return 'info';
  return 'danger';
}

/**
 * The two sinks as the settings describe them; a sink without its URL is
 * null (off). Nothing is read from disk here.
 */
export function notifySettings(env = process.env) {
  const notify = env.FILEX_NOTIFY_URL
    ? {
        url: env.FILEX_NOTIFY_URL,
        keyFile: env.FILEX_NOTIFY_KEY_FILE || '',
        group: env.FILEX_NOTIFY_GROUP || 'infra',
        source: env.FILEX_NOTIFY_SOURCE || 'filex-train',
      }
    : null;
  const wake = env.FILEX_WAKE_URL
    ? {
        url: env.FILEX_WAKE_URL,
        tokenFile: env.FILEX_WAKE_TOKEN_FILE || '',
        tokenEnv: env.FILEX_WAKE_TOKEN_ENV || '',
        agents: listSetting(env.FILEX_WAKE_AGENT),
        from: env.FILEX_WAKE_FROM || 'filex-train',
        project: env.FILEX_WAKE_PROJECT || '',
        task: env.FILEX_WAKE_TASK ? Number(env.FILEX_WAKE_TASK) : null,
      }
    : null;
  return { notify, wake };
}

function readSecretFile(file) {
  if (!file) return '';
  try {
    return fs.readFileSync(file, 'utf8').trim();
  } catch {
    return '';
  }
}

/**
 * POSTs one notification. Returns { sent, status?, why? }.
 * `keyVar` only names, in a log line, the setting that should hold the key.
 */
export async function postNotify(cfg, { title, message, severity = 'info' }, { fetch = globalThis.fetch, log = () => {}, keyVar = 'FILEX_NOTIFY_KEY_FILE' } = {}) {
  if (!cfg?.url) return { sent: false, why: 'off' };
  const key = readSecretFile(cfg.keyFile);
  if (!key) {
    log(`notify skipped: no key in ${keyVar} (${cfg.keyFile || 'unset'})`);
    return { sent: false, why: 'no key' };
  }
  try {
    const res = await fetch(cfg.url, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${key}` },
      body: JSON.stringify({
        group: cfg.group,
        source: cfg.source,
        severity,
        title: String(title).slice(0, 250),
        message: String(message ?? '').slice(0, MESSAGE_MAX),
      }),
      signal: AbortSignal.timeout(20_000),
    });
    log(`notify: HTTP ${res.status}`);
    return { sent: res.status >= 200 && res.status < 300, status: res.status };
  } catch (e) {
    log(`notify failed: ${e?.message ?? e}`);
    return { sent: false, why: String(e?.message ?? e) };
  }
}

/** The JSON-RPC request that calls the MCP `notify` tool for one agent. */
export function wakeRequest({ agent, from, body, project = '', task = null, id = 1 }) {
  const args = { agent, from, body: String(body ?? '').slice(0, WAKE_MAX) };
  // A task number means nothing without its project (numbers are per
  // project), and the tool refuses a task without one.
  if (project) args.project = project;
  if (project && Number.isInteger(task) && task > 0) args.task_id = task;
  return { jsonrpc: '2.0', id, method: 'tools/call', params: { name: 'notify', arguments: args } };
}

/**
 * What an MCP endpoint answered to a `tools/call`: { ok, text?, why? }, the
 * text being the tool's content. It may answer plain JSON or a server-sent
 * event stream (`data: {...}`); a tool error is a result with isError, not an
 * HTTP error. The one reader of an MCP answer here: wake, and the language
 * packs' nightly run asking for its Claude credential (scripts/langpacks-nightly.mjs).
 */
export function toolResult(status, text) {
  if (status < 200 || status >= 300) return { ok: false, why: `HTTP ${status}` };
  let doc = null;
  try {
    doc = JSON.parse(text);
  } catch {
    const data = String(text)
      .split('\n')
      .filter((l) => l.startsWith('data: '))
      .map((l) => l.slice(6))
      .pop();
    try {
      doc = data ? JSON.parse(data) : null;
    } catch {
      doc = null;
    }
  }
  if (!doc) return { ok: false, why: 'the answer was not JSON-RPC' };
  if (doc.error) return { ok: false, why: `error ${doc.error.code ?? ''} ${doc.error.message ?? ''}`.trim() };
  if (!doc.result) return { ok: false, why: 'no result' };
  const content = doc.result.content?.map?.((c) => c.text).join(' ') ?? '';
  if (doc.result.isError) return { ok: false, why: `the tool refused: ${content.slice(0, 200)}` };
  return { ok: true, text: content };
}

/** What an MCP endpoint answered to the `notify` call: { ok, why? }. */
export function wakeVerdict(status, text) {
  const r = toolResult(status, text);
  return r.ok ? { ok: true } : { ok: false, why: r.why };
}

/**
 * POSTs one JSON-RPC request to an MCP endpoint with the token as
 * X-Access-Token; returns { status, text }. Throws on a network error or
 * after 20 seconds. The token is never logged.
 */
export async function postRpc(url, token, request, { fetch = globalThis.fetch } = {}) {
  const res = await fetch(url, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      accept: 'application/json, text/event-stream',
      'x-access-token': token,
    },
    body: JSON.stringify(request),
    signal: AbortSignal.timeout(20_000),
  });
  return { status: res.status, text: await res.text() };
}

/** Wakes every agent the settings name. Returns one { agent, sent, why? } each. */
export async function wake(cfg, body, { fetch = globalThis.fetch, log = () => {}, env = process.env } = {}) {
  if (!cfg?.url || cfg.agents.length === 0) return [];
  const token = cfg.tokenFile ? readSecretFile(cfg.tokenFile) : cfg.tokenEnv ? String(env[cfg.tokenEnv] ?? '').trim() : '';
  if (!token) {
    log(`wake skipped: no token (FILEX_WAKE_TOKEN_FILE ${cfg.tokenFile || 'unset'}, FILEX_WAKE_TOKEN_ENV ${cfg.tokenEnv || 'unset'})`);
    return cfg.agents.map((agent) => ({ agent, sent: false, why: 'no token' }));
  }
  const out = [];
  for (const [i, agent] of cfg.agents.entries()) {
    try {
      const res = await postRpc(cfg.url, token, wakeRequest({ agent, from: cfg.from, body, project: cfg.project, task: cfg.task, id: i + 1 }), { fetch });
      const v = wakeVerdict(res.status, res.text);
      log(`wake ${agent}: ${v.ok ? 'sent' : `not sent (${v.why})`}`);
      out.push({ agent, sent: v.ok, ...(v.ok ? {} : { why: v.why }) });
    } catch (e) {
      log(`wake ${agent} failed: ${e?.message ?? e}`);
      out.push({ agent, sent: false, why: String(e?.message ?? e) });
    }
  }
  return out;
}

/**
 * Both sinks at once: `result` is 'ok', 'red', 'waiting' or 'info'. The wake
 * body is the title and the message, so an agent reads it without a second
 * call.
 */
export async function announce(env, { title, message = '', result = 'info' }, { fetch = globalThis.fetch, log = () => {} } = {}) {
  const cfg = notifySettings(env);
  const severity = severityOf(result);
  const [n, w] = await Promise.all([
    postNotify(cfg.notify, { title, message, severity }, { fetch, log }),
    wake(cfg.wake, `${title}\n\n${message}`.trim(), { fetch, log, env }),
  ]);
  return { notify: n, wake: w };
}
