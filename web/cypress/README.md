# filex admin Cypress suite

End-to-end tests for the admin SPA. Run against any live filex
instance (production, staging, local dev). The suite avoids
destructive operations beyond per-test creates that clean up after
themselves.

## Layout

```
cypress.config.ts        # baseUrl, env vars
cypress/
  e2e/
    00-smoke.cy.ts        # healthz, capabilities, login page boots
    10-login.cy.ts        # happy / sad auth paths
    20-dashboard.cy.ts    # stat cards + total_bytes (v0.1.19 regression)
    30-storages.cy.ts     # Depolar list + stats + replica row hygiene
    40-replication.cy.ts  # replication-targets CRUD + pairing wiring (v0.1.18)
    50-shares.cy.ts       # admin Paylaşımlar envelope + no "undefined"
    60-explorer.cy.ts     # explorer surface + cross-storage search
    70-admin-endpoints.cy.ts  # every admin GET answers 200/204 (route drift guard)
  support/
    e2e.ts                # global setup
    commands.ts           # cy.apiLogin / cy.uiLogin / cy.adminGet
```

## Running

```sh
# Headless run against fm.example.com:
CYPRESS_ADMIN_PASSWORD=<see-memory> \
  pnpm --filter @brftech/filex-admin cy:run

# Open the interactive runner:
CYPRESS_ADMIN_PASSWORD=<…> pnpm --filter @brftech/filex-admin cy:open

# Pointing at a local dev server:
CYPRESS_BASE_URL=http://localhost:5212 \
CYPRESS_ADMIN_PASSWORD=<…> \
  pnpm --filter @brftech/filex-admin cy:run
```

## Credentials

The admin password for `fm.example.com` lives in the operator's auto-memory
at `memory/filex_admin_creds.md`. Never commit it. Pass it through
the `CYPRESS_ADMIN_PASSWORD` env var instead.
