// The embedded explorer's chrome speaks the ACCOUNT's language (#191 follow-up,
// the maintainers' rule 2026-10-08).
//
// The server says every notification in the language of the person's
// account; an explorer embedded in a host page (work.example.com, the fishapp, an
// integrator's page) drew its chrome in the host's `locale`, so the same
// person read the explorer in one language and its bell, push and email in
// another. Now the host's `locale` is only: what the explorer draws until the
// account answers, what a page with nobody signed in keeps (a public link, an
// app's token), and the starting value an account with no language takes - it
// is written to the account then (as the desktop's pinToAdopt does; the web panel's sign-in has the server do it).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { userSettingsApi } from '@brftech/filex-core/src/lib/userSettingsHost';

afterEach(() => vi.unstubAllGlobals());

const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

describe("the explorer's chrome is in the account's language", () => {
  it("the account's language outranks the host's; the host's is the fallback", () => {
    expect(src).toMatch(/const accountLocale = ref\(''\);/);
    expect(src).toMatch(/const locale = computed\(\(\) => resolveLocale\(accountLocale\.value \|\| props\.config\.locale\)\);/);
  });

  it('is asked only for a PERSON: an app token or nobody signed in keeps the host language', () => {
    // The same gate the account's time zone uses (useExplorerTimeZone).
    expect(src).toMatch(/\(props\.config\.callerKind \?\? capabilitiesData\.value\?\.caller_kind\) === 'user'/);
    const adopt = /function adoptAccountLocale\(\): void \{([\s\S]*?)\n\}/.exec(src);
    expect(adopt, 'adoptAccountLocale').toBeTruthy();
    // No user in the answer (a public page): nothing changes.
    expect(adopt![1]).toMatch(/if \(!user\) return;/);
    // The account's own language wins.
    expect(adopt![1]).toMatch(/accountLocale\.value = own;/);
  });

  it("an account with no language takes the host's - written to the account", async () => {
    const adopt = /function adoptAccountLocale\(\): void \{([\s\S]*?)\n\}/.exec(src);
    expect(adopt![1]).toMatch(/\.updateProfile\(\{ locale: seed \}\)/);
    // …through the account's own profile route, with this explorer's credential.
    const calls: Array<{ url: string; method: string; body: string }> = [];
    const jsonFetch = vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: String(init?.method), body: String(init?.body) });
      return { locale: 'tr' } as never;
    });
    await userSettingsApi(jsonFetch, 'https://files.example/').updateProfile({ locale: 'tr' });
    expect(calls).toEqual([{ url: 'https://files.example/api/auth/profile', method: 'PATCH', body: '{"locale":"tr"}' }]);
  });

  it('a host that moves to another language while mounted is followed', () => {
    expect(src).toMatch(/watch\(\s*\(\) => props\.config\.locale,\s*\(\) => \{\s*accountLocale\.value = '';/);
  });
});
