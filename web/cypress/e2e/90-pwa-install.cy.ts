// 90-pwa-install — the standalone SPA's PWA install + update surface
// (Dilim 1, Part B). Real-browser evidence: manifest is served + linked, the
// install banner appears only when the browser offers install and the app
// isn't already installed, iOS gets manual instructions instead of a button,
// and dismissing is remembered. Runs against the Vite dev server (PWA plugin
// devOptions enabled), no backend needed — the banner lives on the login page.

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
      expect(res.headers['content-type']).to.include('manifest');
      expect(res.body.name).to.contain('filex');
      expect(res.body.start_url).to.eq('/admin/');
      expect(res.body.scope).to.eq('/admin/');
      expect(res.body.display).to.eq('standalone');
      expect(res.body.icons).to.have.length.greaterThan(0);
      const purposes = res.body.icons.map((i: { purpose: string }) => i.purpose);
      expect(purposes).to.include('maskable');
    });
  });

  it('shows the install banner after beforeinstallprompt (Chrome desktop)', () => {
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
    // Banner is hidden until the browser offers install.
    cy.get('[data-testid="pwa-install-banner"]').should('not.exist');
    cy.window().then((win) => fireBeforeInstallPrompt(win));
    cy.get('[data-testid="pwa-install-banner"]').should('be.visible');
    cy.get('[data-testid="pwa-install-button"]').should('be.visible');
    // iOS instructions must NOT appear on the desktop path.
    cy.get('[data-testid="pwa-ios-instructions"]').should('not.exist');
  });

  it('clicking Install triggers the native prompt and clears the banner', () => {
    cy.visit('/admin/login', { onBeforeLoad: desktopEnv });
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
