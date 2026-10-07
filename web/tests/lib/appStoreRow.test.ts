// #162 - the "Apps → App store" row's ONE rule (core lib/appStoreRow), which
// the explorer applies for every host - the web SPA, the desktop app, an
// embed - so no host writes a second one (the owner, 2026-10-06: the screen is
// in the desktop app too, under the same rule).
//
// What has to stay true:
//   · the host must have the screen (`config.appStorePage`) - silence is no;
//   · an app token never gets the row, whatever the server says;
//   · the server's answer decides the rest: `visible: true` only, and no
//     answer (an older server, a refusal) is no;
//   · the server is not even asked when the host has no page or the caller is
//     an app token (appStoreAsks), which is what the explorer checks before
//     its request.
import { describe, expect, it } from 'vitest';

import { appStoreAsks, appStoreRowShown } from '@brftech/filex-core/src/lib/appStoreRow';

describe('the App store row', () => {
  it('is drawn for a person whose host has the page and whom the server shows the screen', () => {
    expect(appStoreRowShown(true, false, { visible: true })).toBe(true);
  });

  it('is not drawn when the host has no page, whatever the server says', () => {
    expect(appStoreRowShown(false, false, { visible: true })).toBe(false);
    expect(appStoreAsks(false, false)).toBe(false);
  });

  it('is never drawn for an app token, and the server is not asked for one', () => {
    expect(appStoreRowShown(true, true, { visible: true })).toBe(false);
    expect(appStoreAsks(true, true)).toBe(false);
  });

  it('follows the server: not shown, or no answer at all, is no row', () => {
    expect(appStoreRowShown(true, false, { visible: false })).toBe(false);
    expect(appStoreRowShown(true, false, null)).toBe(false);
    expect(appStoreRowShown(true, false, undefined)).toBe(false);
    expect(appStoreAsks(true, false)).toBe(true);
  });
});
