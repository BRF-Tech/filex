// The winget token, asked before anything is written (task #290).
//
// ⚠⚠ Why. v0.57.6 (2026-10-26): the repository secret WINGET_TOKEN - the
// classic token goreleaser's winget publisher pushes to the fork with - had
// run out (it was made with GitHub's 30-day default). The tag run published
// the GitHub Release and the CLI, then died at winget on "401 Bad
// credentials"; every desktop, Snap Store, Microsoft Store and AUR job behind
// it was skipped, and releases/latest answered 404 for the desktop installers
// until the token was renewed and the Release run again. So the preflight
// asks GitHub with the vault's copy (GitHub never gives a secret's value
// back), and every way that question can go wrong reads red, never green:
//   - a refused token (401), one that may not read or push to the fork, one
//     without public_repo, one that is not a classic token;
//   - 14 days or less left - red, with the fix: a person renews it, and the
//     resume asks again (nothing to confirm: an --ack would let it through
//     unchanged);
//   - GitHub not answering (the network, a 5xx, a rate limit) is "unknown",
//     and unknown does not pass;
//   - no command to read the token with is said, not skipped.
// And the token itself is never printed: not in a detail, not in an error,
// whatever echoed it. The release as a whole, with this gate red and then
// green on a resume, is driven end to end in releaseCli.test.ts.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { WINGET_TOKEN_DAYS, WINGET_TOKEN_NEW_URL, githubTokenExpiry, wingetTokenFix, wingetTokenVerdict } from '../../../scripts/release/checks.mjs';
import { findBash } from '../../../scripts/release/engine.mjs';
import plan, { WINGET_FORK } from '../../../scripts/release/plan.mjs';
import { publishes } from '../../../scripts/release/surfaces.mjs';
import { TOKEN_CMD, askGithub, readWingetToken, redact, wingetTokenGate } from '../../../scripts/release/winget-token.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const FORK = 'example/winget-pkgs';
const SECRET_REPO = 'example/filex';
const TOKEN = 'test-token-0123456789-not-real';
const NOW = Date.parse('2026-10-26T12:00:00Z');
const DAY = 86_400_000;

/** GitHub's way of writing a token's expiry: "2027-10-25 09:00:00 UTC". */
const githubDate = (ms: number) => new Date(ms).toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC');

type Answer = { status?: number; headers?: Record<string, string>; body?: unknown; throws?: Error };

/** A stand-in for api.github.com: the answers in order (the last repeats); every call recorded. */
function fakeGithub(...answers: Answer[]) {
  const calls: Array<{ url: string; headers: Record<string, string>; hasSignal: boolean }> = [];
  const fetch = async (url: string, init: { headers: Record<string, string>; signal?: AbortSignal }) => {
    calls.push({ url, headers: init.headers, hasSignal: !!init.signal });
    const a = answers[Math.min(calls.length - 1, answers.length - 1)];
    if (a.throws) throw a.throws;
    return new Response(a.body === undefined ? '{}' : JSON.stringify(a.body), { status: a.status ?? 200, headers: a.headers ?? {} });
  };
  return { fetch, calls };
}

const GOOD = { 'x-oauth-scopes': 'public_repo' };
const REPO_BODY = { full_name: FORK, permissions: { admin: true, push: true, pull: true } };

function gate(github: ReturnType<typeof fakeGithub>, read: () => unknown = () => ({ token: TOKEN, cmd: 'vault-read winget' })) {
  const sleeps: number[] = [];
  const g = wingetTokenGate({
    fork: FORK,
    repo: SECRET_REPO,
    readToken: read as never,
    fetch: github.fetch as never,
    sleep: async (ms: number) => {
      sleeps.push(ms);
    },
    now: () => NOW,
  });
  return { g, sleeps, check: () => g.check({}) as Promise<{ ok: boolean; detail: string }> };
}

describe('githubTokenExpiry: the header GitHub sends with a token that expires', () => {
  it('reads GitHub\'s UTC form, a numeric offset and an ISO time', () => {
    expect(githubTokenExpiry('2027-10-25 09:00:00 UTC')).toBe(Date.parse('2027-10-25T09:00:00Z'));
    expect(githubTokenExpiry('2027-10-25 12:00:00 +0300')).toBe(Date.parse('2027-10-25T09:00:00Z'));
    expect(githubTokenExpiry('2027-10-25 01:00:00 -0800')).toBe(Date.parse('2027-10-25T09:00:00Z'));
    expect(githubTokenExpiry('2027-10-25T09:00:00Z')).toBe(Date.parse('2027-10-25T09:00:00Z'));
  });

  it('answers null for what does not read as a date, never a guess', () => {
    for (const v of ['', 'never', '2027-10-25', 'next tuesday', '2027-13-45 99:99:99 UTC', undefined, null]) expect(githubTokenExpiry(v), String(v)).toBeNull();
  });
});

describe('wingetTokenVerdict: what GitHub answered, judged', () => {
  const verdict = (a: Answer) => wingetTokenVerdict({ status: a.status ?? 200, headers: a.headers ?? {}, body: a.body ?? null }, { fork: FORK, now: NOW });

  it('green: a classic token with public_repo (or repo, which holds it), push to the fork, no expiry', () => {
    const v = verdict({ headers: GOOD, body: REPO_BODY });
    expect(v).toMatchObject({ ok: true });
    expect(v.detail).toBe(`public_repo, may push to ${FORK}, no expiry`);
    expect(verdict({ headers: { 'x-oauth-scopes': 'repo, workflow' }, body: REPO_BODY }).ok).toBe(true);
  });

  it('green with more than 14 days left, and says when it runs out', () => {
    const v = verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 364 * DAY) }, body: REPO_BODY });
    expect(v.ok).toBe(true);
    expect(v.detail).toBe(`public_repo, may push to ${FORK}, expires on 2027-10-25 (in 364 days)`);
    expect(verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 15 * DAY) }, body: REPO_BODY }).ok).toBe(true);
  });

  it('red at 14 days or less left (0.57.6: a 30-day token ran out between two releases)', () => {
    expect(WINGET_TOKEN_DAYS).toBe(14);
    const at14 = verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 14 * DAY) }, body: REPO_BODY });
    expect(at14).toMatchObject({ ok: false, renew: true });
    expect(at14.detail).toContain('the winget token expires on 2026-11-09, in 14 day(s): a release wants more than 14 days left on it');
    const soon = verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 3 * DAY + 3600_000) }, body: REPO_BODY });
    expect(soon.detail).toContain('in 3 day(s)');
    const hours = verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 5 * 3600_000) }, body: REPO_BODY });
    expect(hours.detail).toContain('in less than a day');
    const gone = verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW - DAY) }, body: REPO_BODY });
    expect(gone).toMatchObject({ ok: false, renew: true, detail: 'the winget token expired on 2026-10-25' });
  });

  it('red on an expiry it cannot read: unknown does not pass', () => {
    const v = verdict({ headers: { ...GOOD, 'github-authentication-token-expiration': 'soon' }, body: REPO_BODY });
    expect(v.ok).toBe(false);
    expect(v.detail).toContain('"soon", which does not read as a date');
  });

  it('red, renew: 401 (ran out or revoked), 403, 404 (another account, or no fork)', () => {
    const r401 = verdict({ status: 401, body: { message: 'Bad credentials' } });
    expect(r401).toMatchObject({ ok: false, renew: true });
    expect(r401.detail).toContain('GitHub refuses the winget token: 401 "Bad credentials" - it ran out or was revoked');
    expect(verdict({ status: 403, body: { message: 'Resource not accessible by personal access token' } })).toMatchObject({ ok: false, renew: true });
    const r404 = verdict({ status: 404, body: { message: 'Not Found' } });
    expect(r404).toMatchObject({ ok: false, renew: true });
    expect(r404.detail).toContain(`${FORK} is not there for the winget token (404 "Not Found")`);
  });

  it('red, renew: no classic token, no public_repo, no push to the fork', () => {
    const fine = verdict({ headers: {}, body: REPO_BODY });
    expect(fine).toMatchObject({ ok: false, renew: true });
    expect(fine.detail).toContain('no classic token');
    const scope = verdict({ headers: { 'x-oauth-scopes': 'gist, read:org' }, body: REPO_BODY });
    expect(scope).toMatchObject({ ok: false, renew: true });
    expect(scope.detail).toContain("the winget token's scopes are gist, read:org: the winget pull request needs public_repo");
    expect(verdict({ headers: { 'x-oauth-scopes': '' }, body: REPO_BODY }).detail).toContain('scopes are none');
    const other = verdict({ headers: GOOD, body: { full_name: FORK, permissions: { push: false, pull: true } } });
    expect(other).toMatchObject({ ok: false, renew: true });
    expect(other.detail).toContain(`may not push to ${FORK}`);
  });

  it('red, unknown (not renew): a 5xx, the rate limit, anything else GitHub answers', () => {
    expect(verdict({ status: 502 })).toMatchObject({ ok: false, unknown: true });
    const limited = verdict({ status: 403, headers: { 'x-ratelimit-remaining': '0', 'x-ratelimit-reset': '1793000000' }, body: { message: 'API rate limit exceeded' } });
    expect(limited).toMatchObject({ ok: false, unknown: true });
    expect(limited.renew).toBeUndefined();
    expect(limited.detail).toContain('the limit resets at');
    expect(verdict({ status: 429, body: { message: 'secondary rate limit' } })).toMatchObject({ ok: false, unknown: true });
    const odd = verdict({ status: 301 });
    expect(odd.ok).toBe(false);
    expect(odd.detail).toContain('unknown does not pass');
  });
});

describe('wingetTokenFix: the renewal, said exactly', () => {
  it('names the classic token page with public_repo, the vault entry and the repository secret', () => {
    expect(WINGET_TOKEN_NEW_URL).toBe('https://github.com/settings/tokens/new?scopes=public_repo&description=filex%20winget%20releases');
    const lines = wingetTokenFix({ fork: FORK, repo: SECRET_REPO }).join('\n');
    expect(lines).toContain(`the fix, by the account that owns ${FORK}:`);
    expect(lines).toContain(WINGET_TOKEN_NEW_URL);
    expect(lines).toContain('long expiry');
    expect(lines).toContain('the vault entry FILEX_WINGET_TOKEN_CMD reads');
    expect(lines).toContain(`gh secret set WINGET_TOKEN -R ${SECRET_REPO}`);
  });

  it('pipes the vault reader into gh secret set when it is known, so the value is never on screen', () => {
    const lines = wingetTokenFix({ fork: FORK, repo: SECRET_REPO, reader: 'vault-read winget' }).join('\n');
    expect(lines).toContain(`vault-read winget | gh secret set WINGET_TOKEN -R ${SECRET_REPO}`);
  });
});

describe('the preflight gate: the vault\'s copy asked of GitHub, never printed', () => {
  it('green asks GET /repos/<fork> once, with the token as a bearer and a time limit', async () => {
    const gh = fakeGithub({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 364 * DAY) }, body: REPO_BODY });
    const { g, check } = gate(gh);
    expect(g.name).toBe(`winget: the token for ${FORK} works, with more than 14 days left`);
    const r = await check();
    expect(r.ok).toBe(true);
    expect(r.detail.split('\n')[0]).toBe(`public_repo, may push to ${FORK}, expires on 2027-10-25 (in 364 days)`);
    expect(r.detail).toContain(`the secret WINGET_TOKEN of ${SECRET_REPO} cannot be read back`);
    expect(gh.calls).toHaveLength(1);
    expect(gh.calls[0].url).toBe(`https://api.github.com/repos/${FORK}`);
    expect(gh.calls[0].headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(gh.calls[0].headers['User-Agent']).toBeTruthy();
    expect(gh.calls[0].hasSignal).toBe(true);
    expect(r.detail).not.toContain(TOKEN);
  });

  it('red on 401 with the fix, the reader piped into gh secret set, and no token', async () => {
    const { check } = gate(fakeGithub({ status: 401, body: { message: 'Bad credentials' } }));
    const r = await check();
    expect(r.ok).toBe(false);
    expect(r.detail).toContain('401 "Bad credentials"');
    expect(r.detail).toContain(WINGET_TOKEN_NEW_URL);
    expect(r.detail).toContain(`vault-read winget | gh secret set WINGET_TOKEN -R ${SECRET_REPO}`);
    expect(r.detail).not.toContain(TOKEN);
  });

  it('red at 10 days left, with the fix', async () => {
    const { check } = gate(fakeGithub({ headers: { ...GOOD, 'github-authentication-token-expiration': githubDate(NOW + 10 * DAY) }, body: REPO_BODY }));
    const r = await check();
    expect(r.ok).toBe(false);
    expect(r.detail).toContain('in 10 day(s)');
    expect(r.detail).toContain(`gh secret set WINGET_TOKEN -R ${SECRET_REPO}`);
  });

  it('GitHub not answering is asked again, then red - never a silent pass', async () => {
    const down = fakeGithub({ throws: new TypeError('fetch failed', { cause: { code: 'ECONNRESET' } }) });
    const { check, sleeps } = gate(down);
    const r = await check();
    expect(r.ok).toBe(false);
    expect(r.detail).toContain(`could not ask GitHub whether the winget token works (GET https://api.github.com/repos/${FORK}, 3 tries, the last: fetch failed (ECONNRESET))`);
    expect(r.detail).toContain('unknown does not pass');
    expect(r.detail).not.toContain(WINGET_TOKEN_NEW_URL);
    expect(down.calls).toHaveLength(3);
    expect(sleeps).toHaveLength(2);
  });

  it('a 5xx is asked again too; the answer after it is the one judged', async () => {
    const flaky = fakeGithub({ status: 502, body: { message: 'Server Error' } }, { headers: GOOD, body: REPO_BODY });
    const { check } = gate(flaky);
    const r = await check();
    expect(r.ok).toBe(true);
    expect(flaky.calls).toHaveLength(2);
    const still = fakeGithub({ status: 503 });
    const s = await gate(still).check();
    expect(s.ok).toBe(false);
    expect(s.detail).toContain('GitHub did not say whether the winget token works (503');
    expect(still.calls).toHaveLength(3);
  });

  it('a time-out is said as one', async () => {
    const slow = fakeGithub({ throws: Object.assign(new Error('The operation was aborted due to timeout'), { name: 'TimeoutError' }) });
    const r = await gate(slow).check();
    expect(r.ok).toBe(false);
    expect(r.detail).toContain('no answer in 20 s');
  });

  it('takes the token out of whatever echoes it: an error, GitHub\'s message, the reader', async () => {
    const echo = fakeGithub({ throws: new Error(`bad header Bearer ${TOKEN}`) });
    expect((await gate(echo).check()).detail).not.toContain(TOKEN);
    const said = fakeGithub({ status: 401, body: { message: `token ${TOKEN} is not valid` } });
    const r = await gate(said).check();
    expect(r.detail).toContain('[the token]');
    expect(r.detail).not.toContain(TOKEN);
    const reader = await gate(fakeGithub({}), () => ({ error: `vault said ${TOKEN}`, token: TOKEN })).check();
    expect(reader.ok).toBe(false);
    expect(reader.detail).not.toContain(TOKEN);
  });

  it('red with the reader\'s own words when the token cannot be read, and GitHub is not asked', async () => {
    const gh = fakeGithub({ headers: GOOD, body: REPO_BODY });
    const r = await gate(gh, () => ({ error: `${TOKEN_CMD} (vault-read winget) exited 3:\nno such entry` })).check();
    expect(r.ok).toBe(false);
    expect(r.detail).toContain('exited 3');
    expect(r.detail).toContain('no such entry');
    expect(gh.calls).toHaveLength(0);
  });

  it('askGithub keeps only the headers the verdict reads', async () => {
    const gh = fakeGithub({ headers: { ...GOOD, 'x-ratelimit-remaining': '4999', 'x-github-request-id': 'ABCD:1234', etag: 'W/"x"' }, body: REPO_BODY });
    const r = await askGithub(FORK, TOKEN, { fetch: gh.fetch as never, sleep: async () => {} });
    expect(r.answer?.headers).toEqual({ 'x-oauth-scopes': 'public_repo', 'x-ratelimit-remaining': '4999' });
  });

  it('redact leaves a text alone without a token, and takes every occurrence out', () => {
    expect(redact('nothing here', '')).toBe('nothing here');
    expect(redact(`a ${TOKEN} b ${TOKEN}`, TOKEN)).toBe('a [the token] b [the token]');
  });
});

describe('readWingetToken: FILEX_WINGET_TOKEN_CMD, the vault reader', () => {
  const settings = (env: Record<string, string>, file = '') => () => ({ env, file });
  const runner = (status: number, stdout: string, stderr = '') => {
    const calls: Array<{ bin: string; args: string[] }> = [];
    const fn = (bin: string, args: string[]) => {
      calls.push({ bin, args });
      return { status, stdout, stderr };
    };
    return { fn, calls };
  };

  it('not set: says which setting, where it was looked for, and where it is described', () => {
    const r = readWingetToken({ bash: 'bash', settings: settings({}) as never });
    expect(r.token).toBeUndefined();
    expect(r.error).toContain('FILEX_WINGET_TOKEN_CMD is not set (the environment, or ~/.config/filex-train.env (there is none here))');
    expect(r.error).toContain('scripts/train/train.env.example');
    expect(readWingetToken({ bash: 'bash', settings: settings({}, '/home/you/.config/filex-train.env') as never }).error).toContain('or /home/you/.config/filex-train.env)');
  });

  it('runs the command in bash and takes its standard output, trimmed', () => {
    const run = runner(0, `${TOKEN}\n`);
    const r = readWingetToken({ bash: '/bin/bash', settings: settings({ [TOKEN_CMD]: 'vault-read --name "winget token"' }) as never, runner: run.fn as never });
    expect(r).toEqual({ token: TOKEN, cmd: 'vault-read --name "winget token"' });
    expect(run.calls).toEqual([{ bin: '/bin/bash', args: ['-c', 'vault-read --name "winget token"'] }]);
  });

  it('a reader that fails, prints nothing or prints more than a token is red, and its output is not shown', () => {
    const cmd = { [TOKEN_CMD]: 'vault-read winget' };
    const failed = readWingetToken({ bash: 'bash', settings: settings(cmd) as never, runner: runner(3, TOKEN, `no access to ${TOKEN}\nask for a grant`).fn as never });
    expect(failed.error).toContain('FILEX_WINGET_TOKEN_CMD (vault-read winget) exited 3:');
    expect(failed.error).toContain('ask for a grant');
    expect(failed.error).not.toContain(TOKEN);
    expect(readWingetToken({ bash: 'bash', settings: settings(cmd) as never, runner: runner(0, '  \n').fn as never }).error).toContain('printed nothing');
    const two = readWingetToken({ bash: 'bash', settings: settings(cmd) as never, runner: runner(0, `${TOKEN} ${TOKEN}`).fn as never });
    expect(two.error).toContain('printed more than one word');
    expect(two.error).not.toContain(TOKEN);
  });

  it('no bash, unreadable settings: red, said', () => {
    expect(readWingetToken({ bash: null as never, settings: settings({ [TOKEN_CMD]: 'x' }) as never }).error).toContain('no bash');
    const broken = readWingetToken({ bash: 'bash', settings: (() => { throw new Error('not KEY=VALUE: oops'); }) as never });
    expect(broken.error).toContain('not KEY=VALUE: oops');
  });

  const bash = findBash();
  it.runIf(!!bash)('through a real bash: what the command prints is the token, and nothing reaches a terminal', () => {
    const r = readWingetToken({ bash, settings: settings({ [TOKEN_CMD]: `printf '%s\\n' ${TOKEN}` }) as never });
    expect(r).toEqual({ token: TOKEN, cmd: `printf '%s\\n' ${TOKEN}` });
    const isatty = readWingetToken({ bash, settings: settings({ [TOKEN_CMD]: '[ -t 1 ] && echo tty || echo pipe' }) as never });
    expect(isatty.token).toBe('pipe');
  });
});

describe("this repository's plan asks it at the preflight", () => {
  const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });

  it('the preflight holds the winget token gate, for the fork .goreleaser.yml publishes to', () => {
    const g = (p.preflight as Array<{ name: string; surface?: string; check?: unknown }>).find((x) => x.name.startsWith('winget: the token for '));
    expect(g, 'the preflight lost the winget token gate (#290: 0.57.6 died at winget on an expired token)').toBeTruthy();
    expect(g!.name).toBe(`winget: the token for ${WINGET_FORK} works, with more than 14 days left`);
    expect(typeof g!.check).toBe('function');
    const yml = fs.readFileSync(path.join(REPO, '.goreleaser.yml'), 'utf8');
    const winget = yml.slice(yml.indexOf('\nwinget:'));
    const repoBlock = winget.slice(winget.indexOf('    repository:'));
    const owner = /\n\s+owner:\s*(\S+)/.exec(repoBlock)?.[1];
    const name = /\n\s+name:\s*(\S+)/.exec(repoBlock)?.[1];
    expect(`${owner}/${name}`, '.goreleaser.yml publishes winget from another fork than the gate asks about').toBe(WINGET_FORK);
    expect(yml).toContain("token: '{{ .Env.WINGET_TOKEN }}'");
  });

  it('a server patch does not ask it: it hands goreleaser no winget secret (#252)', () => {
    const g = (p.preflight as Array<{ name: string; surface?: string }>)[0];
    expect(g.surface).toBe('cli');
    expect(publishes(['server', 'docs'], g.surface)).toBe(false);
    expect(publishes(['server', 'cli', 'ui', 'desktop', 'docs'], g.surface)).toBe(true);
  });

  it('the settings example and the release docs say how the token is read', () => {
    expect(fs.readFileSync(path.join(REPO, 'scripts', 'train', 'train.env.example'), 'utf8')).toContain(`# ${TOKEN_CMD}=`);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toContain(TOKEN_CMD);
    expect(contributing).toContain(WINGET_TOKEN_NEW_URL);
  });
});
