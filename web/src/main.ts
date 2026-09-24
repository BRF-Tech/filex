import { createApp } from 'vue';
import { createPinia } from 'pinia';

import App from './App.vue';
import router from './router';
import { i18n, applyStoredLocale } from './i18n';
import { applyStoredTheme } from './lib/theme';
import { applyPalette } from './lib/palette';
import {
  configurePrefs,
  hasSession,
  isolateLtrRuns,
  localeDir,
  sealSessionless,
  syncDocumentDir,
} from '@brftech/filex-core';
import { loadCustomCss } from './lib/customCss';
import { AppearanceApi } from './api/appearance';
import { applyInstanceThemes, primeInstanceDefault } from './lib/instanceThemes';
import { onPublicPageBase } from './router';
import { useToastStore } from './stores/toast';
import { installAxiosInterceptors } from './api/client';
import { initRuntimeConfig } from './api/runtimeConfig';

import './styles/main.css';
// ⚠ The admin table's stylesheet used to be `./styles/table.css`, imported
// here. It moved into the core package (packages/core/src/styles/base.css,
// the "THE ADMIN TABLE" block, loaded by the `@brftech/filex-core/style.css`
// line below) on 2026-09-20, because the panel's tables are not all in
// `web/src`: four of them are this package's connection panels and a fifth is
// the plugin surfaces' `list` node, and a core component cannot import a
// stylesheet out of `web/src`. One language, in the package BOTH trees reach.
// gorunum:v3-shell — the product's look, loaded ONCE for the whole app.
//
// ⚠⚠ It used to be imported per view (Explore, Home), which meant the `--fe-*`
// tokens simply did not exist on any route that mounted neither — measured on
// /login, where `--fe-bg` computed to the empty string, so no token on that
// page could have worked and every rule written against one silently did
// nothing. The shell is not one view's stylesheet any more; the admin chrome,
// the settings modal, the sign-in page and the install banner all paint with
// these tokens.
//
// ⚠ Repeat imports elsewhere are harmless (one module, resolved once) but
// redundant — this is the line that guarantees it, not they.
import '@brftech/filex-core/style.css';

// Pick up any injected runtime config (Electron preload sets the API base +
// token before the bundle boots). No-op in the plain web build. Must run before
// the first request fires from the router guard.
initRuntimeConfig();

// ⚠⚠ v3 — the look-and-language preferences belong to the ACCOUNT, not to
// this browser (`@brftech/filex-core` → lib/prefs). This names the surface
// they are stored under; `surface=desktop` is the same code in the desktop
// app, which is why there is no web-only branch anywhere in that module.
//
// Before the mount because `savePref` can be reached from any module the
// first render touches, and a write with no surface configured would land
// under the wrong one.
//
// ⚠⚠ `sessionAware` — this host draws a SIGN-IN PAGE, so "nobody is signed
// in" is a state it can really be in, and in that state the palette and
// light/dark mode in this browser belong to a person who is not here. Embeds
// and the desktop app declare nothing and keep reading them, because they are
// mounted inside something that already authenticated.
configurePrefs({ surface: 'web', sessionAware: true });

// ⚠⚠ WHOSE LOOK IS THIS? Asked HERE, synchronously, because everything
// below reads the answer and the first paint happens before `auth.fetchMe()`
// can resolve (`lib/prefs` → SESSION_LS_KEY has the full argument).
//
// A public link is the one case that needs no guessing: `/s/` and `/d/` are a
// stranger with a token, so the answer is no whatever this browser remembers,
// and it is SEALED rather than remembered — writing "no session" from a share
// link would reach the same browser's admin tab and make it flash.
if (onPublicPageBase()) sealSessionless();

// With nobody signed in, the operator's own default is the whole answer, and
// it is a PUBLIC fact — so it may be cached and painted on the first frame
// without touching anything personal. `GET /api/appearance` below confirms or
// replaces it a moment later.
if (!hasSession()) primeInstanceDefault();

// Apply theme + locale before mount so we never flash the wrong palette.
//
// ⚠ These read `localStorage`, which is now the FIRST-PAINT CACHE of the
// account's answer rather than the preference itself: the fetch cannot beat
// the first frame, so the window paints from the cache and `App.vue` re-paints
// from `/api/me/prefs` the moment a session exists.
//
// ⚠⚠ …and they read it ONLY FOR A PERSON. With no session they answer with
// the product's neutral defaults instead — stock palette, `prefers-color-scheme`
// for light/dark — so the sign-in page cannot wear the taste of whoever used
// this browser last (owner, 2026-09-21: "logoutluyken zaten seçtiğim tema
// değil, şirketin default…").
applyStoredTheme();
applyStoredLocale();
// ⚠ RTL — `<html dir>` is DERIVED from `<html lang>` from here on, for the
// life of the page: every place that sets the page's language (i18n's
// decision, the public link page) turns the layout with it, and a
// right-to-left pack's language arriving after the first paint turns it then.
// One rule, in lib/direction; nothing else in this app sets `dir`.
syncDocumentDir();
// ⚠ The PALETTE, for every route — not just the ones that mount an explorer.
// See lib/palette.ts: this used to be `FileExplorer.vue`'s own `onMounted`,
// so the panel ignored the palette on every page without a file browser on it.
applyPalette();

// tema:v1 — the instance's own palettes, published into the shared registry in
// packages/core so the explorer's gallery, the user settings modal and the
// admin editor all list the same set.
//
// Deliberately NOT awaited: blocking the mount on a network round-trip would
// trade a moment of default styling for a moment of blank page. Same trade the
// branded document title already makes.
void AppearanceApi.boot().then(applyInstanceThemes).catch(() => {
  /* No themes is a complete answer: the stock palette. */
});

// tema:v1 — the operator's own stylesheet (admin → Appearance).
//
// ⚠ Fetched from an AUTHENTICATED endpoint now, so it is a no-op until
// somebody is signed in — which is exactly the point. The login page must
// never wear a sheet that could hide the form on it.
void loadCustomCss();

const app = createApp(App);
const pinia = createPinia();

app.use(pinia);
app.use(router);
app.use(i18n);
// ⚠ RTL — the admin panel's half of the rule `useLocale().t` applies to the
// explorer's strings: in a right-to-left language, a `3 / 10` or a `tag:…`
// inside a sentence is isolated so it still reads left to right (core
// lib/direction isolateLtrRuns). A hook rather than a change to each string,
// so a translator writes plain text; in LTR the string passes through
// untouched.
i18n.global.setPostTranslationHandler((str) =>
  typeof str === 'string' && localeDir(i18n.global.locale.value) === 'rtl' ? isolateLtrRuns(str) : str,
);

// Wire axios -> router (401 redirect) and toast (network error surfacing)
// after Pinia + Router are attached so stores resolve.
installAxiosInterceptors({
  router,
  onUnauthorized: () => {
    // Preserve the interrupted location so login lands the user back where
    // they were headed. On a cold-load deep link vue-router already carries
    // the #<folder> hash in fullPath; after in-app navigation the explorer
    // writes it via replaceState behind the router's back — append it only
    // in that case or the hash doubles up.
    const current = router.currentRoute.value;
    let redirect = current.fullPath;
    if (!current.hash && window.location.hash) redirect += window.location.hash;
    router.push(
      redirect && redirect !== '/'
        ? { name: 'login', query: { redirect } }
        : { name: 'login' },
    );
  },
  onError: (msg) => {
    const toast = useToastStore();
    toast.error(msg);
  },
});

app.mount('#app');
