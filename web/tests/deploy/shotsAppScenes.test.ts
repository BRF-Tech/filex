// The screenshot scenes that need an APP build — what `pnpm shots` does with
// them, decided before anything is built (scripts/shots.mjs, the note at its
// top; scripts/lib/shot-scripts.mjs).
//
// ⚠⚠ Why this is a test. apps.mjs and signing.mjs photograph filex-sign and
// filex-convert, whose builds live in other repositories. Both CI jobs that
// run `pnpm shots` on a tag (GitLab `shots`, GitHub *Screenshots*) have
// neither build, and until this rule existed the v0.43.0 tag would have
// turned both red on "the sign app is not built" — at the release that first
// ships the scenes. So in CI those scenes are left out, loudly; locally a
// missing build is refused before an hour of building. Nothing else runs this
// logic before a tag does, which is the one moment it must not be wrong.
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  appScenesLeftOutBy,
  documentServerFor,
  findShotScripts,
  planAppScenes,
  scriptNeeds,
} from '../../../scripts/lib/shot-scripts.mjs';

const SHOTS_DIR = path.resolve(__dirname, '../../../e2e/shots');
const scripts = findShotScripts(SHOTS_DIR).scripts as string[];
const needs = new Map(scripts.map((f) => [f, scriptNeeds(SHOTS_DIR, f)]));
const present = () => ({ present: true, how: '' });
const absent = (app: string) => ({ present: false, how: `no ${app} build (looked in ../filex-${app})` });
const DS = documentServerFor({
  SHOTS_ONLYOFFICE_URL: 'http://office.example',
  SHOTS_ONLYOFFICE_JWT: 'secret',
  SHOTS_ONLYOFFICE_CALLBACK_HOST: 'filex',
});
const NO_DS = documentServerFor({});

describe('which shot scripts need an app build', () => {
  it('reads it off the findApp() calls — the scenes that photograph the apps', () => {
    expect(needs.get('apps.mjs')).toEqual({ apps: ['sign', 'convert'], set: 'apps', documentServer: false });
    expect(needs.get('signing.mjs')).toEqual({ apps: ['sign'], set: 'signing', documentServer: false });
    expect(needs.get('apppermissions.mjs')).toEqual({ apps: ['sign'], set: 'apppermissions', documentServer: false });
    // A language pack is an "app" with no module of its own (app-locations.mjs
    // → `dataOnly`), and it is found and left out by exactly the same rule.
    expect(needs.get('langpack.mjs')).toEqual({
      apps: ['sign', 'convert', 'lang-es', 'lang-de', 'lang-fr'],
      set: 'langpack',
      documentServer: false,
    });
    expect(needs.get('appearance.mjs')?.apps).toEqual([]);
    expect(needs.get('tags.mjs')?.apps).toEqual([]);
  });

  it('reads a document server off the documentServer() call - the scene that photographs ONLYOFFICE itself', () => {
    // 0.51: csvoffice.mjs draws ONLYOFFICE's spreadsheet, which only a real
    // document server can draw. Every other scene runs without one.
    expect(needs.get('csvoffice.mjs')).toEqual({ apps: [], set: 'csvoffice', documentServer: true });
    const withDS = [...needs].filter(([, n]) => n.documentServer).map(([f]) => f);
    expect(withDS).toEqual(['csvoffice.mjs']);
  });

  it('every script that needs one names its folder, so CI can leave it out without calling its pictures leftovers', () => {
    for (const [file, n] of needs) {
      if (n.apps.length || n.documentServer) expect(n.set, `${file} needs ${n.apps.join(', ') || 'a document server'} but has no const SET`).toBeTruthy();
    }
  });
});

describe('when the app scenes are left out', () => {
  it('in CI, unless --with-apps', () => {
    expect(appScenesLeftOutBy({ env: { CI: 'true' } })).toMatch(/CI/);
    expect(appScenesLeftOutBy({ env: { CI: '1' } })).not.toBe('');
    expect(appScenesLeftOutBy({ env: { CI: 'true' }, withApps: true })).toBe('');
  });

  it('never on a workstation unless asked', () => {
    expect(appScenesLeftOutBy({ env: {} })).toBe('');
    expect(appScenesLeftOutBy({ env: { CI: 'false' } })).toBe('');
    expect(appScenesLeftOutBy({ env: {}, withoutApps: true })).toBe('--without-apps');
  });
});

describe('the plan, before anything is built', () => {
  // ⚠ The names are spelled out, not derived: this is the list CI acts on at a
  // tag, and a test that recomputed it from the same source would agree with
  // whatever the source said. A NEW app scene belongs here in the same commit.
  // v0.43.0 added `langpack.mjs` — the Apps list with the language packs in
  // it, which needs both app builds and the three pack checkouts (Spanish,
  // German, French: the ones that ship. The Arabic pack on the maintainer's
  // machine is a right-to-left TEST fixture and is not in app-locations at
  // all, so no picture can find it). `pluginrequests.mjs` (install requests)
  // serves the sign build and the Spanish pack as the sources an agent asks
  // for. 0.49.0 added `apppermissions.mjs`: the signing app's own permission
  // ("Request signatures") on the role and person editors.
  // 0.51 added `csvoffice.mjs`, which needs no app but a real ONLYOFFICE
  // Document Server (SHOTS_ONLYOFFICE_URL / _JWT / _CALLBACK_HOST): left out and
  // refused by the same rule.
  it('left out: every app scene is excluded with the reason, nothing refused', () => {
    const { excluded, refused } = planAppScenes({ needs, withoutApps: true, locate: absent, documentServer: NO_DS });
    expect([...excluded.keys()].sort()).toEqual(['apppermissions.mjs', 'apps.mjs', 'csvoffice.mjs', 'langpack.mjs', 'pluginrequests.mjs', 'signing.mjs']);
    expect(excluded.get('csvoffice.mjs')).toBe('needs an ONLYOFFICE Document Server');
    expect(excluded.get('apppermissions.mjs')).toBe('needs the sign app build');
    expect(excluded.get('apps.mjs')).toBe('needs the sign + convert app builds');
    expect(excluded.get('langpack.mjs')).toBe(
      'needs the sign + convert + lang-es + lang-de + lang-fr app builds',
    );
    expect(excluded.get('pluginrequests.mjs')).toBe('needs the sign + lang-es app builds');
    expect(refused).toEqual([]);
  });

  it('locally with no builds: refused, naming every missing app per scene', () => {
    const { excluded, refused } = planAppScenes({ needs, withoutApps: false, locate: absent, documentServer: NO_DS });
    expect(excluded.size).toBe(0);
    expect(refused).toEqual([
      'e2e/shots/apppermissions.mjs needs the sign app: no sign build (looked in ../filex-sign).',
      'e2e/shots/apps.mjs needs the sign app: no sign build (looked in ../filex-sign).',
      'e2e/shots/apps.mjs needs the convert app: no convert build (looked in ../filex-convert).',
      `e2e/shots/csvoffice.mjs needs an ONLYOFFICE Document Server: ${NO_DS.how}.`,
      'e2e/shots/langpack.mjs needs the sign app: no sign build (looked in ../filex-sign).',
      'e2e/shots/langpack.mjs needs the convert app: no convert build (looked in ../filex-convert).',
      'e2e/shots/langpack.mjs needs the lang-es app: no lang-es build (looked in ../filex-lang-es).',
      'e2e/shots/langpack.mjs needs the lang-de app: no lang-de build (looked in ../filex-lang-de).',
      'e2e/shots/langpack.mjs needs the lang-fr app: no lang-fr build (looked in ../filex-lang-fr).',
      'e2e/shots/pluginrequests.mjs needs the sign app: no sign build (looked in ../filex-sign).',
      'e2e/shots/pluginrequests.mjs needs the lang-es app: no lang-es build (looked in ../filex-lang-es).',
      'e2e/shots/signing.mjs needs the sign app: no sign build (looked in ../filex-sign).',
    ]);
  });

  it('locally with the builds: every scene runs', () => {
    const { excluded, refused } = planAppScenes({ needs, withoutApps: false, locate: present, documentServer: DS });
    expect(excluded.size).toBe(0);
    expect(refused).toEqual([]);
  });

  it('locally with the builds and no document server: only the ONLYOFFICE scene is refused', () => {
    const { refused } = planAppScenes({ needs, withoutApps: false, locate: present, documentServer: NO_DS });
    expect(refused).toEqual([`e2e/shots/csvoffice.mjs needs an ONLYOFFICE Document Server: ${NO_DS.how}.`]);
  });

  it('a document server is named by all three variables or not at all', () => {
    expect(DS).toMatchObject({ present: true, url: 'http://office.example', jwt: 'secret', callbackHost: 'filex' });
    const two = documentServerFor({ SHOTS_ONLYOFFICE_URL: 'http://office.example', SHOTS_ONLYOFFICE_JWT: 'secret' });
    expect(two.present).toBe(false);
    expect(two.how).toMatch(/^SHOTS_ONLYOFFICE_CALLBACK_HOST not set/);
    expect(NO_DS.how).toMatch(/^SHOTS_ONLYOFFICE_URL, SHOTS_ONLYOFFICE_JWT, SHOTS_ONLYOFFICE_CALLBACK_HOST not set/);
  });

  it('a scene with no folder of its own cannot be left out quietly', () => {
    const { excluded, refused } = planAppScenes({
      needs: new Map([['x.mjs', { apps: ['sign'], set: null }]]),
      withoutApps: true,
      locate: present,
    });
    expect(excluded.size).toBe(0);
    expect(refused[0]).toMatch(/x\.mjs needs sign .* names no `const SET`/);
  });
});
