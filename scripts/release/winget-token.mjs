// The winget token, asked before anything is written (task #290): the
// preflight reads the token the tag run's winget pull requests are opened
// with and asks GitHub whether it still works, and for how long. The
// judgement is checks.mjs's (wingetTokenVerdict, with the incident); this
// file reads the token and asks.
//
// ⚠⚠ The token is read by a command the release train's settings name
// (FILEX_WINGET_TOKEN_CMD: the vault's command-line reader, which prints the
// value on its standard output - a pipe, never a terminal), held in this
// process for one request, and never written anywhere: not to a log, not to
// the run's record, not to a command line. Every text this file hands back
// has it taken out, whatever said it.
//
// The repository secret WINGET_TOKEN is what the tag run uses, and GitHub
// never gives a secret's value back: so the vault's copy is asked, and the
// two are kept the same (wingetTokenFix pipes the one into the other).

import { loadSettings } from '../lib/settings.mjs';
import { WINGET_TOKEN_DAYS, wingetTokenFix, wingetTokenVerdict } from './checks.mjs';
import { run, slash } from './engine.mjs';

/** The setting that names the command printing the token. */
export const TOKEN_CMD = 'FILEX_WINGET_TOKEN_CMD';

/** The headers the verdict reads; nothing else of GitHub's answer is kept. */
const HEADERS = ['x-oauth-scopes', 'github-authentication-token-expiration', 'x-ratelimit-remaining', 'x-ratelimit-reset'];

const tail = (s, n = 6) => String(s ?? '').replace(/\s+$/, '').split(/\r?\n/).slice(-n).join('\n');

/** `text` with every occurrence of `secret` taken out. */
export function redact(text, secret) {
  const s = String(text ?? '');
  return secret && String(secret).length >= 4 ? s.split(String(secret)).join('[the token]') : s;
}

/** The release train's settings, as the ship and matrix-watch read them. */
const trainSettings = () => loadSettings({ envVar: 'FILEX_TRAIN_ENV', fallback: 'filex-train.env' });

/**
 * Reads the token by FILEX_WINGET_TOKEN_CMD, run in `bash` (Git Bash on
 * Windows, engine.mjs → findBash). Returns { token, cmd } or { error, cmd? }.
 * `settings` and `runner` are injectable for the tests.
 */
export function readWingetToken({ bash, settings = trainSettings, runner = run } = {}) {
  let s;
  try {
    s = settings();
  } catch (e) {
    return { error: `the release train's settings could not be read (${e?.message ?? e}), so neither could ${TOKEN_CMD}` };
  }
  const where = s?.file ? slash(s.file) : '~/.config/filex-train.env (there is none here)';
  const cmd = String(s?.env?.[TOKEN_CMD] ?? '').trim();
  if (!cmd) {
    return {
      error: [
        `${TOKEN_CMD} is not set (the environment, or ${where}): nothing here can read the winget token, so nothing can tell whether the tag run's winget pull requests will work - and 0.57.6's tag run died on an expired one.`,
        `Set it to a command that prints the token on its standard output and nothing else - the vault's command-line reader for the entry that holds the repository secret WINGET_TOKEN's value. It runs in bash; what it prints is never shown. scripts/train/train.env.example, "the winget token", says more.`,
      ].join('\n'),
    };
  }
  if (!bash) return { error: `no bash to run ${TOKEN_CMD} with (Git for Windows' bin/bash.exe was not found)`, cmd };
  const r = runner(bash, ['-c', cmd]);
  const token = String(r.stdout ?? '').trim();
  if (r.status !== 0) {
    const said = redact(tail(r.stderr), token);
    return { error: `${TOKEN_CMD} (${cmd}) exited ${r.status}${said ? `:\n${said}` : ', and said nothing'}`, cmd };
  }
  if (!token) return { error: `${TOKEN_CMD} (${cmd}) printed nothing: the vault entry is empty, or the command prints its value somewhere else`, cmd };
  if (/\s/.test(token)) return { error: `${TOKEN_CMD} (${cmd}) printed more than one word, and a token is one (what it printed is not shown)`, cmd };
  return { token, cmd };
}

/**
 * Asks GitHub `GET /repos/<fork>` with the token. A dropped connection, a
 * timeout or a 5xx is asked again, `tries` times in all, `waitMs` apart; then
 * it is an error, never an answer. Returns { answer: { status, headers, body } }
 * or { error }. `fetch` and `sleep` are injectable for the tests.
 */
export async function askGithub(fork, token, { fetch: doFetch = (...args) => globalThis.fetch(...args), tries = 3, waitMs = 5000, timeoutMs = 20_000, sleep = (ms) => new Promise((r) => setTimeout(r, ms)) } = {}) {
  const url = `https://api.github.com/repos/${fork}`;
  let last = '';
  for (let i = 1; i <= tries; i++) {
    try {
      const res = await doFetch(url, {
        headers: {
          Accept: 'application/vnd.github+json',
          Authorization: `Bearer ${token}`,
          'User-Agent': 'filex-release',
          'X-GitHub-Api-Version': '2022-11-28',
        },
        signal: AbortSignal.timeout(timeoutMs),
      });
      const headers = {};
      for (const k of HEADERS) {
        const v = res.headers?.get?.(k);
        if (v !== null && v !== undefined) headers[k] = v;
      }
      let body = null;
      try {
        body = await res.json();
      } catch {
        body = null;
      }
      if (res.status >= 500 && i < tries) {
        last = `${res.status}`;
        await sleep(waitMs);
        continue;
      }
      return { answer: { status: res.status, headers, body } };
    } catch (e) {
      const code = e?.cause?.code ? ` (${e.cause.code})` : '';
      last = redact(`${e?.name === 'TimeoutError' ? `no answer in ${Math.round(timeoutMs / 1000)} s` : (e?.message ?? e)}${code}`, token);
      if (i < tries) await sleep(waitMs);
    }
  }
  return { error: `could not ask GitHub whether the winget token works (GET ${url}, ${tries} tries, the last: ${last}): unknown does not pass - resume once api.github.com answers` };
}

/**
 * The preflight gate (plan.mjs → preflight): the token for `fork` (the
 * repository .goreleaser.yml's winget publisher pushes to) works, with more
 * than `days` days left. `repo` is where the secret lives. `readToken`,
 * `fetch`, `sleep` and `now` are injectable: the tests and the fixture plan
 * never touch the network or a vault.
 */
export function wingetTokenGate({ fork, repo, days = WINGET_TOKEN_DAYS, readToken = (ctx) => readWingetToken({ bash: ctx?.bash }), fetch, sleep, now = () => Date.now(), tries } = {}) {
  return {
    name: `winget: the token for ${fork} works, with more than ${days} days left`,
    // A server patch hands goreleaser no winget secret and opens no winget
    // pull request (#252, release-surfaces.patch): it is not asked then.
    surface: 'cli',
    check: async (ctx) => {
      const got = await readToken(ctx);
      if (!got || got.error || !got.token) {
        return { ok: false, detail: redact(got?.error ?? 'the winget token could not be read', got?.token) };
      }
      const token = got.token;
      const asked = await askGithub(fork, token, { fetch, sleep, tries });
      if (asked.error) return { ok: false, detail: redact(asked.error, token) };
      const v = wingetTokenVerdict(asked.answer, { fork, now: now(), days });
      const lines = [v.detail];
      if (v.ok) lines.push(`(the vault's copy, read by ${got.cmd ?? TOKEN_CMD}; the secret WINGET_TOKEN of ${repo} cannot be read back - it is kept the same value)`);
      if (v.renew) lines.push(...wingetTokenFix({ fork, repo, reader: got.cmd ?? '' }));
      return { ok: !!v.ok, detail: redact(lines.join('\n'), token) };
    },
  };
}
