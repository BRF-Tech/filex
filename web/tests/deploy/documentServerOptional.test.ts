/**
 * The default install runs no ONLYOFFICE Document Server, and the optional one
 * is pinned to an exact version.
 *
 * ⚠ Why (the owner's ruling, 2026-10-15, task 268): office documents are
 * edited by the Office editor app, in the browser, with no Document Server. A
 * Document Server stays supported as an option (today it still draws office
 * thumbnails and runs the apps' office conversions), but no deploy file
 * starts one unless asked: in a compose file it sits behind a profile, the
 * full stack's default COMPOSE_PROFILES leaves it out, the Helm chart wires
 * none, and no store listing ships one.
 *
 * And never `latest`: on 2026-10-12 Docs 10.0 became `latest` and saved a
 * semicolon CSV with semicolons where filex expected commas (task 255) -
 * every install that pulled `latest` changed engine without a word. The
 * optional piece names the version filex is tested against (9.4.0.1); an
 * operator who wants another sets ONLYOFFICE_IMAGE on purpose.
 *
 * Read as text, like helmValuesRoots: no YAML parser in the web tests, and
 * the rule is about lines a person reads anyway.
 */
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const ROOT = path.resolve(__dirname, '../../..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');

/** The compose files a person runs as they ship (the private demo deploy is not one). */
const COMPOSE = [
  'docker-compose.yml',
  'deploy/compose/docker-compose.full.yml',
  'deploy/compose/docker-compose.minimal.yml',
  'deploy/compose/docker-compose.multi-tenant.yml',
  'deploy/casaos/docker-compose.yml',
  'deploy/umbrel/filex/docker-compose.yml',
];

/** Store listings in JSON or XML: no Document Server in them at all. */
const LISTINGS = ['deploy/runtipi/filex/docker-compose.json', 'deploy/portainer/template.json', 'deploy/unraid/filex.xml'];

/** An exact Document Server version: 9.4.0.1, 9.4.0 - never latest, never a bare major. */
const EXACT = /^\d+\.\d+\.\d+(\.\d+)?$/;

/** The lines that are not comments, each with its line number. */
function codeLines(src: string): Array<{ n: number; text: string }> {
  return src
    .split('\n')
    .map((text, i) => ({ n: i + 1, text }))
    .filter((l) => l.text.trim() !== '' && !l.text.trim().startsWith('#'));
}

/** The service a line belongs to: its header (two spaces in, under `services:`) and its own lines. */
function serviceBlock(lines: Array<{ n: number; text: string }>, at: number): { name: string; body: string[] } {
  let start = at;
  while (start > 0 && !/^ {2}[\w.-]+:\s*$/.test(lines[start]!.text)) start--;
  const name = lines[start]!.text.trim().replace(/:$/, '');
  const body: string[] = [];
  for (let i = start + 1; i < lines.length && /^ {3,}/.test(lines[i]!.text); i++) body.push(lines[i]!.text);
  return { name, body };
}

/** The image a compose line names, a `${VAR:-default}` read as its default. */
function imageOf(line: string): string {
  const v = line.replace(/^\s*image:\s*/, '').trim().replace(/^["']|["']$/g, '');
  const m = v.match(/^\$\{[A-Z0-9_]+:-([^}]+)\}$/);
  return m ? m[1]! : v;
}

describe('no default install runs an ONLYOFFICE Document Server', () => {
  it.each(COMPOSE)('%s: a Document Server is only ever behind a profile', (file) => {
    const lines = codeLines(read(file));
    for (let i = 0; i < lines.length; i++) {
      if (!/^\s*image:.*documentserver/.test(lines[i]!.text)) continue;
      const svc = serviceBlock(lines, i);
      expect(
        svc.body.some((l) => /^\s*profiles:\s*\[\s*["']?[\w-]+/.test(l)),
        `${file}:${lines[i]!.n} service "${svc.name}" runs a Document Server without a profile: a plain "up" would start it`,
      ).toBe(true);
    }
  });

  it('the full stack leaves the onlyoffice profile out of its default COMPOSE_PROFILES', () => {
    const env = codeLines(read('deploy/compose/.env.example')).find((l) => /^COMPOSE_PROFILES=/.test(l.text));
    expect(env, 'deploy/compose/.env.example names its default profiles').toBeTruthy();
    const profiles = env!.text.replace(/^COMPOSE_PROFILES=/, '').split('#')[0]!.split(',').map((p) => p.trim());
    expect(profiles).not.toContain('onlyoffice');
  });

  it.each(LISTINGS)('%s ships no Document Server', (file) => {
    expect(read(file)).not.toMatch(/documentserver/i);
  });

  it('the Helm chart wires none by default, in values.yaml and values-full.yaml alike', () => {
    for (const file of ['deploy/helm/filex/values.yaml', 'deploy/helm/filex/values-full.yaml']) {
      const block = read(file).match(/^onlyoffice:\n((?:[ \t]+.*\n|[ \t]*#.*\n)+)/m);
      expect(block, `${file} declares an onlyoffice block`).toBeTruthy();
      expect(block![1], `${file}: onlyoffice.enabled`).toMatch(/^\s+enabled:\s*false\b/m);
    }
    const templates = path.join(ROOT, 'deploy/helm/filex/templates');
    for (const f of fs.readdirSync(templates)) {
      expect(fs.readFileSync(path.join(templates, f), 'utf8'), `templates/${f} deploys no Document Server`).not.toMatch(/documentserver/i);
    }
  });

  it('every store listing points to the office editor app for office files', () => {
    for (const file of [
      'deploy/umbrel/filex/umbrel-app.yml',
      'deploy/casaos/docker-compose.yml',
      'deploy/runtipi/filex/config.json',
      'deploy/runtipi/filex/metadata/description.md',
      'deploy/portainer/template.json',
      'deploy/unraid/filex.xml',
    ]) {
      expect(read(file).replace(/\s+/g, ' '), file).toMatch(/install the office editor app/i);
    }
  });
});

describe('the optional Document Server is pinned to an exact version', () => {
  it.each(COMPOSE)('%s: never latest', (file) => {
    for (const l of codeLines(read(file))) {
      if (!/^\s*image:.*documentserver/.test(l.text)) continue;
      const image = imageOf(l.text);
      const tag = image.includes(':') ? image.slice(image.lastIndexOf(':') + 1) : '';
      expect(tag, `${file}:${l.n} ${image}`).toMatch(EXACT);
    }
  });

  it('the docs and the deploy files never tell anybody to run latest', () => {
    const tracked = execFileSync('git', ['-C', ROOT, 'ls-files', '-z', '--', 'README.md', 'docs', 'deploy', 'docker-compose.yml', '.env.example'], {
      encoding: 'utf8',
    })
      .split('\0')
      .filter((f) => f && f !== 'deploy/demo-fm.example.com.compose.yml' && fs.existsSync(path.join(ROOT, f)));
    expect(tracked.length, 'a scan of nothing passes everything').toBeGreaterThan(20);
    const bad: string[] = [];
    for (const f of tracked) {
      read(f)
        .split('\n')
        .forEach((line, i) => {
          // An image reference, not a path inside the container
          // (/var/www/onlyoffice/documentserver): a tag, when there is one,
          // is an exact version.
          for (const m of line.matchAll(/(?<![\w/.-])onlyoffice\/documentserver(?::([\w.-]+))?/g)) {
            if (m[1] !== undefined && !EXACT.test(m[1])) bad.push(`${f}:${i + 1}: ${line.trim()}`);
          }
        });
    }
    expect(bad).toEqual([]);
  });
});
