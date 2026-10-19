// The explorer's chrome speaks the language lib/explorerLocale resolves
// (#191 follow-up, the maintainers' rule 2026-10-08; #275, the owner's rule
// 2026-10-19).
//
// The server says every notification in the language of the person's
// account. On the web and desktop apps the explorer therefore speaks the
// ACCOUNT's language, and the host's `locale` is only what it draws until the
// account answers, what a page with nobody signed in keeps, and the starting
// value an account with no language takes (written to the account then, as
// the desktop's pinToAdopt does; the web panel's sign-in has the server do
// it). An EMBED ranks the person's own pick in this browser, the account (a
// person's credential only), the browser and then the host - and never writes
// the host's language to the account. The order and the account rules are
// tested behaviourally in web/tests/lib/explorerLocale.test.ts; this file pins
// that FileExplorer is wired to them.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

describe("the explorer's language is lib/explorerLocale's", () => {
  it('one resolver decides, fed the embed pick, the account, the browser and the host', () => {
    expect(src).toMatch(/const accountLocale = ref\(''\);/);
    expect(src).toMatch(/const embedded = computed\(\(\) => explorerIsEmbedded\(props\.config\)\);/);
    const resolved = /const resolvedLocale = computed\(\(\) =>\s*resolveExplorerLocale\(\{([\s\S]*?)\}\),\s*\);/.exec(src);
    expect(resolved, 'resolvedLocale').toBeTruthy();
    expect(resolved![1]).toMatch(/embedded: embedded\.value/);
    expect(resolved![1]).toMatch(/chosen: embedLocaleChoice\(\)/);
    expect(resolved![1]).toMatch(/account: accountLocale\.value/);
    expect(resolved![1]).toMatch(/browser: navigatorLanguages\(\)/);
    expect(resolved![1]).toMatch(/host: props\.config\.locale/);
    expect(src).toMatch(/const locale = computed\(\(\) => resolvedLocale\.value\.locale\);/);
    // No second decision anywhere in the file.
    expect(src).not.toMatch(/resolveLocale\(/);
  });

  it('the API client says things in the language on screen, and so do the panels opened over the explorer', () => {
    expect(src).toMatch(/const api = useFileApi\(props\.config, \{ screenLocale: \(\) => locale\.value \}\);/);
    expect(src.indexOf('const locale = computed(')).toBeLessThan(src.indexOf('const api = useFileApi('));
    expect(src).toMatch(/<ConnectionsPanel\s+v-if="showConnections"\s+:config="screenConfig"/);
    expect(src).toMatch(/<TokensPanel :config="screenConfig" full \/>/);
  });

  it("the account is asked only for a PERSON, through lib/explorerLocale's accountLanguage", () => {
    // The same gate the account's time zone uses (useExplorerTimeZone).
    expect(src).toMatch(/const personCaller = computed\(\(\) => \(props\.config\.callerKind \?\? capabilitiesData\.value\?\.caller_kind\) === 'user'\);/);
    const deps = /function accountLanguageDeps\(\): AccountLanguageDeps \{([\s\S]*?)\n\}/.exec(src);
    expect(deps, 'accountLanguageDeps').toBeTruthy();
    expect(deps![1]).toMatch(/person: personCaller\.value/);
    expect(deps![1]).toMatch(/embedded: embedded\.value/);
    expect(deps![1]).toMatch(/updateProfile: \(patch\) => userSettingsApi\(api\.jsonFetch, connectionsBase\(props\.config\)\)\.updateProfile\(patch\)/);
    const adopt = /function adoptAccountLocale\(\): void \{([\s\S]*?)\n\}/.exec(src);
    expect(adopt, 'adoptAccountLocale').toBeTruthy();
    expect(adopt![1]).toMatch(/accountLanguage\(accountLanguageDeps\(\)\)/);
    expect(adopt![1]).toMatch(/if \(own\) accountLocale\.value = own;/);
    // The host's language is no longer written from here: the seed lives in
    // accountLanguage, which skips it for an embed.
    expect(src).not.toMatch(/updateProfile\(\{ locale: seed \}\)/);
  });

  it('a host that moves to another language while mounted is followed - except in an embed, where it ranks below the account', () => {
    expect(src).toMatch(/watch\(\s*\(\) => props\.config\.locale,\s*\(\) => \{\s*if \(embedded\.value\) return;\s*accountLocale\.value = '';/);
  });

  it("an embed's pick goes through pickEmbedLanguage, and only an embed gets the Language row, the dialog and the server's list", () => {
    const pick = /function pickEmbedLocale\(code: string\): void \{([\s\S]*?)\n\}/.exec(src);
    expect(pick, 'pickEmbedLocale').toBeTruthy();
    expect(pick![1]).toMatch(/pickEmbedLanguage\(code, accountLanguageDeps\(\)\)/);
    expect(src).toMatch(/:language-row="embedded && !accountSettingsHooks\(\)\?\.language/);
    expect(src).toMatch(/@open-language="showLanguage = true/);
    expect(src).toMatch(/<LanguageDialog\s+v-if="embedded"/);
    const load = /onMounted\(\(\) => \{\s*if \(!embedded\.value\) return;\s*void loadLocalesOnce\(\{([\s\S]*?)\}\);/.exec(src);
    expect(load, 'the embed loads the list once').toBeTruthy();
    expect(load![1]).toMatch(/base: connectionsBase\(props\.config\)/);
    expect(load![1]).toMatch(/timeoutMs: LOCALES_LIST_TIMEOUT_MS/);
  });
});
