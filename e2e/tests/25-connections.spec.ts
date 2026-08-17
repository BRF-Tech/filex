/**
 * The connections surface — measured in a browser, against a real server.
 *
 * What it proves, in the order the user meets it:
 *
 *   1. the storages the caller may see are listed;
 *   2. a storage can be CREATED from the shared component, with a field
 *      set the backend descriptor supplied (not one this test hardcodes),
 *      and the server really has it afterwards;
 *   3. the "how to connect" page names the deployment it is served from,
 *      and its copy button really puts that on the clipboard;
 *   4. a NON-admin gets an honest "ask your administrator" state and the
 *      instructions anyway — not a form whose submit would 403.
 *
 * ⚠ The desktop half of this feature is measured separately, by driving
 * the real Electron app: `node desktop/scripts/connections-e2e.mjs`. Both
 * mount the same component; neither surface is taken on trust from the
 * other.
 */
import { test, expect } from '@playwright/test';
import { loginAs, apiLogin, ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';

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

test.describe('storage connections', () => {
  test.beforeAll(async ({ request }) => {
    await dropTestStorage(request);
  });

  test.afterAll(async ({ request }) => {
    await dropTestStorage(request);
  });

  test('lists storages and creates one from the shared component', async ({ page, request }) => {
    await loginAs(page);
    await page.goto('/admin/connections');
    await dismissInstallBanner(page);

    const panel = page.getByTestId('connections-panel');
    await expect(panel).toBeVisible();
    // The list is rendered for an admin (empty or not) and the add button
    // is there — the two things a non-admin does NOT get.
    await expect(page.getByTestId('storage-list')).toBeAttached();
    await expect(page.getByTestId('storage-add')).toBeVisible();

    await page.getByTestId('storage-add').click();
    await expect(page.getByTestId('storage-form')).toBeVisible();

    await page.getByTestId('storage-name').fill(STORAGE_NAME);
    await page.getByTestId('storage-driver').selectOption('local');

    // ⚠ The path field is whatever the DRIVER declared, found by its
    // descriptor-generated id — not a selector this test invented. If the
    // descriptor stopped shipping the field, this fails, which is the
    // point.
    const pathField = page.locator('#fe-cf-path');
    await expect(pathField).toBeVisible();
    await pathField.fill(MOUNT);

    await dismissInstallBanner(page);
    await page.getByTestId('storage-test').click();
    await expect(page.getByTestId('test-result')).toBeVisible({ timeout: 15_000 });

    await page.getByTestId('storage-save').click();
    await expect(page.getByTestId('storage-form')).toBeHidden({ timeout: 15_000 });
    await expect(page.getByTestId('storage-list')).toBeVisible();
    await expect(page.getByTestId(`storage-edit-${STORAGE_NAME}`)).toBeVisible();

    // The server, not the screen, is the authority on whether it saved.
    // ⚠ Its own session: the per-test `request` fixture is a fresh context,
    // so the beforeAll login does not carry into it.
    await apiLogin(request);
    const list = await request.get('/api/admin/storages');
    expect(list.ok()).toBeTruthy();
    const items: Array<{ name: string; driver: string; config: Record<string, unknown> }> =
      await list.json();
    const made = items.find((s) => s.name === STORAGE_NAME);
    expect(made).toBeTruthy();
    expect(made?.driver).toBe('local');
    expect(made?.config?.path).toBe(MOUNT);
  });

  test('the instruction page names this deployment, and the copy button works', async ({
    page,
    context,
    baseURL,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await loginAs(page);
    await page.goto('/admin/connections');
    await page.getByTestId('tab-connect').click();

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
    await page.getByTitle(/Language|Dil/i).click();
    await page.getByRole('menuitem', { name: 'Türkçe' }).click();
    await page.waitForTimeout(500);

    const panel = page.getByTestId('connections-panel');
    await expect(panel).toContainText('Depo bağlantıları');
    await expect(page.getByTestId('tab-storages')).toHaveText('Depolar');
    await expect(page.getByTestId('tab-connect')).toHaveText('Nasıl bağlanılır');

    // The descriptor-driven labels come from the SAME i18n keys the backend
    // names, so they must be Turkish too — the whole point of copying the
    // catalogue rather than inventing a second one.
    await page.getByTestId('storage-add').click();
    await page.getByTestId('storage-driver').selectOption('s3');
    await expect(page.getByTestId('storage-form')).toContainText('Önek (prefix)');
    await expect(page.getByTestId('storage-form')).toContainText('Gelişmiş ayarlar');

    // And the generated instructions.
    await page.getByTestId('tab-connect').click();
    await expect(page.getByTestId('guide-facts')).toContainText('Kullanıcı adı');
    await expect(page.locator('.fe-guide__body')).toContainText('Dosya Gezgini');

    // Put it back so the next test in this file is not surprised.
    await page.getByTitle(/Language|Dil/i).click();
    await page.getByRole('menuitem', { name: 'English' }).click();
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
    await page.getByTestId('tab-connect').click();
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

    // The key is listed, and revoking it takes a confirmation.
    const row = keys.locator('tbody tr', { hasText: akid });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'Revoke' }).click();
    await row.getByRole('button', { name: 'Sure?' }).click();
    await expect(keys.locator('tbody tr', { hasText: akid })).toHaveCount(0);
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
    await page.getByTestId('tab-connect').click();
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

    const row = keys.locator('tbody tr', { hasText: 'e2e laptop' });
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
    await row.getByRole('button', { name: 'Remove' }).click();
    await row.getByRole('button', { name: 'Sure?' }).click();
    await expect(keys.locator('tbody tr', { hasText: 'e2e laptop' })).toHaveCount(0);
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
    await page.getByTestId('tab-connect').click();
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
    await page.getByTestId('tab-connect').click();
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

    const row = panel.locator('tbody tr', { hasText: 'e2e media player' });
    await expect(row).toBeVisible();
    await expect(row).toContainText('read-only');
    await row.getByRole('button', { name: 'Revoke' }).click();
    await row.getByRole('button', { name: 'Sure?' }).click();
    await expect(panel.locator('tbody tr', { hasText: 'e2e media player' })).toHaveCount(0);
  });

  test('a non-admin is told to ask an administrator, and still gets the guide', async ({
    page,
    request,
  }) => {
    await apiLogin(request);
    // Best-effort create; a rerun against the same DB already has them.
    await request.post('/api/admin/users', {
      data: { email: USER_EMAIL, password: USER_PASSWORD, role: 'user' },
    });

    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(USER_EMAIL);
    await page.getByLabel(/password|şifre/i).fill(USER_PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    // Non-admins are bounced out of the panel to the chrome-less explorer.
    await page.waitForURL(/\/explore/, { timeout: 15_000 });

    await dismissInstallBanner(page);
    await page.getByTestId('explore-connect').click();
    const panel = page.getByTestId('connections-panel');
    await expect(panel).toBeVisible();

    // It opens on the half they can use…
    await expect(page.getByTestId('guide-facts')).toBeVisible();
    await expect(page.getByTestId('guide-facts')).toContainText(USER_EMAIL);

    // …and the other half says why, instead of showing a dead form.
    await page.getByTestId('tab-storages').click();
    await expect(page.getByTestId('no-admin')).toBeVisible();
    await expect(page.getByTestId('storage-form')).toHaveCount(0);
    await expect(page.getByTestId('storage-add')).toHaveCount(0);

    // ⚠⚠ And the thing that was actually broken: a non-admin has to be able to
    // mint the credential the guide tells them to use. FTPS signs in with an
    // API token, and until 2026-08-17 the only screen that could make one was
    // the admin panel — so this user read "use an API token as the password"
    // and had nowhere to go. Being an admin hid the gap completely.
    await page.getByTestId('tab-connect').click();
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
    await page.getByTestId('tab-connect').click();

    // ⚠ The picker shows a NAME, not the id upper-cased — "MOUNT" would be a
    // label for a thing that is not a protocol, in a list where the rest are.
    const picker = page.getByTestId('guide-protocol');
    await expect(picker.locator('option[value="mount"]')).toHaveText('filex mount');
    await picker.selectOption('mount');

    const facts = page.getByTestId('guide-facts');
    await expect(facts).toContainText(/API token/i);
    // The token is never echoed: filex does not have its plaintext.
    await expect(facts).toContainText(/create one under Tokens/i);

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
    await page.getByTestId('tab-connect').click();

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

    // Revoking removes it from the list.
    await page.getByTestId('api-tokens').getByRole('button', { name: /revoke/i }).first().click();
    await page.getByTestId('api-tokens').getByRole('button', { name: /sure/i }).first().click();
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
