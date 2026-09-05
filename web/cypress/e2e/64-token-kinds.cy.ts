// 64-token-kinds — an APP token cannot manage its owner's credentials.
//
// A filex API token authenticates AS its owner (auth/apitoken_middleware.go
// resolves the row to a user), so from inside a request handler a shared embed
// token and somebody's own CLI token look identical. The embeds we run inject
// ONE token from the host's proxy for every visitor — which meant every
// visitor of an embed was, for these four routes, the token's owner:
//
//   GET /api/tokens            → list the proxy's API tokens
//   DELETE /api/tokens/{id}    → revoke the proxy's own token, from the embed
//   POST /api/auth/s3-keys     → mint an S3 access key bound to the owner
//   POST /api/auth/ssh-keys    → …and an SSH key, and an NFS export path
//
// Migration 00030 gives a token a KIND, and the four routes share ONE gate
// (`handlers.RequirePersonalCaller`, mounted on the route group in routes.go).
// The first round of that change gated `/api/tokens` alone and left the other
// three answering 200 — same hole, different button — which is why this spec
// asserts all four and BOTH directions.
//
// ⚠ Both directions, always. "App is refused" alone would pass against a gate
// that refuses everybody, which is a worse bug and an easy one to ship: the
// no-token case is a cookie session, i.e. a person, and must keep working.
//
// The UI half of the same feature — that the explorer hides the star and the
// identity views when the server says `caller_kind: "app"` — is 46-star-action.

/** Every route the gate covers. One entry per METHOD+PATH the group mounts a
 *  read on: a gate that only wrapped the POSTs would let an embed visitor list
 *  the proxy's credentials, which was the original report. */
const GATED = [
  '/api/tokens',
  '/api/auth/s3-keys',
  '/api/auth/ssh-keys',
  '/api/auth/nfs-exports',
];

/** Mint a token of `kind` for the calling admin and hand back the plaintext.
 *  `POST /api/admin/ai-tokens` returns it exactly once. */
function mint(kind: 'app' | 'user', label: string) {
  return cy.apiLogin().then((admin) =>
    cy
      .request({
        method: 'POST',
        url: '/api/admin/ai-tokens',
        headers: { Authorization: `Bearer ${admin}` },
        body: { label, kind },
      })
      .then((res) => {
        expect(res.status, `mint a ${kind} token`).to.eq(201);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.row.kind, 'the server stored the kind we asked for').to.eq(kind);
        return { token: body.token as string, id: body.row.id as number };
      }),
  );
}

describe('app token vs user token', () => {
  const made: number[] = [];

  after(() => {
    // Leave the instance as we found it: these rows are visible on the admin
    // token screen and in the audit log, and a later spec listing tokens
    // should not have to know about ours.
    cy.apiLogin().then((admin) => {
      for (const id of made) {
        cy.request({
          method: 'DELETE',
          url: `/api/admin/ai-tokens/${id}`,
          headers: { Authorization: `Bearer ${admin}` },
          failOnStatusCode: false,
        });
      }
    });
  });

  it('capabilities reports the caller kind, and it is the kind that was minted', () => {
    // This is the signal the explorer reads to suppress the identity surfaces
    // (FileExplorer.callerIsApp). If it stopped being reported the UI would
    // silently fall back to "person" — the safe default, and a silent one.
    mint('app', 'cypress-kind-caps-app').then(({ token, id }) => {
      made.push(id);
      cy.request({
        url: '/api/files/capabilities',
        headers: { Authorization: `Bearer ${token}` },
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.caller_kind, 'app token → caller_kind').to.eq('app');
      });
    });
    mint('user', 'cypress-kind-caps-user').then(({ token, id }) => {
      made.push(id);
      cy.request({
        url: '/api/files/capabilities',
        headers: { Authorization: `Bearer ${token}` },
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.caller_kind, 'user token → caller_kind').to.eq('user');
      });
    });
  });

  it('an APP token is refused on every self-service credential route', () => {
    mint('app', 'cypress-kind-gate-app').then(({ token, id }) => {
      made.push(id);
      for (const path of GATED) {
        cy.request({
          url: path,
          headers: { Authorization: `Bearer ${token}` },
          failOnStatusCode: false,
        }).then((res) => {
          // 403 and not 404: the route exists and the credential is valid —
          // what is refused is this KIND of credential using it. A 404 would
          // send an operator hunting for a missing route.
          expect(res.status, `app token → GET ${path}`).to.eq(403);
          const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
          // Machine-readable, because "your role is too low" is also a 403 here
          // and a host app has to be able to tell the two apart.
          expect(body.reason, `refusal reason for ${path}`).to.eq('app_token');
          expect(body.token_kind, `token_kind for ${path}`).to.eq('app');
          // The message is read in somebody's proxy logs, not in this file, so
          // it has to name the token and a way out.
          expect(body.error, `refusal names the token for ${path}`).to.contain(
            'cypress-kind-gate-app',
          );
        });
      }
    });
  });

  it('a USER token reaches every one of them', () => {
    mint('user', 'cypress-kind-gate-user').then(({ token, id }) => {
      made.push(id);
      for (const path of GATED) {
        cy.request({
          url: path,
          headers: { Authorization: `Bearer ${token}` },
          failOnStatusCode: false,
        }).then((res) => {
          expect(res.status, `user token → GET ${path}`).to.eq(200);
        });
      }
    });
  });

  it('a cookie/bearer SESSION reaches them too — a person is never an app token', () => {
    // ⚠ The gate reads the API-token row from the request context. No token at
    // all means a session, and `TokenFrom` then yields a zero value whose
    // `IsApp()` must be false. Getting that inverted would lock every human
    // out of their own API-keys screen while every test using an app token
    // still passed.
    cy.apiLogin().then((session) => {
      for (const path of GATED) {
        cy.request({
          url: path,
          headers: { Authorization: `Bearer ${session}` },
          failOnStatusCode: false,
        }).then((res) => {
          expect(res.status, `session → GET ${path}`).to.eq(200);
        });
      }
    });
  });

  it('the refusal covers the WRITE verbs, not just the reads', () => {
    // The embed hole that was reported is a write: an embed visitor minting an
    // S3 key bound to the proxy's owner, or revoking the proxy's own token. A
    // gate mounted only on the GETs would leave exactly that open.
    mint('app', 'cypress-kind-write-app').then(({ token, id }) => {
      made.push(id);
      const h = { Authorization: `Bearer ${token}` };
      cy.request({
        method: 'POST',
        url: '/api/tokens',
        headers: h,
        body: { label: 'should-never-exist' },
        failOnStatusCode: false,
      })
        .its('status')
        .should('eq', 403);
      cy.request({
        method: 'POST',
        url: '/api/auth/s3-keys',
        headers: h,
        body: { label: 'should-never-exist' },
        failOnStatusCode: false,
      })
        .its('status')
        .should('eq', 403);
      cy.request({
        method: 'DELETE',
        // Its own id: the exact move the report described, and the one that
        // would take the embed down for every other visitor at once.
        url: `/api/tokens/${id}`,
        headers: h,
        failOnStatusCode: false,
      })
        .its('status')
        .should('eq', 403);
      // …and the token it tried to revoke is still there.
      cy.apiLogin().then((admin) => {
        cy.request({
          url: '/api/admin/ai-tokens',
          headers: { Authorization: `Bearer ${admin}` },
        }).then((res) => {
          const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
          const ids = (body.tokens ?? []).map((t: { id: number }) => t.id);
          expect(ids, 'the app token could not revoke itself').to.include(id);
        });
      });
    });
  });

  it('CONFINEMENT is a different axis and is not caught by this gate', () => {
    // ⚠ A `root:`-scoped token may perfectly well be a person's — it is how
    // somebody limits their own CLI to one folder. If the gate ever grew to
    // "narrow tokens are not people" it would start refusing ordinary users,
    // and every case above would still pass, because none of them is confined.
    const storage = (Cypress.env('SEEDED_STORAGE') as string) || '';
    if (!storage) {
      cy.log('no seeded storage — a root: scope needs an adapter to name');
      return;
    }
    cy.apiLogin().then((admin) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/ai-tokens',
        headers: { Authorization: `Bearer ${admin}` },
        body: {
          label: 'cypress-kind-confined-user',
          kind: 'user',
          scopes: `root:${storage}://`,
        },
      }).then((res) => {
        expect(res.status, `mint a confined user token: ${JSON.stringify(res.body)}`).to.eq(
          201,
        );
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        made.push(body.row.id);
        cy.request({
          url: '/api/tokens',
          headers: { Authorization: `Bearer ${body.token}` },
          failOnStatusCode: false,
        })
          .its('status')
          .should('eq', 200);
      });
    });
  });
});
