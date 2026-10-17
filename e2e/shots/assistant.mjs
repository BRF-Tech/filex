// The AI assistant (task #269, docs/AI-ASSISTANT.md), against a fake model
// behind OpenAI's Chat Completions API (e2e/lib/fake-llm.mjs):
//
//   node e2e/shots/assistant.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/assistant/ (or SHOTS_OUT):
//
//   panel-1440.png       the explorer with the assistant's panel open: a
//                        question, the step the tool took, the answer
//   approval-1440.png    a public link waiting on its approval card
//   settings-1440.png    Admin, Settings, AI assistant: the provider, the
//                        address, the model, the test's answer, the limits
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { startFakeLlm } from '../lib/fake-llm.mjs';
import { addLocalStorage, bootInstance, client, log, mustSay, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'assistant';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const STORAGE = 'projects';
const FILE = 'budget-2026.xlsx';

async function main() {
  const llm = await startFakeLlm({ storage: STORAGE, file: FILE });
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    const root = join(tmpdir(), `filex-shots-${SET}-${Date.now()}`);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, FILE), 'Quarter,Amount\nQ1,1200\nQ2,1350\n');
    writeFileSync(join(root, 'roadmap.md'), '# Roadmap\n\n- Q3: the new office\n');
    writeFileSync(join(root, 'kickoff-notes.txt'), 'Kickoff: budget agreed, roadmap next.\n');
    await addLocalStorage(admin, STORAGE, root);
    await admin.patch('/api/admin/assistant', { enabled: true, provider: 'openai', base_url: llm.url, model: 'fake-model' });

    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await signIn(page, inst.url, ADMIN);

    // ── the panel, a question answered ───────────────────────────────────
    await page.goto(`${inst.url}/drive/explore`);
    await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
    await page.locator('[data-fe-path]').filter({ hasText: FILE }).first().waitFor({ timeout: 20_000 });
    await page.getByTestId('header-ai').click();
    const panel = page.getByTestId('assistant-panel');
    await panel.waitFor({ timeout: 10_000 });
    await page.getByTestId('assistant-input').fill('What is in this storage?');
    await page.getByTestId('assistant-send').click();
    await panel.locator('.fe-assistant__md').first().waitFor({ timeout: 20_000 });
    await mustSay(panel, 'the panel', ['What is in this storage?', 'List a folder', FILE]);
    await sleep(400);
    await shot(page, SET, 'panel-1440.png');

    // ── a public link, waiting for the person ────────────────────────────
    await page.getByTestId('assistant-input').fill(`Share ${FILE} with a link`);
    await page.getByTestId('assistant-send').click();
    const card = page.getByTestId('assistant-approval');
    await card.waitFor({ timeout: 20_000 });
    await mustSay(card, 'the approval card', ['public link', FILE, 'Approve', 'Decline']);
    await sleep(300);
    await shot(panel, SET, 'approval-1440.png');
    await page.getByTestId('assistant-decline').click();
    await sleep(500);

    // ── the administrator's page ─────────────────────────────────────────
    await page.setViewportSize({ width: 1440, height: 1300 });
    await page.goto(`${inst.url}/admin/ai-assistant`);
    const settings = page.getByTestId('ai-assistant-admin');
    await settings.waitFor({ timeout: 20_000 });
    await page.getByTestId('ai-assistant-test').click();
    await page.getByTestId('ai-assistant-test-result').waitFor({ timeout: 20_000 });
    await mustSay(settings, 'the settings page', ['AI assistant', 'Provider', 'Model', 'Limits and keeping']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(settings, SET, 'settings-1440.png');
    await ctx.close();

    log('the panel, the approval card and the settings page');
  } finally {
    await browser.close();
    await inst.stop();
    await llm.close();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
