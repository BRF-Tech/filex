// The strings the product shows a stranger, in one place, so that the check
// and the product cannot drift apart.
//
// This module holds no logic on purpose. It is imported by BOTH
// `scripts/check-shop-window.mjs` (which runs the queries against a live
// server) and `web/tests/deploy/shopWindow.test.ts` (which asserts the demo
// splash still prints them). Either half alone can pass while being wrong: a
// live check on a query nobody is shown proves nothing, and a copy check on a
// query nobody runs proves nothing either.

/**
 * The example searches the demo splash tells a visitor to type, and the file
 * each one promises them.
 *
 * ⚠ Measured on demo.filex.sh, 2026-09-07, with the exact strings the product
 * printed at the time:
 *
 *     invoice 2026  ->  0 hits   (splash: “invoice 2026” finds invoice_2026.pdf)
 *     invoice       ->  0 hits
 *     tag:report    ->  0 hits   (search box: e.g. invoice 2026, tag:report)
 *     mian.go       ->  2 hits
 *
 * Search worked. The two queries the product itself suggested were the two
 * that failed, on the most likely first interaction a visitor has — there was
 * no file with "invoice" in its name anywhere in the demo tree, and no tag
 * called "report" (tags are per-user rows a human clicks in).
 *
 * `web/src/locales/{en,tr}.json` → `demo.features.searchBody` is where these
 * are printed. Change one there and change it here, or the offline half fails
 * and tells you so.
 */
export const ADVERTISED_QUERIES = [
  {
    // Separator-blind: the file on disk is Budget-2026.csv and the visitor
    // types a space. This is the replacement for "invoice 2026", chosen
    // because the demo tree actually holds it.
    query: 'budget 2026',
    finds: 'Documents/Budget-2026.csv',
  },
  {
    // One typo forgiven — the more surprising half of the claim, and the one
    // that was already true.
    query: 'mian.go',
    finds: 'Code/main.go',
  },
];

/**
 * Paths under `/api/admin`, `/api/ai/admin` and the shared account's own
 * identity routes that a public demo must refuse to a signed-in visitor.
 *
 * ⚠⚠ Each entry carries a body that is INVALID on purpose. A working guard
 * answers 403 at the mount point and never reaches the handler, so the request
 * is inert; a guard that has been removed answers 400/404/200 instead of
 * performing the write. That is what makes it safe to point this check at an
 * instance and still have it mean something — and it is why the assertion is
 * "403, and only 403", not "not 200".
 *
 * ⚠ Every one of these answered on demo.filex.sh on 2026-09-07. All 101 routes
 * under /api/admin did, with the demo's published credentials, and not one
 * returned 403: resetting the shared password (locking out every other
 * reader until the nightly restore), deleting users, repointing an existing
 * storage, making the server connect wherever a visitor pointed smtp-test.
 */
export const DEMO_GUARDED_WRITES = [
  { method: 'POST', path: '/api/auth/password', why: 'changes the SHARED demo account password' },
  { method: 'PATCH', path: '/api/auth/profile', why: 'changes the shared account’s identity' },
  { method: 'POST', path: '/api/admin/users', why: 'creates users on someone else’s instance' },
  { method: 'POST', path: '/api/admin/storages', why: 'points the server at a stranger’s storage' },
  { method: 'POST', path: '/api/admin/settings/smtp-test', why: 'makes the server connect outward' },
  { method: 'PATCH', path: '/api/ai/admin/settings', why: 'the same admin surface behind a token' },
];

/**
 * Read verbs a demo must keep answering. A guard that refused these would
 * leave nothing to demonstrate — the whole point of a demo is showing the
 * operator surfaces — and a check that only asserted refusals would be
 * satisfied by an instance that is simply down.
 */
export const DEMO_ALLOWED_READS = ['/api/admin/users', '/api/admin/storages', '/api/admin/settings'];

/**
 * The demo's own published credentials, read from the demo itself.
 *
 * `GET /api/files/capabilities` answers an anonymous caller with `demo_user`
 * and `demo_pass` — deliberately, because the login splash prints them. So the
 * live check does not carry a password: it asks the instance what it is telling
 * visitors, and then does what a visitor does.
 */
export const DEMO_CREDENTIAL_FIELDS = { user: 'demo_user', pass: 'demo_pass' };

/**
 * The external services a booted instance is given a hostname for, so that
 * "the anonymous answer names no host" is a claim about redaction rather than
 * about an empty config.
 *
 * ⚠ Why both and not just OnlyOffice: `redactExternalHosts` walks the
 * `external` map and blanks the flat aliases, so ONE seeded service exercises
 * the loop but proves nothing about `onlyoffice_url`/`drawio_url`
 * individually — a redaction that dropped one alias would have passed. Measured
 * on demo.filex.sh 2026-09-07: the payload carried FOUR services (mermaid too;
 * the iframe converter was removed in 0.48), and only these can be seeded from
 * the environment, which is why the
 * check also asserts the list-free property "no entry under `external` has a
 * `url` key at all". That covers mermaid, and whatever is added next.
 */
export const EXTERNAL_SENTINELS = [
  { name: 'onlyoffice', env: 'FILEX_ONLYOFFICE_URL', alias: 'onlyoffice_url', host: 'onlyoffice-sentinel.invalid' },
  { name: 'drawio', env: 'FILEX_DRAWIO_URL', alias: 'drawio_url', host: 'drawio-sentinel.invalid' },
];

/**
 * The GitHub repository's About blurb and website, as this repository says they
 * should read.
 *
 * ⚠⚠ This constant exists because there was NO source of truth. The blurb is
 * typed into a web form in GitHub's settings and lives only there: grepped on
 * 2026-09-07, not one character of the published description appeared anywhere
 * in the tree, and no script or workflow syncs it. So it was never wrong or
 * right — it was unreviewable, and it drifted: the published text still listed
 * five storage drivers when the product had six (SMB shipped in v0.24.0), and
 * named none of real-time collaboration, the desktop app, folder sync or the
 * MCP server, all of which the README leads with.
 *
 * It is the first thing a stranger reads — GitHub prints it under the repo
 * name, in search results and on every link preview — and it is the one shop
 * window with no deploy step to get it wrong, which is exactly why nobody
 * looked at it for months.
 *
 * ⚠ GitHub truncates the About box at 350 characters. Keep it under that; the
 * offline half of the gate measures it.
 */
export const REPO_ABOUT =
  'Self-hosted file manager & sharing with a web UI, in one Go binary - ' +
  'open-source alternative to Nextcloud, Dropbox and Google Drive. ' +
  'Web-based file browser for local, S3, SFTP, WebDAV, FTP and SMB storage: ' +
  'private cloud served as S3, SFTP, NFS, WebDAV. SSO/LDAP, multi-tenant, ' +
  'encrypted folders, embeddable web component, desktop app, MCP server.';

/** The `homepage` field beside it. Not the docs site: filex.sh links there. */
export const REPO_HOMEPAGE = 'https://filex.sh';

/**
 * Hosts the published front page must link, and why each one is load-bearing.
 *
 * ⚠ Measured 2026-09-07: filex.sh linked `github.com/BRF-Tech/filex/blob/main/
 * docs/README.md` from both its "Docs" buttons while docs.filex.sh — a whole
 * documentation SITE, answering 200 — went unlinked from the product's front
 * page. `site/index.html` in this repository already pointed at it in three
 * places; the deployed page was simply older. Nothing compares the two, because
 * `sync-site.sh` uploads and never verifies.
 */
export const SITE_MUST_LINK = [
  { host: 'docs.filex.sh', why: 'the documentation site, which is otherwise reachable only by guessing the hostname' },
  { host: 'github.com', why: 'the source. A self-hosted product whose landing page does not link its repository is a download page' },
  { host: 'demo.filex.sh', why: 'the demo. It is the shortest path from "what is this" to "I have used it"' },
];

// ⚠ Pictures are named here as e2e/shots/manifest.json names them
// (`sidenav/sidenav-rail-1440.png`), not by URL: the URL carries the content
// hash and changes with every retake, the name does not. The README links the
// manifest's current URL of each, and the test beside this declaration fails
// when a README picture is not declared here (task #176).
/**
 * Every screenshot the README shows, and the sources of what is IN it.
 *
 * ⚠⚠ A screenshot is a promise about what the product looks like now, and a
 * stale one is not missing information — it is WRONG information, because the
 * reader takes it for the current product. `admin-plugins.png` shipped six
 * releases out of date with a private repository URL inside it, and nothing in
 * eleven release steps looked at a picture.
 *
 * Full visual judgement is not automatable. Staleness is: a picture taken
 * before the last change to the code that draws it cannot be showing that
 * change (the manifest's `taken` is when). `depicts` is what makes that question answerable — it is the one
 * thing here a person has to know, so it is written down once, next to
 * everything else the gate knows, and the offline half asserts every path in it
 * still exists. A rename empties a glob silently; that is the failure this
 * declaration is checked against.
 *
 * ⚠ `depicts` is the surface the picture is ABOUT, not everything visible in
 * it. Listing the whole frontend would make every shot stale on every push and
 * the gate would be switched off within a week; listing one file per picture
 * would miss the component that actually changed. The rule of thumb used here:
 * the view, plus the widgets a reader would look at the picture TO SEE.
 */
export const SCREENSHOTS = [
  {
    name: 'explorer-grid-light.png',
    depicts: ['packages/core/src/components/GridView.vue', 'packages/core/src/components/Toolbar.vue', 'packages/core/src/components/ViewSwitcher.vue'],
  },
  {
    name: 'explorer-grid-dark.png',
    depicts: ['packages/core/src/components/GridView.vue', 'packages/core/src/components/Toolbar.vue', 'packages/core/src/styles'],
  },
  {
    name: 'share-modal.png',
    depicts: ['packages/core/src/modals/PermissionsModal.vue'],
  },
  {
    name: 'viewer-markdown.png',
    depicts: ['packages/core/src/modals/PreviewModal.vue'],
  },
  {
    name: 'admin-dashboard.png',
    depicts: ['web/src/views/Dashboard.vue'],
  },
  {
    name: 'demo-landing.png',
    depicts: ['web/src/views/Login.vue'],
  },
  {
    name: 'connections-guide.png',
    depicts: ['packages/core/src/components/ConnectionGuideView.vue', 'packages/core/src/components/ConnectionsPanel.vue'],
  },
  {
    name: 'admin-plugins.png',
    // The storage-plugins table moved into a tab when Apps arrived; the page
    // shell draws the tab strip, the tab draws what the picture shows.
    depicts: ['web/src/views/Plugins.vue', 'web/src/components/plugins/StoragePluginsTab.vue'],
  },
  {
    name: 'driveshell/driveshell-hero-1440.png',
    depicts: ['packages/core/src/FileExplorer.vue', 'packages/core/src/components/SideNav.vue'],
  },
  {
    name: 'driveshell/driveshell-search-1440.png',
    depicts: ['packages/core/src/components/FilterBar.vue', 'packages/core/src/components/CommandPalette.vue'],
  },
  {
    name: 'sidenav/sidenav-expanded-1440.png',
    depicts: ['packages/core/src/components/SideNav.vue'],
  },
  {
    name: 'sidenav/sidenav-rail-1440.png',
    depicts: ['packages/core/src/components/SideNav.vue'],
  },
  {
    name: 'sidenav/view-shared-1440.png',
    depicts: ['packages/core/src/components/SideNav.vue', 'packages/core/src/components/ListView.vue'],
  },
  {
    name: 'sidenav/embed-webcomponent-1440.png',
    depicts: ['packages/webcomponent/src'],
  },
  {
    name: 'sidenav/connect-1440.png',
    depicts: ['packages/core/src/components/ConnectionsPanel.vue'],
  },
  {
    name: 'sidenav/apikeys-minted-1440.png',
    depicts: ['packages/core/src/components/TokensPanel.vue'],
  },
  // ── v0.43.0: Apps, and the signing round that debuts them ──────────────
  // e2e/shots/signing.mjs, apps.mjs, appearance.mjs and symlinks.mjs.
  {
    name: 'signing/sign-define-1440.png',
    depicts: [
      'packages/core/src/components/plugin/nodes/SurfacePdfFields.vue',
      'packages/core/src/components/plugin/nodes/PdfFieldEditor.vue',
      'packages/core/src/components/plugin/PluginPageView.vue',
    ],
  },
  {
    name: 'signing/sign-place-1440.png',
    depicts: ['packages/core/src/components/plugin/nodes/SurfacePdfFields.vue', 'packages/core/src/components/plugin/PluginPageView.vue'],
  },
  {
    // The one screen a stranger ever sees. ⚠ It is a SHELL shared by every
    // outward-facing page, so a change to any of these moves the picture.
    name: 'public-share.png',
    depicts: [
      'packages/core/src/components/public/PublicShell.vue',
      'packages/core/src/components/public/PublicShareBody.vue',
      'packages/core/src/lib/publicLayout.ts',
    ],
  },
  {
    name: 'signing/sign-outside-pin-1440.png',
    depicts: ['packages/core/src/components/public/PublicPinGate.vue', 'packages/core/src/components/public/PublicShell.vue'],
  },
  {
    name: 'signing/sign-outside-fill-1440.png',
    depicts: [
      'packages/core/src/components/public/PublicLinkPage.vue',
      'packages/core/src/components/plugin/nodes/SurfaceSignaturePad.vue',
      'packages/core/src/components/plugin/nodes/SurfaceForm.vue',
    ],
  },
  {
    name: 'signing/sign-status-1440.png',
    depicts: ['packages/core/src/components/plugin/PluginInspectorSection.vue', 'packages/core/src/components/InspectorPanel.vue'],
  },
  {
    name: 'apps/apps-install-review-1440.png',
    depicts: ['web/src/components/plugins/AppPluginInstallWizard.vue'],
  },
  {
    name: 'apps/apps-detail-1440.png',
    depicts: ['web/src/components/plugins/AppPluginDetail.vue', 'web/src/components/plugins/AppPluginsTab.vue'],
  },
  {
    name: 'apps/convert-wizard-1440.png',
    depicts: [
      'packages/core/src/components/plugin/PluginViewModal.vue',
      'packages/core/src/components/plugin/nodes/SurfaceSteps.vue',
      'packages/core/src/components/ChoiceButtons.vue',
    ],
  },
  // ── v0.48.0: an app's own interface (e2e/shots/apps.mjs + board-app/) ──
  {
    name: 'apps/app-interface-review-1440.png',
    depicts: ['web/src/components/plugins/AppPluginInstallWizard.vue'],
  },
  {
    name: 'apps/app-interface-viewer-1440.png',
    depicts: ['packages/core/src/components/plugin/AppFrame.vue', 'packages/app-ui/src', 'e2e/shots/board-app'],
  },
  {
    name: 'signing/bell-badge-1440.png',
    depicts: ['packages/core/src/components/NotificationBell.vue', 'packages/core/src/components/UnreadBadge.vue'],
  },
  {
    name: 'signing/notifications-list-1440.png',
    depicts: ['packages/core/src/components/NotificationsPanel.vue'],
  },
  {
    name: 'signing/my-shares-1440.png',
    depicts: ['web/src/views/MyShares.vue', 'packages/core/src/components/RowActions.vue'],
  },
  {
    name: 'signing/admin-table-actions-1440.png',
    depicts: ['web/src/views/Shares.vue', 'packages/core/src/components/DataTable.vue', 'packages/core/src/components/RowActions.vue'],
  },
  {
    name: 'appearance/theme-editor-1440.png',
    depicts: ['web/src/views/Appearance.vue'],
  },
  {
    name: 'appearance/themed-explorer-1440.png',
    depicts: ['web/src/lib/instanceThemes.ts', 'packages/core/src/lib/themes.ts'],
  },
  {
    name: 'appearance/themed-signin-1440.png',
    depicts: ['web/src/views/Login.vue', 'web/src/lib/theme.ts'],
  },
  {
    name: 'symlinks/symlink-badge-1440.png',
    depicts: ['packages/core/src/components/ListView.vue', 'packages/core/src/components/InspectorPanel.vue'],
  },
  {
    // The Apps list itself, with a language pack in it — the row that says
    // what a pack is and how far it goes.
    name: 'langpack/apps-list-1440.png',
    depicts: ['web/src/components/plugins/AppPluginsTab.vue', 'web/src/components/plugins/AppPluginLanguages.vue'],
  },
  {
    // Two kinds of tag, under their own headings, with one opened.
    name: 'tags/tags-kinds-1440.png',
    depicts: ['web/src/views/TaggedFiles.vue', 'packages/core/src/components/TagKindIcon.vue', 'web/src/api/tags.ts'],
  },
  {
    // Trash: deleted-from, deleted-on and how long is left are columns of the
    // explorer's own table, filled by the shared "time left" sentence.
    name: 'sidenav/view-trash-1440.png',
    depicts: ['packages/core/src/components/ListView.vue', 'packages/core/src/lib/trashTimeLeft.ts'],
  },
  // ── v0.49.0: roles (e2e/shots/roles.mjs) ──────────────────────────────
  {
    // Admin → Roles: the built-in roles and the custom ones in the
    // explorer's table, each row's name read in the panel's language.
    name: 'roles/roles-list-1440.png',
    depicts: ['web/src/views/Roles.vue', 'web/src/lib/roleName.ts'],
  },
  // ── v0.50.0: groups, sign-in security, Default apps, folder previews
  //    (e2e/shots/groups.mjs, loginsecurity.mjs, defaultapps.mjs, thumbnails.mjs) ──
  {
    // Admin → Groups: each group's role, priority, members, folders and SSO
    // links in the explorer's table.
    name: 'groups/groups-list-1440.png',
    depicts: ['web/src/views/Groups.vue'],
  },
  {
    // The sharing panel: a group suggested beside people, Owner asked in the dialog.
    name: 'groups/share-group-1440.png',
    depicts: ['packages/core/src/modals/PermissionsModal.vue'],
  },
  {
    name: 'loginsecurity/login-security-1440.png',
    depicts: ['web/src/views/LoginSecurity.vue', 'web/src/components/loginSecurity/AddressListEditor.vue'],
  },
  {
    // The sign-in form on a locked account: the message and the button counting down.
    name: 'loginsecurity/login-locked-1440.png',
    depicts: ['web/src/views/Login.vue'],
  },
  {
    // Plugins → Default apps: every kind something besides filex handles, one with a rule.
    name: 'defaultapps/default-apps-1440.png',
    depicts: ['web/src/components/plugins/DefaultAppsTab.vue'],
  },
  {
    // The grid over folders drawn with the files that came into them last,
    // the SVGs drawn by the built-in engine.
    name: 'thumbnails/folders-grid-1440.png',
    depicts: ['packages/core/src/components/FolderMosaic.vue', 'packages/core/src/components/ThumbTile.vue'],
  },
  // ── v0.51.0: the admin menu, who may encrypt, a .csv in ONLYOFFICE
  //    (e2e/shots/megamenu.mjs, encryption.mjs, csvoffice.mjs) ──
  {
    // The People & security panel open over Admin → Users: sections, a line
    // under every page, the page you are on.
    name: 'megamenu/people-panel-1440.png',
    depicts: ['packages/core/src/components/MegaMenu.vue', 'web/src/lib/adminNav.ts', 'web/src/components/TopNav.vue'],
  },
  {
    // Admin → Encryption: the policy card and the requests waiting.
    name: 'encryption/admin-encryption-1440.png',
    depicts: ['web/src/views/Encryption.vue', 'web/src/components/encryption/EncryptionPolicyCard.vue', 'web/src/components/encryption/EncryptionRequestsPanel.vue'],
  },
  {
    // The New folder dialog's request for an encrypted folder, a reason written.
    name: 'encryption/request-new-folder.png',
    depicts: ['packages/core/src/components/E2eRequestModal.vue', 'packages/core/src/modals/NewFolderModal.vue'],
  },
  {
    // A semicolon CSV in ONLYOFFICE's spreadsheet, a look first.
    name: 'csvoffice/csv-view-1440.png',
    depicts: ['packages/core/src/modals/PreviewModal.vue', 'backend/internal/onlyoffice/csv.go'],
  },
  {
    // The editor tab with the note on what a save as CSV keeps.
    name: 'csvoffice/csv-edit-1440.png',
    depicts: ['packages/core/src/modals/PreviewModal.vue', 'backend/internal/onlyoffice/csv.go'],
  },
  // ── v0.52.0: installing from a store, a paid app's license (e2e/shots/store.mjs) ──
  {
    // The install review opened from a store's link, marked From store.
    name: 'store/store-review-1440.png',
    depicts: ['web/src/components/plugins/AppPluginInstallWizard.vue', 'web/src/views/StoreInstall.vue'],
  },
  {
    // The first link from a store: its address and its keys' fingerprints.
    name: 'store/store-trust-1440.png',
    depicts: ['web/src/views/StoreInstall.vue'],
  },
  {
    // A license the store revoked: the app held, the band on an admin page.
    name: 'store/store-license-held-1440.png',
    depicts: ['web/src/components/AppLicenseAlert.vue', 'web/src/components/plugins/AppPluginsTab.vue'],
  },
];
