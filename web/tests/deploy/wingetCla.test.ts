// Every winget pull request a release opens gets its CLA agreed by the release.
//
// ⚠ Why: microsoft-github-policy-service asks for the Microsoft CLA on EVERY
// pull request from the account that submits (brkfun), not once per account.
// It was agreed on #441070 (2026-09-25), and #441303, #441437, #441440,
// #441711 and #441715 still came up "Needs-CLA" and sat unreviewed until
// somebody commented by hand. The release opens both pull requests (the CLI
// through goreleaser, the desktop app through wingetcreate), so the release
// comments the agreement too, through .github/workflows/scripts/winget-cla.sh.
//
// That script finds the pull request by its EXACT title. A title changed on
// one side only would leave the step looking for a pull request that does not
// exist: a warning on the run, and the pull request unsigned again. So this
// pins both titles against what actually opens them.
//
// ⚠ The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR, and skipped when neither exists.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find(
  (d) => d && fs.existsSync(path.join(d, 'release.yml')),
);
const SCRIPT = 'scripts/winget-cla.sh';

/** release.yml without comment-only lines, CRLF or LF. */
const release = () =>
  fs
    .readFileSync(path.join(DIR!, 'release.yml'), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The titles winget-cla.sh is called with, in release.yml order. */
const claTitles = (text: string) =>
  [...text.matchAll(/winget-cla\.sh\s+"([^"]+)"/g)].map((m) => m[1]);

describe('the release agrees to the CLA on its winget pull requests', () => {
  it.runIf(!!DIR)('calls winget-cla.sh for the CLI and for the desktop app', () => {
    const titles = claTitles(release());
    expect(titles).toContain('New version: BRFTech.filex ${VER}');
    expect(titles).toContain('New version: BRFTech.filex-app ${GITHUB_REF_NAME#v}');
  });

  // ⚠ v0.47.0: the step searched for the CLI's pull request 3 s after
  // goreleaser opened it, the search index had not caught up, and the step
  // warned "No pull request" and never signed #441927's sibling. winget-cla.sh
  // already looks by exact title for five minutes, so nothing may gate it.
  it.runIf(!!DIR)('signs the CLI pull request without searching for it first', () => {
    const text = release();
    const at = text.indexOf('- name: Did the CLI reach winget and Homebrew?');
    expect(at).toBeGreaterThan(0);
    const body = text.slice(at, text.indexOf('winget-cla.sh', at));
    expect(body).not.toMatch(/gh pr list/);
  });

  it.runIf(!!DIR)("uses the CLI's title as goreleaser writes it", () => {
    const gr = fs.readFileSync(path.join(REPO, '.goreleaser.yml'), 'utf8');
    expect(gr).toMatch(/^\s*package_identifier:\s*BRFTech\.filex\s*$/m);
    expect(gr).toMatch(/^\s*commit_msg_template:\s*"New version: \{\{ \.PackageIdentifier \}\} \{\{ \.Version \}\}"\s*$/m);
  });

  it.runIf(!!DIR)("uses the desktop app's title as wingetcreate submits it", () => {
    const text = release();
    const submitted = /--prtitle\s+"New version: BRFTech\.filex-app \$ver"/.test(text);
    expect(submitted, 'wingetcreate submit --prtitle').toBe(true);
    // The pull request exists only once "Submit to winget" ran: the CLA step
    // comes after it, under the same condition.
    const submit = text.indexOf('- name: Submit to winget');
    const sign = text.indexOf('- name: Sign the CLA on the winget pull request');
    expect(submit).toBeGreaterThan(0);
    expect(sign).toBeGreaterThan(submit);
    const cond = (at: number) => /\n\s+if:\s*(.+)/.exec(text.slice(at))?.[1].trim();
    expect(cond(sign)).toBe(cond(submit));
  });

  it.runIf(!!DIR)('names the company the CLA was agreed for', () => {
    const text = release();
    const companies = [...text.matchAll(/CLA_COMPANY[=:]\s*"?([^"\n]+?)"?\s*(\\)?$/gm)].map((m) => m[1].trim());
    expect(companies).toHaveLength(2);
    for (const c of companies) expect(c).toBe('BRF TEKNOLOJİ LİMİTED ŞİRKETİ');
  });

  it.runIf(!!DIR)('posts the agreement the bot reads, and never fails the release', () => {
    const script = fs.readFileSync(path.join(DIR!, SCRIPT), 'utf8');
    expect(script).toContain('@microsoft-github-policy-service agree company=\\"$company\\"');
    // It waits for the bot's Needs-CLA label instead of commenting at once,
    // and does not agree twice.
    expect(script).toMatch(/Needs-CLA/);
    expect(script).toMatch(/grep -qF '@microsoft-github-policy-service agree'/);
    // A missed signature is a warning on the run, not a failed release.
    expect(script).not.toMatch(/^\s*set\s+-[a-z]*e/m);
    expect(script).not.toMatch(/\bexit\s+[1-9]/);
  });
});
