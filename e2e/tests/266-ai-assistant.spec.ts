/**
 * 266 - the AI assistant in the explorer (task #269), end to end with a fake
 * model behind OpenAI's Chat Completions API (e2e/lib/fake-llm.mjs).
 *
 * Off by default: no button anywhere. Switched on on Admin, Settings, AI
 * assistant, the explorer offers its button; the panel streams the reply and
 * shows the step the tool took; a public link waits on a card until the person
 * approves it; User settings says the state and who reads what is written;
 * "Hide the button" takes the button away again.
 *
 * RED PROOF: before 0.57 the toolbar drew a disabled "AI assistant - coming
 * soon" button for everybody, and none of the routes this suite calls existed.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { startFakeLlm, type FakeLlm } from '../lib/fake-llm.mjs';

const STORAGE = `e2eai${Date.now()}`;
const FILE = 'budget-2026.txt';

async function openExplorer(page: Page) {
  await page.goto('/drive/explore');
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  await expect(page.locator('[data-fe-path]').filter({ hasText: FILE }).first()).toBeVisible();
}

test.describe.serial('the AI assistant', () => {
  let api: APIRequestContext;
  let llm: FakeLlm;

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    llm = await startFakeLlm({ storage: STORAGE, file: FILE });
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from('Q1 1200\nQ2 1350\n') } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBeTruthy();
    // Off, as every installation starts.
    const off = await api.patch('/api/admin/assistant', { data: { enabled: false, hide_button: false } });
    expect(off.ok(), await off.text()).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    await api.patch('/api/admin/assistant', { data: { enabled: false, hide_button: false, provider: '', base_url: '', model: '' } });
    await dropStorageByName(request, STORAGE);
    await api.dispose();
    await llm.close();
  });

  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
  });

  test('off: there is no button at all', async ({ page }) => {
    await openExplorer(page);
    await expect(page.getByTestId('header-ai')).toHaveCount(0);
    const caps = await (await api.get('/api/capabilities')).json();
    expect(caps.assistant).toEqual({ state: 'off', permitted: true, button: false });
  });

  test('the administrator sets it up on its page, and the test answers', async ({ page }) => {
    const res = await api.patch('/api/admin/assistant', {
      data: { enabled: true, provider: 'openai', base_url: llm.url, model: 'fake-model' },
    });
    expect(res.ok(), await res.text()).toBeTruthy();
    expect((await res.json()).state).toBe('on');

    await page.goto('/admin/ai-assistant');
    await expect(page.getByTestId('ai-assistant-state')).toHaveText(/On|Açık/);
    await page.getByTestId('ai-assistant-test').click();
    await expect(page.getByTestId('ai-assistant-test-result')).toContainText(/can use tools|araç kullanabiliyor/);
    await expect(page.getByTestId('ai-assistant-models')).toContainText('fake-model-large');
  });

  test('the panel streams the reply and shows the step the tool took', async ({ page }) => {
    await openExplorer(page);
    await page.getByTestId('header-ai').click();
    const panel = page.getByTestId('assistant-panel');
    await expect(panel).toBeVisible();
    await expect(page.getByTestId('assistant-provider')).toHaveText('OpenAI · fake-model');
    await page.getByTestId('assistant-input').fill('What is in this storage?');
    await page.getByTestId('assistant-send').click();
    await expect(panel).toContainText(FILE, { timeout: 20_000 });
    await expect(panel.locator('.fe-assistant__step.is-done')).toHaveCount(1);
    // The tool ran as the person, and its answer went back to the model.
    const toolAnswer = llm.requests.at(-1)?.messages.find((m) => m.role === 'tool');
    expect(String(toolAnswer?.content)).toContain(FILE);
  });

  test('a public link waits for the person; approved, it is made', async ({ page }) => {
    await openExplorer(page);
    await page.getByTestId('header-ai').click();
    await page.getByTestId('assistant-input').fill(`Share ${FILE} with a link`);
    await page.getByTestId('assistant-send').click();
    const card = page.getByTestId('assistant-approval');
    await expect(card).toBeVisible({ timeout: 20_000 });
    await expect(card).toContainText(FILE);
    const before = llm.requests.length;
    // Nothing is shared while the card waits.
    const shares = await (await api.get('/api/shares')).text();
    expect(shares).not.toContain(FILE);
    await page.getByTestId('assistant-approve').click();
    await expect(card).toHaveCount(0, { timeout: 20_000 });
    await expect(page.getByTestId('assistant-panel')).toContainText('/s/', { timeout: 20_000 });
    expect(llm.requests.length).toBeGreaterThan(before);
  });

  test('User settings says the state and who reads what is written', async ({ page }) => {
    await page.goto('/admin/dashboard?settings=1');
    await page.getByTestId('user-settings-tab-ai').click();
    await expect(page.getByTestId('user-settings-ai-state')).toHaveText(/is on|açık/);
    await expect(page.getByTestId('user-settings-ai-provider')).toHaveText('OpenAI · fake-model');
  });

  test('"Hide the button" takes it away for everybody', async ({ page }) => {
    const res = await api.patch('/api/admin/assistant', { data: { hide_button: true } });
    expect(res.ok()).toBeTruthy();
    await openExplorer(page);
    await expect(page.getByTestId('header-ai')).toHaveCount(0);
    const chat = await api.post('/api/files/assistant/chat', { data: { message: 'hi' } });
    expect(chat.status()).toBe(404);
  });
});
