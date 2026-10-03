// The unit-test harness itself (tests/setup.ts): no test reaches the network,
// and every test ends with its pages taken down in the one safe order.
//
// Task #127: the v0.49.0 release run failed with every test green. A page a
// test never mocked sent a REAL request (happy-dom → localhost:3000); its
// ECONNREFUSED landed in the next test, after the page's <body> had been
// wiped under it while it was still mounted, and the page re-drew into DOM
// that was gone — an unhandled rejection, exit 1. These tests hold the two
// guards that make that impossible: take either out of setup.ts, or write the
// old pattern back into a test, and this file goes red.
//
// ⚠ This file does NOT import tests/helpers/noNetwork at the top: importing it
// installs the guard, and then these tests would pass even if setup.ts had
// stopped installing it. The first test asks the globals alone.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import { defineComponent, h, onUnmounted } from 'vue';
import { flushPromises, mount } from '@vue/test-utils';

const TESTS = path.resolve(__dirname, '..');
const guard = () => import('../helpers/noNetwork');

/** The refusals a call produced, taken off the list so this test stays green. */
async function refusals(): Promise<string[]> {
  return (await guard()).takeNetworkHits().map((e) => e.message);
}

describe('a unit test never reaches the network', () => {
  it('fetch: refused at once by setup.ts, with the address and what to do', async () => {
    const err = await fetch('/api/roles/overrides').then(
      () => null,
      (e: Error) => e,
    );
    expect(err?.name, 'setup.ts installs the guard before any test file runs').toBe('NetworkBlockedError');
    expect(err?.message).toContain('GET http://localhost:3000/api/roles/overrides');
    expect(err?.message).toContain('mock it');
    expect(err?.message).toContain('in "a unit test never reaches the network > fetch');
    expect(await refusals()).toHaveLength(1);
  });

  it('XMLHttpRequest (every @/api/* call through axios): refused', async () => {
    const x = new XMLHttpRequest();
    x.open('put', '/api/me/prefs?surface=web');
    expect(() => x.send('{}')).toThrow(/PUT http:\/\/localhost:3000\/api\/me\/prefs\?surface=web/);
    expect(await refusals()).toHaveLength(1);
  });

  it("node's http.request underneath (how happy-dom loads what a page asks for): refused", async () => {
    expect(() => http.request('http://localhost:3000/drawio/?embed=1')).toThrow(/node http\.request: GET http:\/\/localhost:3000\/drawio/);
    expect(await refusals()).toHaveLength(1);
  });

  it.skipIf(typeof WebSocket !== 'function')('WebSocket: refused', async () => {
    expect(() => new WebSocket('ws://localhost:3000/api/events')).toThrow(/WebSocket: OPEN ws:\/\/localhost:3000\/api\/events/);
    expect(await refusals()).toHaveLength(1);
  });

  it('a refusal the code under test swallows still fails the test', async () => {
    await fetch('/api/public/branding').catch(() => undefined);
    // setup.ts's afterEach does exactly this after every test:
    const { failOnNetworkHits } = await guard();
    expect(() => failOnNetworkHits('(this test)')).toThrow(/1 request\(s\) left a unit test \(this test\):\n {2}- \[no-network\] fetch: GET http:\/\/localhost:3000\/api\/public\/branding/);
    expect(await refusals()).toEqual([]);
  });

  it('data: and blob: never leave the process, and pass', async () => {
    expect(await (await fetch('data:text/plain,hello')).text()).toBe('hello');
    expect(await refusals()).toEqual([]);
  });

  it('an <iframe src> is a blank frame at that address: a window to talk to, a load, no request', async () => {
    const f = document.createElement('iframe');
    const loaded = new Promise<void>((r) => f.addEventListener('load', () => r()));
    f.src = 'https://draw.example/?embed=1';
    document.body.appendChild(f);
    await loaded;
    expect(f.contentWindow).not.toBeNull();
    await flushPromises();
    expect(await refusals()).toEqual([]);
  });

  it('opting in needs a reason', async () => {
    const { allowNetwork } = await guard();
    expect(() => allowNetwork('  ')).toThrow(/say why/);
  });
});

// Tests in order: the first leaves a page mounted and a node under <body>,
// the second finds the page unmounted and <body> empty — without the first
// test doing anything about it.
describe('every test ends with its pages taken down, then <body> emptied', () => {
  let unmounted = 0;
  const Page = defineComponent({
    setup() {
      onUnmounted(() => void unmounted++);
      return () => h('p', { class: 'harness-page' }, 'page');
    },
  });

  it('leaves a page mounted', () => {
    mount(Page, { attachTo: document.body });
    document.body.appendChild(document.createElement('aside'));
    expect(document.querySelectorAll('.harness-page')).toHaveLength(1);
  });

  it('…and the next test starts with it unmounted and an empty <body>', () => {
    expect(unmounted).toBe(1);
    expect(document.body.innerHTML).toBe('');
  });

  // Vue says "Cannot unmount an app that is not mounted" when an app is
  // unmounted twice; the harness must not add that noise to a test that
  // took its own page down.
  const warnings: string[] = [];
  const realWarn = console.warn;
  it('a page the test unmounted itself…', () => {
    console.warn = (...args: unknown[]) => {
      warnings.push(args.map(String).join(' '));
      realWarn(...args);
    };
    mount(Page, { attachTo: document.body }).unmount();
    expect(unmounted).toBe(2);
  });

  it('…is not unmounted again when the test ends', () => {
    console.warn = realWarn;
    expect(warnings.filter((w) => w.includes('Cannot unmount'))).toEqual([]);
    expect(unmounted).toBe(2);
  });

  afterAll(() => {
    console.warn = realWarn;
  });
});

describe('the rule, where it is written', () => {
  // The order is the point: teardown (flush → unmount → empty <body>) before
  // anything else, the network verdict after it (a late request that lands
  // during the flush is still this test's), and the same for the file's end.
  it('setup.ts tears every test down first and then fails it on any request', () => {
    const src = readFileSync(path.join(TESTS, 'setup.ts'), 'utf8');
    expect(src).toMatch(/import \{ failOnNetworkHits \} from '\.\/helpers\/noNetwork';/);
    expect(src).toMatch(/import \{ teardownDom \} from '\.\/helpers\/teardown';/);
    expect(src).toMatch(/afterEach\(async \(\) => \{\n {2}await teardownDom\(\);[\s\S]*?failOnNetworkHits\('\(this test\)'\);\n\}\);/);
    expect(src).toMatch(/afterAll\(async \(\) => \{\n {2}await teardownDom\(\);\n {2}failOnNetworkHits\([^)]*\);\n\}\);/);
  });

  // ⚠ The pattern that failed v0.49.0: a test emptying <body> itself. Under
  // a page it has not unmounted it is the race; after one it has, it is the
  // harness's job anyway. Filling <body> with markup a test needs is fine.
  it('no test empties <body> on its own', () => {
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) walk(p);
        else if (name.endsWith('.test.ts')) files.push(p);
      }
    };
    walk(TESTS);
    expect(files.length, 'the scan found the suite').toBeGreaterThan(300);
    const wipe = /document\.body\.(innerHTML\s*=\s*(''|""|``)|textContent\s*=\s*(''|"")|replaceChildren\(\s*\))/;
    const offenders = files
      .filter((f) => f !== __filename)
      .flatMap((f) =>
        readFileSync(f, 'utf8')
          .split('\n')
          .map((line, i) => ({ line, at: `${path.relative(TESTS, f)}:${i + 1}` }))
          .filter(({ line }) => wipe.test(line) && !line.trim().startsWith('//')),
      )
      .map(({ at }) => at);
    expect(offenders, "use unmountAll() / teardownDom() from tests/helpers/teardown").toEqual([]);
  });
});
