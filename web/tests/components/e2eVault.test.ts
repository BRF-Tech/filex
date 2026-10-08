// The vault's surfaces (wiring:e2 vault; docs/E2E-VAULT-FORMAT.md → "Clients",
// "The idle lock"): level 3 in the picker only where the server has vaults
// and only for a new folder, the pack size beside it, the strip that says who
// writes and when the vault locks itself, and the settings that change
// nothing about a vault's level.

import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import E2eLevelPicker from '@brftech/filex-core/src/components/E2eLevelPicker.vue';
import EncryptedFolderModal from '@brftech/filex-core/src/components/EncryptedFolderModal.vue';
import E2eVaultStrip from '@brftech/filex-core/src/components/E2eVaultStrip.vue';
import E2eSettingsModal from '@brftech/filex-core/src/components/E2eSettingsModal.vue';
import type { VaultStripState } from '@brftech/filex-core/src/composables/useE2eVault';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');

function strip(over: Partial<VaultStripState> = {}): VaultStripState {
  return {
    root: 'docs://Kasa',
    name: 'Kasa',
    generation: 3,
    mode: 'read',
    busy: false,
    progress: null,
    holder: null,
    readOnly: '',
    shownAt: 0,
    limit: 'ok',
    entries: 4,
    lost: '',
    lostMinutes: 0,
    lostTo: '',
    unsaved: [],
    idleDeadline: 0,
    idleMinutes: 0,
    lockDeadline: 0,
    error: '',
    ...over,
  };
}

describe('the level picker', () => {
  it('draws two levels without `vault`: a choice that does not work is not offered', () => {
    const w = mount(E2eLevelPicker, { props: { locale: 'en', modelValue: 'content' } });
    expect(w.findAll('input[type="radio"]').map((i) => i.attributes('data-testid'))).toEqual(['e2e-level-content', 'e2e-level-names']);
    expect(w.find('[data-testid="e2e-level-vault-cost"]').exists()).toBe(false);
  });

  it('draws the vault as level 3, with what it costs, when the server has vaults', () => {
    const w = mount(E2eLevelPicker, { props: { locale: 'tr', modelValue: 'vault', vault: true } });
    const radios = w.findAll('input[type="radio"]');
    expect(radios.map((i) => i.attributes('data-testid'))).toEqual(['e2e-level-content', 'e2e-level-names', 'e2e-level-vault']);
    expect(w.text()).toContain(`3 · ${tr('e2e.level.vault')}`);
    expect(w.find('[data-testid="e2e-level-vault-cost"]').text()).toBe(tr('e2e.level.vault_cost'));
    expect((radios[2].element as HTMLInputElement).checked).toBe(true);
  });
});

describe('the encrypted folder dialog', () => {
  const fill = async (password = 'correct horse battery') => {
    const inputs = document.body.querySelectorAll<HTMLInputElement>('input');
    const text = [...inputs].filter((i) => i.type === 'text')[0];
    const pws = [...inputs].filter((i) => i.type === 'password');
    if (text) {
      text.value = 'Kasa';
      text.dispatchEvent(new Event('input'));
    }
    for (const p of pws) {
      p.value = password;
      p.dispatchEvent(new Event('input'));
    }
    const ack = document.body.querySelector<HTMLInputElement>('[data-testid="e2e-create-ack"]')!;
    ack.checked = true;
    ack.dispatchEvent(new Event('change'));
    await nextTick();
  };

  it('offers the pack size only for a vault, and sends it', async () => {
    const w = mount(EncryptedFolderModal, { props: { open: true, locale: 'en', vaultAvailable: true }, attachTo: document.body });
    await nextTick();
    expect(document.body.querySelector('[data-testid="e2e-vault-pack"]')).toBeNull();
    const vault = document.body.querySelector<HTMLInputElement>('[data-testid="e2e-level-vault"]')!;
    vault.dispatchEvent(new Event('change'));
    await nextTick();
    const pack = document.body.querySelector('[data-testid="e2e-vault-pack"]')!;
    expect(pack).not.toBeNull();
    expect(pack.textContent).toContain(en('e2e.vault.pack_4'));
    expect(pack.textContent).toContain(en('e2e.vault.pack_16'));
    (document.body.querySelector('[data-testid="e2e-vault-pack-24"]') as HTMLButtonElement).click();
    await fill();
    const create = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === en('e2e.create.create'))!;
    create.click();
    await nextTick();
    const sent = w.emitted('submit')?.[0]?.[0] as { level: string; packLog2?: number; name: string };
    expect(sent.level).toBe('vault');
    expect(sent.packLog2).toBe(24);
    expect(sent.name).toBe('Kasa');
  });

  it('4 MiB unless 16 is chosen', async () => {
    const w = mount(EncryptedFolderModal, { props: { open: true, locale: 'en', vaultAvailable: true }, attachTo: document.body });
    await nextTick();
    document.body.querySelector<HTMLInputElement>('[data-testid="e2e-level-vault"]')!.dispatchEvent(new Event('change'));
    await nextTick();
    await fill();
    [...document.body.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === en('e2e.create.create'))!.click();
    await nextTick();
    expect((w.emitted('submit')?.[0]?.[0] as { packLog2?: number }).packLog2).toBe(22);
  });

  it('never offers the vault for a folder that already exists, nor without the server', async () => {
    mount(EncryptedFolderModal, { props: { open: true, locale: 'en', vaultAvailable: true, existing: 'Belgeler' }, attachTo: document.body });
    await nextTick();
    expect(document.body.querySelector('[data-testid="e2e-level-vault"]')).toBeNull();
  });

  it('no vault level on a server without vaults', async () => {
    mount(EncryptedFolderModal, { props: { open: true, locale: 'en' }, attachTo: document.body });
    await nextTick();
    expect(document.body.querySelector('[data-testid="e2e-level-vault"]')).toBeNull();
  });
});

describe('the vault strip', () => {
  const sentence = (w: ReturnType<typeof mount>) => w.find('[data-testid="e2e-vault-sentence"]').text();

  it('read-only until something writes, and the vault-lock countdown', () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'tr', state: strip({ lockDeadline: Date.now() + 14 * 60_000 + 30_000 }) } });
    expect(sentence(w)).toBe(tr('e2e.vault.read_only'));
    const lock = w.find('[data-testid="e2e-vault-lock-countdown"]').text();
    expect(lock).toMatch(/^14:(29|30) boyunca/);
    expect(w.attributes('data-mode')).toBe('read');
  });

  it('writing: how long until it goes back to read-only, and no vault-lock clock', () => {
    const w = mount(E2eVaultStrip, {
      props: { locale: 'en', state: strip({ mode: 'write', idleDeadline: Date.now() + 2 * 60_000 + 41_000, lockDeadline: 0 }) },
    });
    expect(sentence(w)).toMatch(/^You are writing to this vault\. If you do nothing for 2:(40|41), it goes back to read-only\.$/);
    expect(w.find('[data-testid="e2e-vault-lock-countdown"]').exists()).toBe(false);
  });

  it('somebody else writes: who and since when, and "Take over"', async () => {
    const w = mount(E2eVaultStrip, {
      props: { locale: 'tr', state: strip({ holder: { name: 'Ayşe', client: 'desktop', label: 'filex, macOS', since: '2026-10-06T10:00:00Z' } }) },
    });
    expect(sentence(w)).toContain('Ayşe (filex, macOS)');
    expect(sentence(w)).toContain('salt okunur');
    await w.find('[data-testid="e2e-vault-take-over"]').trigger('click');
    expect(w.emitted('take-over')).toHaveLength(1);
  });

  it('the lock was lost to idleness: says so, with the minutes', () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'en', state: strip({ lost: 'idle', lostMinutes: 3 }) } });
    expect(sentence(w)).toBe('You did nothing for 3 minutes, so this vault went back to read-only. Nothing was lost.');
    const one = mount(E2eVaultStrip, { props: { locale: 'en', state: strip({ lost: 'idle', lostMinutes: 1 }) } });
    expect(sentence(one)).toBe('You did nothing for 1 minute, so this vault went back to read-only. Nothing was lost.');
  });

  it('somebody took over: the server named who, and the strip says it', () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'tr', state: strip({ lost: 'taken', lostTo: 'Ayşe' }) } });
    expect(sentence(w)).toBe(tr('e2e.vault.lost_taken_by', { name: 'Ayşe' }));
    const broken = mount(E2eVaultStrip, { props: { locale: 'en', state: strip({ lost: 'broken', lostTo: 'Admin' }) } });
    expect(sentence(broken)).toBe(en('e2e.vault.lost_broken_by', { name: 'Admin' }));
  });

  it('a lock lost mid-change lists what was not saved, and it can be dismissed', async () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'tr', state: strip({ lost: 'connection', unsaved: ['rapor.pdf', 'not.txt'] }) } });
    expect(sentence(w)).toBe(tr('e2e.vault.lost_connection'));
    expect(w.find('[data-testid="e2e-vault-unsaved"]').text()).toBe(tr('e2e.vault.unsaved', { names: 'rapor.pdf, not.txt' }));
    expect(w.classes()).toContain('fe-vault-strip--error');
    await w.find('[data-testid="e2e-vault-dismiss"]').trigger('click');
    expect(w.emitted('dismiss')).toHaveLength(1);
  });

  it('a damaged newest state: read-only, and "Continue from this state"', async () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'en', state: strip({ readOnly: 'damaged', shownAt: 0 }) } });
    expect(sentence(w)).toBe(en('e2e.vault.ro_damaged_short'));
    expect(w.attributes('data-readonly')).toBe('damaged');
    await w.find('[data-testid="e2e-vault-continue"]').trigger('click');
    expect(w.emitted('continue')).toHaveLength(1);
    // No "Take over" on a vault that is read-only for a reason of its own.
    expect(w.find('[data-testid="e2e-vault-take-over"]').exists()).toBe(false);
  });

  it('Lock and settings are the parent\'s, and wait while a change runs', async () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'en', state: strip({ busy: true, progress: { done: 1024, total: 4096 } }) } });
    expect(sentence(w)).toContain('Saving:');
    expect(w.find('[data-testid="e2e-vault-lock"]').attributes('disabled')).toBeDefined();
    const idle = mount(E2eVaultStrip, { props: { locale: 'en', state: strip() } });
    await idle.find('[data-testid="e2e-vault-lock"]').trigger('click');
    await idle.find('[data-testid="e2e-settings-open"]').trigger('click');
    expect(idle.emitted('lock')).toHaveLength(1);
    expect(idle.emitted('settings')).toHaveLength(1);
  });

  it('warns near the limit', () => {
    const w = mount(E2eVaultStrip, { props: { locale: 'en', state: strip({ limit: 'warn', entries: 212400 }) } });
    expect(w.find('[data-testid="e2e-vault-limit"]').text()).toContain('212,400');
  });
});

describe('a vault\'s encryption settings', () => {
  it('say the level and change nothing about it', async () => {
    mount(E2eSettingsModal, {
      props: { open: true, locale: 'tr', level: 'vault', canRaise: false, plainNamed: 0, escrowState: 'n/a' },
      attachTo: document.body,
    });
    await nextTick();
    expect(document.body.querySelector('[data-testid="e2e-settings-level"]')?.textContent).toContain(tr('e2e.level.vault'));
    expect(document.body.querySelector('[data-testid="e2e-settings-vault-fixed"]')?.textContent?.trim()).toBe(tr('e2e.vault.level_fixed'));
    expect(document.body.querySelector('[data-testid="e2e-settings-raise"]')).toBeNull();
    expect(document.body.querySelector('[data-testid="e2e-settings-fix-names"]')).toBeNull();
    // The password still changes.
    expect(document.body.querySelector('[data-testid="e2e-password-open"]')).not.toBeNull();
  });
});
