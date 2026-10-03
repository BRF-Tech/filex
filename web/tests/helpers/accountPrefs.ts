/**
 * The account's preference document, answered by the test file.
 *
 * Every preference a page writes — a theme or a density picked, the version
 * of an app the person has seen (an AppFrame opening), a storage order — is
 * mirrored locally at once and sent to the account 400 ms later:
 * `PUT /api/me/prefs` (core lib/prefs `savePref`). A test that writes one
 * and is still running 400 ms later sent that PUT to the network; under a
 * loaded full run that is SOMETIMES, so helpers/noNetwork fails such a test
 * only now and then. Answer it instead: call this once at the top of any
 * file whose tests write a preference, directly (`setTheme`, `setDensity`)
 * or through a page.
 *
 * Every test then gets a transport that answers 200, and a write still
 * waiting when the test ends is dropped with the rest of the module's state
 * (`resetPrefs`).
 *
 * ⚠ Two copies of core run in the web suite: its SOURCE modules
 * (`@brftech/filex-core/src/…`, what core components imported that way use)
 * and the BUILT bundle (`@brftech/filex-core`, what `web/src` imports). Each
 * has its own prefs module, so both are answered.
 */
import { afterEach, beforeEach } from 'vitest';
import { configurePrefs as configureBuilt, resetPrefs as resetBuilt } from '@brftech/filex-core';
import { configurePrefs, resetPrefs } from '@brftech/filex-core/src/lib/prefs';

const answer = (async () => new Response('{}', { status: 200 })) as unknown as typeof fetch;

export function answerAccountPrefs(): void {
  beforeEach(() => {
    configurePrefs({ surface: 'web', fetchImpl: answer });
    configureBuilt({ surface: 'web', fetchImpl: answer });
  });
  afterEach(() => {
    resetPrefs();
    resetBuilt();
  });
}
