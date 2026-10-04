// Who may encrypt (0.51, PR #83, docs/E2E-ENCRYPTION.md → Who may encrypt):
// the approval policy, from both sides.
//
//   node e2e/shots/encryption.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/encryption/ (or SHOTS_OUT):
//
//   admin-encryption-1440.png  Admin → Encryption: the policy (approval) and
//                              three requests waiting - one new encrypted
//                              folder, one folder encrypted where it is, one
//                              new encrypted file - with who asked and why
//   approve-new-folder.png     the administrator's answer to the new-folder
//                              request: what the approval opens, once
//   request-new-folder.png     the person's side: New folder → Request an
//                              encrypted folder…, a reason written
//
// It also MEASURES the page at 1024 and 1440 px in English and Turkish (no
// sideways scroll, the requests table inside its frame).
//
// ⚠ Only a person who is NOT an administrator can ask (an administrator is
// never asked, and the server answers their request 400), so the requests
// are filed by three accounts with the User role, through the API the
// explorer's dialog calls.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'encryption';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PEOPLE = [
  { email: 'deniz@example.com', display_name: 'Deniz Kaya' },
  { email: 'ece@example.com', display_name: 'Ece Aydın' },
  { email: 'sam@example.com', display_name: 'Sam Carter' },
];
const pw = (email) => `${email.split('@')[0]}-shots-2026`;

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // A team storage the three can write to, with something to protect.
    const root = inst.storageRoot('team');
    if (!inst.container) mkdirSync(root, { recursive: true });
    await admin.post('/api/admin/storages', {
      name: 'Team',
      driver: 'local',
      mount_path: root,
      config: { path: root },
      sync_mode: 'ondemand',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
    });
    for (const name of ['Contracts', 'Payroll', 'Board']) {
      await admin.post('/api/files/manager?action=newfolder', { path: 'Team://', name });
    }
    const upload = async (dir, name, body) => {
      const fd = new FormData();
      fd.append('path', dir);
      fd.append('file[]', new Blob([body]), name);
      const res = await admin.call('/api/files/manager?action=upload', { method: 'POST', body: fd });
      if (!res.ok) throw new Error(`upload ${name}: ${res.status} ${(await res.text()).slice(0, 300)}`);
    };
    await upload('Team://Payroll', 'Salaries 2026.csv', 'name;salary\nDeniz Kaya;4200\n');
    await upload('Team://Board', 'Minutes 2026-09.md', '# Board minutes, September\n');

    for (const p of PEOPLE) {
      await admin.post('/api/admin/users', { email: p.email, password: pw(p.email), display_name: p.display_name, role: 'user', locale: 'en' });
    }
    await admin.json('/api/admin/e2e', { method: 'PATCH', body: JSON.stringify({ policy: 'approval' }) });

    // The three requests, as the explorer's dialog sends them.
    const ask = async (email, path, kind, reason) => {
      const person = client(inst.url);
      await person.login(email, pw(email));
      await person.post('/api/files/e2e/requests', { path, kind, reason });
    };
    await ask('deniz@example.com', 'Team://Contracts', 'new_folder', 'Signed client contracts');
    await ask('ece@example.com', 'Team://Payroll', 'folder', 'Salaries, not for the storage host');
    await sleep(1100);
    await ask('deniz@example.com', 'Team://Board/Minutes 2026-09.md', 'file', 'Minutes before they go out');
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    const open = async (width, height = 900) => {
      await page.setViewportSize({ width, height });
      await page.goto(`${inst.url}/admin/encryption`);
      await page.getByTestId('encryption-requests').waitFor({ timeout: 20_000 });
      await page.getByTestId('encryption-requests-count').waitFor({ timeout: 20_000 });
      await page.mouse.move(4, 4);
      await sleep(500);
    };

    // ── measure ───────────────────────────────────────────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [1024, 1440]) {
        await open(width);
        for (const x of await layoutProblems(page, { frames: ['encryption-requests'] })) problems.push(`${locale} ${width}px: ${x}`);
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('the Encryption page fits at 1024 and 1440 px, in English and Turkish');

    // ── 1. Admin → Encryption ────────────────────────────────────────────
    await setLanguage(admin, page, 'en');
    await open(1440);
    await mustSay(page.locator('main'), 'the Encryption page', [
      'Who may encrypt', 'after an administrator’s approval', 'Encryption requests', '3 waiting',
      'One new encrypted folder in this folder', 'This folder, encrypted where it is', 'One new encrypted file in this folder',
      'Deniz Kaya', 'Ece Aydın', 'Contracts', 'Signed client contracts',
    ]);
    await shot(page, SET, 'admin-encryption-1440.png');

    // ── 2. Approving the new-folder request ──────────────────────────────
    const list = await admin.json('/api/admin/e2e/requests?status=pending');
    const newFolder = (list.requests ?? []).find((r) => r.kind === 'new_folder');
    if (!newFolder) throw new Error(`no new-folder request in ${JSON.stringify(list).slice(0, 300)}`);
    await page.getByTestId(`e2e-request-actions-${newFolder.id}`).click();
    await page.getByRole('menuitem', { name: 'Approve' }).click();
    const answer = page.getByTestId('e2e-answer');
    await answer.waitFor({ state: 'visible', timeout: 10_000 });
    await sleep(400);
    const card = page.locator('dialog[open] [role="dialog"]').last();
    await mustSay(card, 'the approval dialog', ['Approve the request', 'one new encrypted folder directly inside', 'Contracts', 'used once']);
    await shot(card, SET, 'approve-new-folder.png');
    await page.keyboard.press('Escape');
    await ctx.close();

    // ── 3. The person's side: New folder → Request an encrypted folder… ──
    const pctx = await newContext(browser, { width: 1440, height: 900 });
    const ppage = await pctx.newPage();
    await signIn(ppage, inst.url, { email: 'sam@example.com', password: pw('sam@example.com') });
    await ppage.goto(`${inst.url}/drive/explore#${encodeURIComponent('Team')}/Contracts`);
    // ⚠ Not 'networkidle': the explorer keeps a connection open, and the wait
    // never ends here. The New button, then the folder's own (empty) listing.
    await ppage.getByTestId('sidenav-new').first().waitFor({ state: 'visible', timeout: 20_000 });
    await sleep(1500);
    await ppage.getByTestId('sidenav-new').first().click();
    await ppage
      .locator('.fe-ctx__item', { has: ppage.locator('.fe-ctx__label', { hasText: /^New folder$/ }) })
      .first()
      .click();
    const dialog = ppage.locator('.fe-modal__card').filter({ hasText: 'New folder' }).last();
    await dialog.waitFor({ state: 'visible', timeout: 10_000 });
    // The encryption answer arrives a moment after the dialog opens
    // (POST /api/files/e2e/allowed): the request option appears with it.
    await ppage.getByTestId('e2e-request-option').waitFor({ state: 'visible', timeout: 15_000 });
    await ppage.getByTestId('e2e-request-option').click();
    const request = ppage.locator('.fe-modal__card').filter({ has: ppage.getByTestId('e2e-request') });
    await request.waitFor({ state: 'visible', timeout: 10_000 });
    await ppage.getByTestId('e2e-request-reason').fill('Client NDAs for the Berlin office - two people need them');
    await ppage.mouse.move(4, 4);
    await sleep(400);
    await mustSay(request, 'the request dialog', ['one new encrypted folder', 'Reason', 'Send request']);
    await shot(request, SET, 'request-new-folder.png');
    await pctx.close();
    log('Admin → Encryption with three requests, the approval, the request dialog');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
