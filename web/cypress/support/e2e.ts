// e2e support file — loaded before every spec. Pulls in custom
// commands and any global setup (uncaught-exception muting, etc.).
import './commands';

// Cypress treats uncaught console errors as test failures. The admin
// SPA emits a benign Vue-router redirect message ("redirected from /
// to /dashboard") on cold load that we don't want to fail on.
Cypress.on('uncaught:exception', (err) => {
  if (/redirected from|Avoided redundant navigation/i.test(err.message)) {
    return false;
  }
  return undefined;
});
