// The two admin screens that printed a Go error as their whole answer.
//
// ⚠ QA, 2026-09-21: Updates read "Check failed: Get \"https://…\": dial tcp
// …: connection refused"; the SMTP test read "SMTP failed: dial tcp
// 10.0.0.5:587: connect: connection refused". Both screens are an
// administrator's, so the raw words may stay — as a SECOND line, under a
// sentence that says what happened and what to do.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { useToastStore } from '@/stores/toast';

const RAW = 'Get "https://api.github.com/repos/x/y/releases": dial tcp 140.82.121.6:443: connect: connection refused';

vi.mock('@/api/updates', () => ({
  UpdatesApi: {
    status: vi.fn(async () => ({
      current: 'v0.43.0', enabled: true, policy: 'manual', mode: 'binary', can_self_apply: true,
      restart_required: false, action: 'none', step: 'idle', check_error: RAW,
    })),
    check: vi.fn(async () => ({
      current: 'v0.43.0', enabled: true, policy: 'manual', mode: 'binary', can_self_apply: true,
      restart_required: false, action: 'none', step: 'idle', check_error: RAW,
    })),
    releases: vi.fn(async () => []),
    apply: vi.fn(),
  },
}));

import Updates from '@/views/Updates.vue';

describe('Updates: a failed check', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('says what happened; the raw error is the second line, and never the toast', async () => {
    const i18n = createI18n({ legacy: false, locale: 'tr', fallbackLocale: 'en', messages: { en, tr } });
    const w = mount(Updates, { global: { plugins: [i18n] } });
    await flushPromises();
    const box = w.get('[data-testid="updates-check-error"]');
    expect(box.text()).toContain(tr.updates.checkFailedWords);
    expect(w.get('[data-testid="updates-check-detail"]').text()).toContain('connection refused');

    const check = w.findAll('button').find((b) => b.text().includes(tr.updates.checkNow));
    expect(check, 'the Check now button').toBeDefined();
    await check!.trigger('click');
    await flushPromises();
    const toasts = useToastStore().toasts.map((x) => x.message);
    expect(toasts).toContain(tr.updates.checkFailedWords);
    expect(toasts.join(' ')).not.toContain('dial tcp');
  });
});

describe('SMTP test: the reason in words', () => {
  // Settings.vue mounts half the admin; its SMTP wiring is read from the
  // source and pinned by shape (a name alone survives in a comment).
  const src = readFileSync(path.resolve(__dirname, '../../src/views/Settings.vue'), 'utf8');

  it('the sentence comes from the server’s reason, the error is the second line', () => {
    expect(src).toMatch(/smtpTestMsg\.value = t\(`settings\.smtp\.reason\.\$\{smtpReasonKey\(data\.reason\)\}`\);/);
    expect(src).toMatch(/smtpTestDetail\.value = data\.error \?\? '';/);
    expect(src).not.toMatch(/smtpTestMsg\.value = `[^`]*\$\{data\.error/);
  });

  it('every reason the server names has words in both languages', () => {
    for (const r of ['not_configured', 'host', 'connect', 'tls', 'starttls', 'auth', 'recipient', 'rejected', 'other']) {
      expect((en.settings.smtp.reason as Record<string, string>)[r], r).toBeTruthy();
      expect((tr.settings.smtp.reason as Record<string, string>)[r], r).toBeTruthy();
    }
  });
});
