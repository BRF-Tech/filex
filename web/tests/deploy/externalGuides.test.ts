// Guides outside this repository that tell people how to set filex up, and
// the names they quote. Each one was written against a running install: it
// copies labels off the screen, paths off the network tab and variables off
// the docs. Rename one of them and the guide lies to everybody who follows it,
// while every other test in this tree stays green.
//
// ⚠ A red test here does not mean the rename is wrong. It means the guide has
// to change in the same release: open a pull request against it (Release
// process, step 3 in docs/CONTRIBUTING.md), then update the entry below.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { ADMIN_NAV } from '@/lib/adminNav';

const REPO = path.resolve(__dirname, '../../..');
const read = (...p: string[]) => readFileSync(path.join(REPO, ...p), 'utf8');
const en = JSON.parse(read('web', 'src', 'locales', 'en.json'));
const text = (key: string): unknown =>
  key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], en);

interface Guide {
  name: string;
  /** Where the guide's source lives - the place the pull request goes. */
  source: string;
  /** en.json key -> the exact English text the guide quotes. */
  labels: Record<string, string>;
  /** [menu entry, page]: the guide's "Entry > Page" path through the admin menu. */
  menus: [string, string][];
  /** [file, text]: a route, redirect or variable the guide quotes, in the file that defines it. */
  code: [string, string][];
}

const GUIDES: Guide[] = [
  {
    name: 'authentik integration guide',
    source:
      'https://github.com/goauthentik/authentik/blob/main/website/integrations/chat-communication-collaboration/filex/index.mdx',
    labels: {
      'nav.top.settings': 'Settings',
      'nav.authProviders': 'Identity providers',
      'authProviders.tabs.oidc': 'OIDC',
      'authProviders.providers.oidc': 'OIDC (Keycloak, Auth0, Authentik…)',
      'common.enabled': 'Enabled',
      'authProviders.fields.issuer': 'Issuer URL',
      'authProviders.fields.client_id': 'Client ID',
      'authProviders.fields.client_secret': 'Client secret',
      'authProviders.fields.role_claim': 'Role claim',
      'authProviders.fields.admin_group': 'Admin group / role',
      'authProviders.fields.trust_email': "Trust this provider's email addresses",
      'common.testNow': 'Test now',
      'authProviders.saveApply': 'Save and apply',
      'nav.top.people': 'People',
      'nav.users': 'Users',
      'login.oidc': 'Sign in with SSO',
    },
    menus: [
      ['nav.top.settings', 'nav.authProviders'],
      ['nav.top.people', 'nav.users'],
    ],
    code: [
      ['backend/internal/api/routes.go', 'r.Get("/oidc/callback"'],
      ['backend/internal/config/config.go', '"/api/auth/oidc/callback"'],
      ['backend/internal/api/handlers/auth.go', '"/admin/login?signed_out=1"'],
      ['backend/internal/api/handlers/auth.go', '"/drive/login?signed_out=1"'],
      ['backend/internal/config/config.go', '"FILEX_PUBLIC_URL"'],
      ['backend/internal/config/config.go', '"FILEX_AUTH_DRIVERS"'],
      ['backend/internal/config/config.go', '"FILEX_OIDC_ISSUER"'],
      ['backend/internal/config/config.go', '"FILEX_OIDC_CLIENT_ID"'],
      ['backend/internal/config/config.go', '"FILEX_OIDC_CLIENT_SECRET"'],
      ['backend/internal/config/config.go', '"FILEX_OIDC_ROLE_CLAIM"'],
      ['backend/internal/config/config.go', '"FILEX_OIDC_ADMIN_GROUP"'],
      ['backend/internal/config/config.go', '"FILEX_OIDC_TRUST_EMAIL"'],
      ['backend/internal/config/config.go', '"FILEX_SECRET_KEY"'],
    ],
  },
];

describe.each(GUIDES)('the $name', (guide) => {
  const why = `quoted by ${guide.source} - change the guide in the same release`;

  it('finds every label it quotes, word for word', () => {
    for (const [key, quoted] of Object.entries(guide.labels)) {
      expect(text(key), `${key} ${why}`).toBe(quoted);
    }
  });

  it('finds every page under the menu entry it names', () => {
    for (const [entry, page] of guide.menus) {
      const found = ADMIN_NAV.find((e) => e.label === entry);
      const pages = found?.sections.flatMap((s) => s.pages.map((p) => p.label)) ?? [];
      expect(pages, `${entry} > ${page} ${why}`).toContain(page);
    }
  });

  it('finds every route, redirect and variable it quotes', () => {
    for (const [file, quoted] of guide.code) {
      expect(read(file), `${file}: ${quoted} ${why}`).toContain(quoted);
    }
  });
});
