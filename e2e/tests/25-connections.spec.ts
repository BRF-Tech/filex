/**
 * The connections surface — measured in a browser, against a real server.
 *
 * What it proves, in the order the user meets it:
 *
 *   1. the surface is the guides and NOTHING else — no tab strip, no storage
 *      list, no storage form (v0.43.0 removed that half; see below);
 *   2. the "how to connect" page names the deployment it is served from,
 *      and its copy button really puts that on the clipboard;
 *   3. a NON-admin gets the instructions and can mint the credential they
 *      name — the half they actually need.
 *
 * ⚠⚠ This file used to open by CREATING a storage through the panel, and the
 * tests below still need that storage to exist (see the non-admin test's
 * header: the explorer only mounts for a caller who can see one). The panel
 * cannot create it any more, so `makeTestStorage` does it through the API in
 * `beforeAll`. Do not "restore" the UI version — the owner removed that half
 * on purpose: "bu depolar sekmesine hiç ihtiyaç yok". Creating a storage
 * through the UI is 20-storage.spec.ts's job, on Admin → Storages.
 *
 * ⚠ The desktop half of this feature is measured separately, by driving
 * the real Electron app: `node desktop/scripts/connections-e2e.mjs`. Both
 * mount the same component; neither surface is taken on trust from the
 * other.
 */
import { test, expect } from '@playwright/test';
import { loginAs, apiLogin, ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';
// panel:tek-satir — the four panels below moved onto the shared admin table,
// and a row's verbs moved with them: one pinned "Actions" control per row
// holding what used to be loose `Revoke` / `Remove` buttons. The menu
// teleports to <body>, so it cannot be reached through the row at all.
import { confirmRowAction } from '../helpers/rowMenu';

const STORAGE_NAME = 'conn-e2e';
const USER_EMAIL = 'conn-viewer@local';
const USER_PASSWORD = 'conn-viewer-pass';
/** Where the created storage points. Must exist on the SERVER's filesystem. */
const MOUNT = process.env.E2E_CONN_MOUNT ?? '/tmp/filex-conn-e2e';

/** The PWA install banner is fixed to the bottom of the viewport and eats
 *  clicks aimed at anything under it. Nothing to do with this feature —
 *  it just has to be out of the way before a form can be driven. */
async function dismissInstallBanner(page: import('@playwright/test').Page) {
  const btn = page.getByTestId('pwa-install-dismiss');
  if (await btn.isVisible().catch(() => false)) await btn.click().catch(() => {});
}

/**
 * Remove the storage this file creates.
 *
 * ⚠ This has to run AFTER as well as before. A storage left behind is not
 * inert: with more than one storage the explorer leaves single-storage mode,
 * and `canGoUp` then correctly reports a parent at the storage root — so
 * 75-navigation's "parent-dir button is hidden at storage root" starts failing
 * in a file that never touched connections. Measured 2026-08-15: that test
 * passed on the base build, failed on this one, and passed again the moment
 * `conn-e2e` was deleted. A spec that does not clean up does not fail itself;
 * it fails somebody else, later, and looks like a product regression.
 */
async function dropTestStorage(request: import('@playwright/test').APIRequestContext) {
  await apiLogin(request);
  const list = await request.get('/api/admin/storages');
  if (!list.ok()) return;
  const items: Array<{ id: number; name: string }> = await list.json();
  for (const it of items) {
    if (it.name === STORAGE_NAME) await request.delete(`/api/admin/storages/${it.id}`);
  }
}

/**
 * Switch the signed-in account's language the way a person does: the user
 * settings dialog, opened over the current admin page by its `?settings=1`
 * deep link, then closed again.
 */
async function switchLanguage(page: import('@playwright/test').Page, code: 'en' | 'tr') {
  const here = new URL(page.url());
  await page.goto(`${here.pathname}?settings=1`);
  await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
  await page.getByTestId('user-settings-tab-preferences').click();
  await page.getByTestId(`user-settings-locale-${code}`).click();
  await expect(page.getByTestId(`user-settings-locale-${code}`)).toHaveAttribute('aria-pressed', 'true');
  await page.getByTestId('user-settings-close').click();
  await expect(page.getByTestId('user-settings-dialog')).toBeHidden();
}

/**
 * The storage the rest of this file borrows, made the only way that is left:
 * the admin API. ⚠ Not a convenience — the explorer (and therefore
 * `sidenav-connect`, the non-admin test's door) is only mounted for a caller
 * who can see at least one storage.
 */
async function makeTestStorage(request: import('@playwright/test').APIRequestContext) {
  await apiLogin(request);
  // ⚠ `enabled` and `read_only` explicitly, the same body the panel's own form
  // used to send. Leaving `enabled` off creates a storage nobody can see, and
  // the failure lands two tests later as "sidenav not found" — the explorer
  // does not mount for a caller with no visible storage.
  const made = await request.post('/api/admin/storages', {
    data: {
      name: STORAGE_NAME,
      driver: 'local',
      config: { path: MOUNT },
      read_only: false,
      enabled: true,
    },
  });
  expect(
    made.ok(),
    `could not create ${STORAGE_NAME}: ${made.status()} ${await made.text()}`,
  ).toBeTruthy();
}

test.describe('storage connections', () => {
  test.beforeAll(async ({ request }) => {
    await dropTestStorage(request);
    await makeTestStorage(request);
  });

  test.afterAll(async ({ request }) => {
    await dropTestStorage(request);
  });

  /**
   * ⚠⚠ The Storages half is GONE, on BOTH doors into this component.
   *
   * The owner, testing v0.43.0: "nasıl bağlanılır kısmında ve adminde
   * bağlantılar sayfası (aynılar zaten biliyorum) ikisinde de depolar
   * gözüküyor bu depolar sekmesine hiç ihtiyaç yok. kaldıralım." It was a
   * poorer copy of Admin → Storages — no sync mode, no RBAC, no drift —
   * standing in front of the screen that answers the other question.
   *
   * This asserts ABSENCE, which is the kind of test that rots quietly, so it
   * asserts the survivor in the same breath: if the panel ever fails to render
   * at all, the guide assertion fails rather than the whole test passing
   * because nothing is on screen.
   */
  test('the panel is the guides only — no tab strip, no storage form', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);

    const panel = page.getByTestId('connections-panel');
    await expect(panel).toBeVisible();
    // The survivor, on screen without anything being clicked.
    await expect(page.getByTestId('guide-protocol')).toBeVisible();

    for (const gone of [
      'tab-storages',
      'tab-connect',
      'storage-list',
      'storage-add',
      'storage-form',
      'no-admin',
    ]) {
      await expect(
        page.getByTestId(gone),
        `${gone} is still on the connections page`,
      ).toHaveCount(0);
    }
    // Not even an empty strip left behind. ⚠ `.fe-conn__tabs`, the panel's OWN
    // strip — not `[role="tablist"]`, which also matches the guide's Windows /
    // macOS / Linux tabs further down the page, and those are the feature.
    await expect(panel.locator('.fe-conn__tabs')).toHaveCount(0);

    // ⚠ And the door out is named: this page sends a reader who wanted the
    // storage form to the pages that have it. Without this the removal is a
    // dead end rather than a move.
    const advanced = page.getByRole('button', { name: 'Manage storages' });
    await expect(advanced).toBeVisible();
    await advanced.click();
    await expect(page).toHaveURL(/\/admin\/storages/);
  });

  test('the instruction page names this deployment, and the copy button works', async ({
    page,
    context,
    baseURL,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await loginAs(page);
    await page.goto('/admin/connections');
    await expect(page.getByTestId('connections-panel')).toBeVisible();

    const facts = page.getByTestId('guide-facts');
    await expect(facts).toBeVisible();

    // The real host, not a documentation placeholder.
    const host = new URL(baseURL ?? 'http://localhost:5212').host;
    await expect(facts).toContainText(`${new URL(baseURL!).origin}/dav/`);
    await expect(facts).toContainText(host);
    // The caller's own account is the WebDAV username.
    await expect(facts).toContainText(ADMIN_EMAIL);

    // Copy really copies — read back by PASTING, not by asking the
    // clipboard API. ⚠ This suite runs against a plain-http origin, which
    // is not a secure context, so `navigator.clipboard` is undefined there
    // and the component falls back to execCommand. Reading through the API
    // would have measured a path this deployment never takes.
    await page.getByTestId('copy-fact-0').click();
    await expect(page.getByTestId('copy-fact-0')).toHaveText(/Copied|Kopyalandı/);
    await page.evaluate(() => {
      const el = document.createElement('input');
      el.id = 'paste-probe';
      document.body.appendChild(el);
      el.focus();
    });
    await page.keyboard.press('ControlOrMeta+V');
    const clip = await page.evaluate(() => {
      const el = document.getElementById('paste-probe') as HTMLInputElement | null;
      const v = el?.value ?? '';
      el?.remove();
      return v;
    });
    expect(clip).toContain('/dav/');

    // Every client tab renders, and the Windows one carries the registry
    // lines that are the difference between "it works" and "filex is
    // broken at 47.7 MB".
    for (const id of ['windows', 'macos', 'linux', 'rclone', 'cyberduck']) {
      await expect(page.getByTestId(`guide-tab-${id}`)).toBeVisible();
    }
    await page.getByTestId('guide-tab-windows').click();
    await expect(page.locator('.fe-guide__body')).toContainText('FileSizeLimitInBytes');
    await expect(page.locator('.fe-guide__body')).toContainText('net use');

    await page.getByTestId('guide-tab-rclone').click();
    await expect(page.locator('.fe-guide__body')).toContainText('type = webdav');
    await expect(page.locator('.fe-guide__body')).toContainText(`url = ${new URL(baseURL!).origin}/dav`);
  });

  test('the panel is translated on SCREEN, not just in its config', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toContainText('Storage connections');

    // ⚠ Assert the TEXT, never the component's locale property. A
    // property-level check passed 10/10 in v0.19.0 while the screen was
    // still in the other language, because the component merges
    // `{...attributes, ...config}` and config wins — the property said
    // what nothing rendered from.
    // ⚠ Language lives in user settings since 0.41.0 — the header's own
    // switcher is gone, so no preference has two controls. The dialog opens
    // over this page and the panel behind it must follow without a reload.
    await switchLanguage(page, 'tr');

    const panel = page.getByTestId('connections-panel');
    await expect(panel).toContainText('Depo bağlantıları');
    // ⚠ The tab strip used to be measured here (Depolar / Nasıl bağlanılır).
    // It went with the storage half; the protocol picker is what the panel
    // opens on now, and its label is the one that has to be Turkish.
    await expect(panel).toContainText('Protokol');

    // And the generated instructions.
    await expect(page.getByTestId('guide-facts')).toContainText('Kullanıcı adı');
    await expect(page.locator('.fe-guide__body')).toContainText('Dosya Gezgini');

    // Put it back so the next test in this file is not surprised.
    await switchLanguage(page, 'en');
  });


  /**
   * The S3 half: mint a key, and watch every command on the page rewrite
   * itself around it.
   *
   * ⚠⚠ The endpoint in those commands is the one the SERVER computed, not one
   * the page assembled from its own origin. They are different hosts whenever
   * a dedicated S3 host is configured, and a client pointed at the application
   * root reaches the web app — which is how the first real-client run failed,
   * with rclone parsing an HTML redirect as XML.
   */
  test('an S3 key can be minted, fills in the guide, and can be revoked', async ({
    page,
    baseURL,
  }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toBeVisible();
    await page.getByTestId('guide-protocol').selectOption('s3');

    const keys = page.getByTestId('s3-keys');
    await expect(keys).toBeVisible();

    // Before there is a key, the guide says so rather than printing something
    // credential-shaped that authenticates as nothing.
    await expect(page.getByTestId('guide-facts')).toContainText('create a key above');

    await page.getByTestId('s3-key-label').fill('e2e laptop backup');
    await page.getByTestId('s3-key-mint').click();

    // The secret, exactly once.
    const secretBox = page.getByTestId('s3-key-secret');
    await expect(secretBox).toBeVisible();
    const akid = ((await secretBox.locator('code').first().innerText()) ?? '').trim();
    expect(akid).toMatch(/^FLX[A-Z0-9]+$/);

    // …and the commands now carry it, with the endpoint the server named.
    const endpoint = `${new URL(baseURL!).origin}/s3`;
    await expect(page.getByTestId('guide-facts')).toContainText(akid);
    await expect(page.getByTestId('guide-facts')).toContainText(endpoint);

    await page.getByTestId('guide-tab-rclone').click();
    const body = page.locator('.fe-guide__body');
    await expect(body).toContainText('type = s3');
    await expect(body).toContainText(`endpoint = ${endpoint}`);
    await expect(body).toContainText(`access_key_id = ${akid}`);
    // No dedicated S3 host on this deployment, so path style is not optional.
    await expect(body).toContainText('force_path_style = true');

    await page.getByTestId('guide-tab-restic').click();
    await expect(body).toContainText(`RESTIC_REPOSITORY="s3:${endpoint}/`);

    // The key is listed, and revoking it takes a confirmation. ⚠ The verb is
    // a named entry in the row's one Actions menu now, and the confirmation
    // is a second trip through it — destroying a credential did not get any
    // cheaper in the move, which is the part worth measuring.
    const row = keys.locator('.fe-list__row', { hasText: akid });
    await expect(row).toBeVisible();
    await confirmRowAction(row, 'Revoke');
    await expect(keys.locator('.fe-list__row', { hasText: akid })).toHaveCount(0);
  });


  /**
   * The SFTP half: register a key and watch the commands name this deployment.
   *
   * ⚠ The box itself is the feature. `ssh-copy-id` appends to
   * ~/.ssh/authorized_keys over a shell and filex has none, so without a place
   * to paste a public key nobody can use one — and everybody sends their
   * account password to a file server instead.
   */
  test('an SSH key can be registered, and the SFTP guide names this server', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toBeVisible();
    await page.getByTestId('guide-protocol').selectOption('sftp');

    const keys = page.getByTestId('ssh-keys');
    await expect(keys).toBeVisible();

    // A key that is not one is refused with the reason, not swallowed.
    await page.getByTestId('ssh-key-input').fill('this is not a public key');
    await page.getByTestId('ssh-key-add').click();
    await expect(keys).toContainText(/not a valid public key|key type/i);

    // A real one is accepted, and shows the fingerprint OpenSSH prints.
    const pub =
      'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIH3EJ0kR1e0lMDyDhbNRTyBAEXVhkgIzO2WVL4NA5cA e2e@filex';
    await page.getByTestId('ssh-key-input').fill(pub);
    await page.getByTestId('ssh-key-name').fill('e2e laptop');
    await page.getByTestId('ssh-key-add').click();

    const row = keys.locator('.fe-list__row', { hasText: 'e2e laptop' });
    await expect(row).toBeVisible();
    await expect(row).toContainText('SHA256:');

    // The guide names the real port and the account's own login, not a
    // placeholder and not the web port.
    const facts = page.getByTestId('guide-facts');
    await expect(facts).toContainText('2022');
    await page.getByTestId('guide-tab-openssh').click();
    await expect(page.locator('.fe-guide__body')).toContainText('sftp -P 2022');
    await page.getByTestId('guide-tab-rclone').click();
    await expect(page.locator('.fe-guide__body')).toContainText('type = sftp');
    // ⚠ The two settings rclone needs because filex has no shell. Without them
    // every run is buried in warnings about a missing md5sum.
    await expect(page.locator('.fe-guide__body')).toContainText('shell_type = none');

    // Remove it again, so the next run starts from the same place.
    await confirmRowAction(row, 'Remove');
    await expect(keys.locator('.fe-list__row', { hasText: 'e2e laptop' })).toHaveCount(0);
  });


  /**
   * The FTPS guide. There is no credential to mint here — FTP authenticates
   * with the account password or an API token — so what has to be right is the
   * DEPLOYMENT: the port, the passive range and the fact that TLS is required.
   *
   * ⚠ The passive range is in this test because leaving it out of the guide is
   * how an FTP deployment fails: a firewall that blocks it makes every transfer
   * hang with no error on either side, and nobody can guess the range from the
   * client end.
   */
  test('the FTPS guide names the port, the passive range and the TLS requirement', async ({
    page,
    request,
  }) => {
    // ⚠ The port is asked of the SERVER rather than hardcoded: the harness
    // binds `:0`, so the only place the real port exists is the running
    // listener. A test that asserted a constant would either be measuring the
    // config (which says 0) or pinning a port the product does not have to use.
    await apiLogin(request);
    const facts0 = await (await request.get('/api/auth/ssh-keys')).json();
    const ftpsPort = String(facts0.ftps?.port ?? '');
    expect(ftpsPort, 'the server reported no FTPS port').not.toBe('');
    expect(ftpsPort).not.toBe('0');

    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toBeVisible();
    await page.getByTestId('guide-protocol').selectOption('ftps');

    const facts = page.getByTestId('guide-facts');
    await expect(facts).toContainText(ftpsPort);
    await expect(facts).toContainText(/explicit TLS/i);
    await expect(facts).toContainText('30000-30100');

    // No SSH key box on this page: FTP will never look at one.
    await expect(page.getByTestId('ssh-keys')).toHaveCount(0);

    await page.getByTestId('guide-tab-curl').click();
    const body = page.locator('.fe-guide__body');
    await expect(body).toContainText('--ssl-reqd');
    await page.getByTestId('guide-tab-lftp').click();
    await expect(body).toContainText('ssl-force true');
    await expect(body).toContainText('ssl-protect-data true');
  });


  /**
   * The NFS half. What has to be right here is unusual enough to test on
   * screen: the export PATH is the credential, so the panel must say so, show
   * it exactly once, and produce a mount line that carries both `port=` and
   * `mountport=` — without either, the mount hangs with no error.
   */
  test('an NFS export can be created, shows its path once, and can be revoked', async ({
    page,
    request,
  }) => {
    await apiLogin(request);
    const facts0 = await (await request.get('/api/auth/nfs-exports')).json();
    const nfsPort = String(facts0.port ?? '');
    expect(nfsPort, 'the server reported no NFS port').not.toBe('');

    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toBeVisible();
    await page.getByTestId('guide-protocol').selectOption('nfs');

    const panel = page.getByTestId('nfs-exports');
    await expect(panel).toBeVisible();
    // ⚠ The warning is part of the feature: a path in /etc/fstab is a password
    // in a world-readable file, and nobody reads a document about it.
    await expect(panel).toContainText(/path IS the password/i);

    // Before there is an export, the guide says so rather than printing
    // something path-shaped that would fail with no clue why.
    await expect(page.getByTestId('guide-facts')).toContainText('create an export above');

    await page.getByTestId('nfs-label').fill('e2e media player');
    await page.getByTestId('nfs-mint').click();

    const shown = page.getByTestId('nfs-path');
    await expect(shown).toBeVisible();
    const line = ((await shown.locator('code').first().innerText()) ?? '').trim();
    expect(line).toContain('mount -t nfs');
    expect(line).toContain(`port=${nfsPort}`);
    expect(line, 'without mountport= the mount hangs with no error').toContain(
      `mountport=${nfsPort}`,
    );
    expect(line, 'read-only is the default for a machine').toContain(',ro');
    expect(line).toMatch(/\/x\/[0-9a-f]{64}/);

    // The guide is now filled in with the same path.
    const path = line.match(/\/x\/[0-9a-f]{64}/)?.[0] ?? '';
    await expect(page.getByTestId('guide-facts')).toContainText(path);

    const row = panel.locator('.fe-list__row', { hasText: 'e2e media player' });
    await expect(row).toBeVisible();
    await expect(row).toContainText('read-only');
    await confirmRowAction(row, 'Revoke');
    await expect(panel.locator('.fe-list__row', { hasText: 'e2e media player' })).toHaveCount(0);
  });

  test('a non-admin gets the guide, and can mint the credential it names', async ({
    page,
    request,
  }) => {
    await apiLogin(request);
    // Best-effort create; a rerun against the same DB already has them.
    await request.post('/api/admin/users', {
      data: { email: USER_EMAIL, password: USER_PASSWORD, role: 'user' },
    });

    // ⚠ THIS TEST BORROWS THE STORAGE THE FIRST ONE CREATES, and the borrow
    // is what makes it lie when something else in this file breaks. The door
    // below (`sidenav-connect`) lives inside the explorer, and the explorer is
    // only mounted when the caller can see at least one storage — a non-admin
    // with none gets the bare "nothing has been shared with you" screen and no
    // navigation panel at all.
    //
    // Playwright restarts its worker after ANY failed test, and a new worker
    // re-runs this describe's `beforeAll` — which is `dropTestStorage`. So an
    // earlier failure in this file deletes `conn-e2e` out from under this test
    // and it then fails with "sidenav not found", which reads like a second,
    // separate regression and is not one. Measured 2026-09-20: three revoke
    // buttons moved into a row menu, and this test went red with them.
    // If it fails alone, the finding is real; if it fails behind others in
    // this file, fix those first and look again.

    // gorunum:v2-topbar — the onboarding tour has to be off for this test now,
    // and this is not belt-and-braces: it was MEASURED.
    //
    // `FileExplorer` auto-starts the tour on a first mount with no
    // `filex.tourDone`, which is every fresh browser context — i.e. every run
    // of this file. The door this test uses moved from the Explore page's own
    // top bar (outside `.fe`, which the tour's card never covered) to the
    // explorer's navigation panel (inside it, which it does). Measured
    // 2026-09-12 against the live build: with the tour open, clicking
    // `sidenav-connect` times out — the card sits over it. Without this line
    // the selector swap below would simply have turned a green test red.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));

    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(USER_EMAIL);
    await page.getByLabel(/password|parola/i).fill(USER_PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    // Non-admins land in the explorer — on Home since 0.41.0.
    await page.waitForURL(/\/(home|explore)([?#]|$)/, { timeout: 15_000 });

    await dismissInstallBanner(page);
    // gorunum:v2-topbar — the door moved, the room did not.
    //
    // This used to click `explore-connect`, a button in the Explore page's own
    // top bar that opened the page's OWN copy of the connections overlay. That
    // bar is gone, and with it the second copy: the navigation panel has
    // carried `sidenav-connect` since gezinti:g1 and mounts the very same
    // `ConnectionsPanel` with the same `initial-tab="connect"` and `closable`,
    // so every assertion below is unchanged.
    // ⚠ Wait for the panel to exist first. `sidenav-connect` is inside the
    // explorer, which mounts only after storage discovery resolves — clicking
    // straight after the URL settles races that, where the old page-bar button
    // was in the page's own chrome and was there immediately.
    await expect(page.getByTestId('sidenav')).toBeVisible({ timeout: 25_000 });
    await page.getByTestId('sidenav-connect').click();
    const panel = page.getByTestId('connections-panel');
    await expect(panel).toBeVisible();

    // It opens on what they came for…
    await expect(page.getByTestId('guide-facts')).toBeVisible();
    await expect(page.getByTestId('guide-facts')).toContainText(USER_EMAIL);

    // …and there is no longer another half to be turned away from. Before
    // v0.43.0 this clicked a "Storages" tab to read "ask an administrator";
    // the honest version of that card is not showing a non-admin a console
    // they may not use at all.
    await expect(page.getByTestId('tab-storages')).toHaveCount(0);
    await expect(page.getByTestId('no-admin')).toHaveCount(0);
    await expect(page.getByTestId('storage-form')).toHaveCount(0);
    await expect(page.getByTestId('storage-add')).toHaveCount(0);

    // ⚠⚠ And the thing that was actually broken: a non-admin has to be able to
    // mint the credential the guide tells them to use. FTPS signs in with an
    // API token, and until 2026-08-17 the only screen that could make one was
    // the admin panel — so this user read "use an API token as the password"
    // and had nowhere to go. Being an admin hid the gap completely.
    await expect(page.getByTestId('connections-panel')).toBeVisible();
    await page.getByTestId('guide-protocol').selectOption('ftps');
    await expect(page.getByTestId('api-tokens')).toBeVisible();

    await page.getByTestId('token-mint').click();
    const secret = page.getByTestId('token-secret');
    await expect(secret).toBeVisible();
    const shown = (await secret.locator('code').innerText()).trim();

    // It authenticates AS THEM — not as the admin who happens to be nearby.
    const who = await page.request.get('/api/auth/me', {
      headers: { Authorization: `Bearer ${shown}` },
    });
    expect(who.status(), 'a non-admin minted a token the API refused').toBeLessThan(400);
    expect(JSON.stringify(await who.json())).toContain(USER_EMAIL);

    // ── baglan:b1 — and now the same person with NOTHING ────────────────
    //
    // ⚠⚠ Everything above went through `sidenav-connect`, which is inside the
    // explorer — and the explorer is only mounted when the caller can see at
    // least one storage. That is the borrow this test's header warns about,
    // and it was also hiding the other half of the gap the ⚠⚠ block above says
    // was closed on 2026-08-17: a brand-new account, and this same account the
    // moment its grant is revoked, gets the bare "nothing has been shared with
    // you" screen with no navigation panel at all — so it is told to ask an
    // administrator AND cannot read how to connect or mint the token the guide
    // tells it to use. Being handed a storage hid the zero-storage case
    // exactly the way being an admin hid the first one.
    //
    // So: take the storage away from under a live session and reload. The door
    // that has to be there is a row in the empty screen's own account menu
    // (Explore.vue → `emptyStateActions`), the same mechanism
    // "Paylaştıklarım" uses, opening the SAME ConnectionsPanel.
    //
    // ⚠ The drop is safe for what follows: nothing later in this file uses
    // `conn-e2e`, and `afterAll` drops it anyway. It is done HERE rather than
    // in a test of its own because this is the one session in the file signed
    // in as a non-admin — a separate test would have to log in again, and a
    // worker restart would re-run `beforeAll` and drop the storage under it.
    await dropTestStorage(request);
    await page.reload();
    await dismissInstallBanner(page);

    // No explorer, no navigation panel: the promise now rests entirely on the
    // empty screen's own cluster.
    const empty = page.getByTestId('explore-empty-actions');
    await expect(empty).toBeVisible({ timeout: 25_000 });
    await expect(page.getByTestId('sidenav')).toHaveCount(0);

    await page.getByTestId('explore-account').click();
    // ⚠ `explore-connections`, the testid AccountMenu derives from the row's
    // KEY — not its label, which changes with the viewer's language.
    const guideRow = page.getByTestId('explore-connections');
    await expect(guideRow, 'no way to the connections guide with no storage').toBeVisible();
    await guideRow.click();

    // The package's own panel, opened on the half this person can use.
    const emptyPanel = page.getByTestId('connections-panel');
    await expect(emptyPanel).toBeVisible();
    await expect(page.getByTestId('guide-facts')).toBeVisible();
    await expect(page.getByTestId('guide-facts')).toContainText(USER_EMAIL);

    // …and the credential the guide sends them for is mintable from here too,
    // which is why the door matters rather than just the page.
    await page.getByTestId('guide-protocol').selectOption('ftps');
    await expect(page.getByTestId('api-tokens')).toBeVisible();

    // It closes again and puts them back where they were, rather than trapping
    // them on a surface the empty screen has no back button for.
    await page.getByTestId('connections-close').click();
    await expect(emptyPanel).toHaveCount(0);
    await expect(empty).toBeVisible();
  });

  /**
   * `filex mount`. It is the one entry in this picker that is not a wire
   * protocol — filex's own binary is the client — and the two things the page
   * has to get right are the two things a reader would otherwise get wrong:
   * that it is NOT a sync, and that macOS is not supported.
   *
   * ⚠ The second one is a refusal, and a refusal stated on the page is the
   * whole point: the alternative is somebody installing it on a Mac and
   * discovering the command does nothing.
   */
  test('the filex mount guide names itself, and says where it does not work', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toBeVisible();

    // ⚠ The picker shows a NAME, not the id upper-cased — "MOUNT" would be a
    // label for a thing that is not a protocol, in a list where the rest are.
    const picker = page.getByTestId('guide-protocol');
    await expect(picker.locator('option[value="mount"]')).toHaveText('filex mount');
    await picker.selectOption('mount');

    const facts = page.getByTestId('guide-facts');
    await expect(facts).toContainText(/API key/i);
    // The token is never echoed: filex does not have its plaintext.
    await expect(facts).toContainText(/create one under API keys/i);

    const body = page.locator('.fe-guide__body');

    // ⚠ Each tab is clicked explicitly rather than assuming which one opens.
    // The guide picks the tab matching the VIEWER's platform, so the default
    // depends on the machine running the suite — asserting Linux content on an
    // unselected tab passed on CI and failed on a Windows workstation.
    await page.getByTestId('guide-tab-linux').click();
    await expect(body).toContainText('filex mount');
    await expect(body).toContainText('fusermount -u');

    // Windows is supported and says what to install…
    await page.getByTestId('guide-tab-windows').click();
    await expect(body).toContainText(/WinFsp/);
    await expect(body).toContainText('filex mount Z:');

    // …macOS is not, and the page REFUSES rather than implying. That is the
    // assertion worth having: the alternative is somebody installing it on a
    // Mac and discovering the command does nothing.
    await page.getByTestId('guide-tab-macos').click();
    await expect(body).toContainText(/macFUSE/);
    await expect(body).toContainText(/does not work on macOS|çalışmıyor/i);
  });

  /**
   * ⚠⚠ The gap this closes. FTPS, WebDAV and `filex mount` all take an API
   * token as the password — their guides say so in as many words — and until
   * 2026-08-17 the only place to mint one was the admin panel's own screen.
   * A normal user read the instruction and had nowhere to follow it.
   *
   * Asserted where it matters: the panel appears on the three protocols whose
   * credential IS a token, and NOT on the three that have their own.
   */
  test('an API token can be minted from the connections surface, and only where it is the credential', async ({
    page,
  }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('connections-panel')).toBeVisible();

    const picker = page.getByTestId('guide-protocol');
    const panel = page.getByTestId('api-tokens');

    // Present on the three that sign in with a token…
    for (const proto of ['ftps', 'webdav', 'mount']) {
      await picker.selectOption(proto);
      await expect(panel, `token panel missing on ${proto}`).toBeVisible();
    }
    // …and absent on the three that have a credential of their own, where it
    // would only invite minting one nobody needs.
    for (const proto of ['s3', 'sftp', 'nfs']) {
      await picker.selectOption(proto);
      await expect(panel, `token panel should not be on ${proto}`).toHaveCount(0);
    }

    await picker.selectOption('ftps');
    const label = `e2e-token-${Date.now()}`;
    await page.getByTestId('token-label').fill(label);
    await page.getByTestId('token-mint').click();

    // The secret is shown ONCE, in full, and it is the real thing.
    const secret = page.getByTestId('token-secret');
    await expect(secret).toBeVisible();
    const shown = (await secret.locator('code').innerText()).trim();
    expect(shown.length, `token looked wrong: ${shown}`).toBeGreaterThan(32);

    // It appears in the list under the name that was typed…
    await expect(page.getByTestId('api-tokens')).toContainText(label);

    // …and it actually authenticates. This is the assertion that matters: a
    // panel that mints something the server will not accept is worse than no
    // panel, and only a real request can tell the two apart.
    const probe = await page.request.get('/api/files/manager?action=index&path=', {
      headers: { Authorization: `Bearer ${shown}` },
    });
    expect(probe.status(), 'the minted token was refused by the API').toBeLessThan(400);

    // Revoking removes it from the list. ⚠ THIS row's own menu, found by the
    // name that was typed — the old `.first()` reached for whichever revoke
    // button happened to be first in the panel, which is not necessarily the
    // token this test minted.
    const tokenRow = page.getByTestId('api-tokens').locator('.fe-list__row', { hasText: label });
    await expect(tokenRow).toBeVisible();
    await confirmRowAction(tokenRow, 'Revoke');
    await expect(page.getByTestId('api-tokens')).not.toContainText(label);

    // ⚠ And the revoked token stops working — the panel promises exactly this
    // ("revoking stops a connection that is already open"), so it is measured
    // rather than asserted in prose.
    const after = await page.request.get('/api/files/manager?action=index&path=', {
      headers: { Authorization: `Bearer ${shown}` },
    });
    expect(after.status(), 'a revoked token still authenticated').toBeGreaterThan(399);
  });
});
