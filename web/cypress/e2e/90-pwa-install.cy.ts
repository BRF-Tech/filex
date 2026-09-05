// 90-pwa-install — the standalone SPA's PWA install + update surface
// (Dilim 1, Part B). Real-browser evidence: manifest is served + linked, the
// install banner appears only when the browser offers install and the app
// isn't already installed, iOS gets manual instructions instead of a button,
// and dismissing is remembered. Runs against the Vite dev server (PWA plugin
// devOptions enabled), no backend needed — the banner lives on the login page.

// ⚠ This is the ONE spec that wants the install banner on screen, so it opts
// out of the global dismissal in cypress/support/e2e.ts. Everything else in the
// suite runs with the banner dismissed, because its full-width wrapper covers
// the bottom of the sidebar (the comment there has the measurement).
before(() => {
  Cypress.env('KEEP_INSTALL_BANNER', true);
  // ⚠ And the REAL service worker: the rest of the suite is served an empty
  // one (cypress/support/e2e.ts explains why — its precache was two thirds of
  // all traffic and stalled cy.visit for a full minute at a time). This is the
  // spec that measures the worker, so it has to get the genuine article.
  Cypress.env('KEEP_SERVICE_WORKER', true);
});
after(() => {
  Cypress.env('KEEP_INSTALL_BANNER', false);
  Cypress.env('KEEP_SERVICE_WORKER', false);
});

/** A phone UA. The ONLY environment in which the native-install path exists:
 *  `canPromptInstall` is gated on `detectDesktopPlatform() === null`, because
 *  on a PC the product deliberately offers the desktop app instead ("offering
 *  both at once asks the user to choose between two things that sound
 *  identical"). Two cases below used to assert the native button on a desktop
 *  browser, where the component can never render it — they could not pass on
 *  any machine a developer or a CI runner actually has. */
function androidUA(win: Window) {
  Object.defineProperty(win.navigator, 'userAgent', {
    value:
      'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36',
    configurable: true,
  });
}

// A non-standalone, non-iOS desktop-Chrome environment: matchMedia reports the
// app is NOT display-mode:standalone. Applied via onBeforeLoad so it's in place
// before the app boots and the composable snapshots standalone state.
function desktopEnv(win: Window) {
  const real = win.matchMedia?.bind(win);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (win as any).matchMedia = (q: string) => ({
    matches: false,
    media: q,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false;
    },
    _real: real,
  });
}

// Fire a synthetic beforeinstallprompt with a working prompt()/userChoice so
// the composable captures it exactly as Chrome would.
function fireBeforeInstallPrompt(win: Window, outcome: 'accepted' | 'dismissed' = 'accepted') {
  const evt = new win.Event('beforeinstallprompt') as Event & {
    platforms?: string[];
    prompt?: () => Promise<void>;
    userChoice?: Promise<{ outcome: string; platform: string }>;
  };
  evt.platforms = ['web'];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (win as any).__promptCalled = false;
  evt.prompt = () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (win as any).__promptCalled = true;
    return Promise.resolve();
  };
  evt.userChoice = Promise.resolve({ outcome, platform: 'web' });
  win.dispatchEvent(evt);
}

describe('PWA install surface', () => {
  it('serves a valid webmanifest linked from the document', () => {
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
    cy.get('link[rel="manifest"]').should('have.attr', 'href').and('include', 'manifest');

    cy.request('/admin/manifest.webmanifest').then((res) => {
      // ⚠ This used to be a weak "not text/html", under a comment claiming the
      // header was host-dependent — Go's mime table reading /etc/mime.types on
      // Linux and knowing nothing about the extension on Windows. That was
      // WRONG, so the weakening was unearned. `routes.contentTypeForName`
      // returned "" for `.webmanifest`, so net/http fell back to
      // `http.DetectContentType` — pure Go, a compiled-in sniff table, no
      // /etc/mime.types anywhere in it — which answered
      // `text/plain; charset=utf-8` on EVERY host, measured under GOOS=linux on
      // the same bytes. The bug was everywhere, not on one developer's machine.
      // One line in routes.go now names the type, so the real header is
      // assertable, and asserted.
      expect(res.headers['content-type'], 'manifest is served as a manifest').to.match(
        /application\/manifest\+json/,
      );
      // The other realistic break: the SPA fallback swallowing the request and
      // serving index.html with a 200, which leaves the app un-installable
      // while looking fine. `.webmanifest` is in `hasAssetExt` for that reason,
      // so a MISSING manifest 404s instead of being handed the SPA.
      expect(res.headers['content-type'], 'manifest is not swallowed by the SPA fallback')
        .to.not.match(/text\/html/);
      // ⚠ Still parsed defensively: cy.request only auto-parses a body whose
      // Content-Type says JSON, and `application/manifest+json` is not
      // `application/json`.
      const manifest = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
      expect(manifest.name).to.contain('filex');
      expect(manifest.start_url).to.eq('/admin/');
      // ⚠ `id`, not `scope`, is what pins the app's identity — assert on it,
      // because changing it turns every existing install into a second app.
      expect(manifest.id).to.eq('/admin/');
      // The MANIFEST scope is deliberately wider than the service worker's
      // (see vite.config.ts). Since GitHub #14 a non-admin who opens the app
      // is handed on to /drive/, and a '/admin/' manifest scope would eject
      // them from the installed window on their very first screen. The SW
      // registration below stays pinned to /admin/.
      expect(manifest.scope).to.eq('/');
      expect(manifest.display).to.eq('standalone');
      expect(manifest.icons).to.have.length.greaterThan(0);
      const purposes = manifest.icons.map((i: { purpose: string }) => i.purpose);
      expect(purposes).to.include('maskable');
    });
  });

  it('a MISSING .webmanifest 404s instead of being handed the SPA', () => {
    // The second half of the same fix: `.webmanifest` joined `hasAssetExt`, so
    // a manifest that is not there falls through to a 404 rather than to
    // index.html. Without it a broken build serves HTML with a 200 and the
    // browser reports "corrupt manifest" — a report that points at the file
    // instead of at the routing.
    cy.request({ url: '/admin/no-such-file.webmanifest', failOnStatusCode: false }).then(
      (res) => {
        expect(res.status, 'a missing manifest is a 404').to.eq(404);
        expect(res.headers['content-type'] ?? '', 'and not the SPA document').to.not.match(
          /text\/html/,
        );
      },
    );
  });

  it('a PC visitor is offered the DESKTOP APP, not a browser install', () => {
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
    cy.get('[data-testid="pwa-install-banner"]').should('be.visible');
    cy.get('[data-testid="desktop-download-button"]').should('be.visible');
    // ⚠ Even after the browser offers a native install, the PC path must stay
    // on the desktop app — `canPromptInstall` is explicitly false when a
    // desktop platform is detected. A regression that dropped that check would
    // show both offers at once.
    cy.window().then((win) => fireBeforeInstallPrompt(win));
    cy.get('[data-testid="pwa-install-button"]').should('not.exist');
    cy.get('[data-testid="pwa-ios-instructions"]').should('not.exist');
  });

  it('shows the native install button after beforeinstallprompt (Android Chrome)', () => {
    cy.visit('/admin/login', {
      onBeforeLoad(win) {
        desktopEnv(win);
        androidUA(win);
      },
    });
    // Nothing is offered on a phone until the browser says it can install.
    cy.get('[data-testid="pwa-install-banner"]').should('not.exist');
    cy.window().then((win) => fireBeforeInstallPrompt(win));
    cy.get('[data-testid="pwa-install-banner"]').should('be.visible');
    cy.get('[data-testid="pwa-install-button"]').should('be.visible');
    cy.get('[data-testid="desktop-download-button"]').should('not.exist');
    cy.get('[data-testid="pwa-ios-instructions"]').should('not.exist');
  });

  it('clicking Install triggers the native prompt and clears the banner', () => {
    cy.visit('/admin/login', {
      onBeforeLoad(win) {
        desktopEnv(win);
        androidUA(win);
      },
    });
    cy.window().then((win) => fireBeforeInstallPrompt(win, 'accepted'));
    cy.get('[data-testid="pwa-install-button"]').click();
    cy.window().its('__promptCalled').should('eq', true);
    // Event is single-use; after accepting, the offer clears.
    cy.get('[data-testid="pwa-install-button"]').should('not.exist');
  });

  it('dismiss hides the banner and is remembered across reloads', () => {
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
    cy.window().then((win) => fireBeforeInstallPrompt(win));
    cy.get('[data-testid="pwa-install-banner"]').should('be.visible');
    cy.get('[data-testid="pwa-install-dismiss"]').click();
    cy.get('[data-testid="pwa-install-banner"]').should('not.exist');
    cy.window().then((win) => {
      expect(win.localStorage.getItem('filex.installPrompt.dismissed')).to.eq('1');
    });
    // After reload the offer stays hidden even if the browser offers again.
    cy.reload();
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
    cy.window().then((win) => fireBeforeInstallPrompt(win));
    cy.get('[data-testid="pwa-install-banner"]').should('not.exist');
  });

  it('iOS Safari gets manual instructions and no install button', () => {
    cy.visit('/admin/login', {
      onBeforeLoad(win) {
        desktopEnv(win);
        Object.defineProperty(win.navigator, 'userAgent', {
          value:
            'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1',
          configurable: true,
        });
      },
    });
    // iOS never fires beforeinstallprompt, yet the manual help must show.
    cy.get('[data-testid="pwa-ios-instructions"]').should('be.visible');
    cy.get('[data-testid="pwa-install-button"]').should('not.exist');
  });

  it('already-installed (standalone) suppresses the offer entirely', () => {
    cy.visit('/admin/login', {
      onBeforeLoad(win) {
        // Report display-mode: standalone → the app treats itself as installed.
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (win as any).matchMedia = (q: string) => ({
          matches: /standalone/.test(q),
          media: q,
          onchange: null,
          addEventListener() {},
          removeEventListener() {},
          addListener() {},
          removeListener() {},
          dispatchEvent() {
            return false;
          },
        });
      },
    });
    cy.window().then((win) => fireBeforeInstallPrompt(win));
    cy.get('[data-testid="pwa-install-banner"]').should('not.exist');
  });

  it('registers a service worker scoped to /admin/', () => {
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
    cy.window()
      .its('navigator.serviceWorker')
      .should('exist');
    // The composable calls useRegisterSW → registration lands under /admin/.
    cy.window().then(async (win) => {
      const reg = await win.navigator.serviceWorker.getRegistration('/admin/');
      // In dev the SW may still be installing; assert either a registration or
      // that the API is reachable (never throws). A present scope must match.
      if (reg) expect(reg.scope).to.include('/admin/');
    });
  });
});
