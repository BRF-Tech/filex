import { nextTick, ref } from 'vue';
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import ArchiveCreateModal from '@brftech/filex-core/src/modals/ArchiveCreateModal.vue';
import ArchivePasswordModal from '@brftech/filex-core/src/modals/ArchivePasswordModal.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';
import { archiveFormatLabel } from '@brftech/filex-core/src/lib/archiveFormats';

// One way to write a format, in the dialog and on the admin page alike: the
// admin page wrote "7Z" where the dialog wrote "7z".
describe('archiveFormatLabel', () => {
  it('writes 7z as its project does and every other format in capitals', () => {
    expect(['zip', '7z', 'tar.gz', 'RAR', ' Tar.Xz '].map(archiveFormatLabel)).toEqual(['ZIP', '7z', 'TAR.GZ', 'RAR', 'TAR.XZ']);
  });
});

describe('ArchiveCreateModal', () => {
  it('keeps the filename extension in sync and submits only supported 7z options', async () => {
    const wrapper = mount(ArchiveCreateModal, {
      props: { open: false, locale: 'en', count: 2, suggestedName: 'backup' },
    });
    await wrapper.setProps({ open: true });
    await nextTick();

    const name = wrapper.find<HTMLInputElement>('input[autocomplete="off"]');
    const format = wrapper.find<HTMLSelectElement>('select');
    expect(name.element.value).toBe('backup.7z');
    expect(format.findAll('option').map((option) => option.text())).toEqual([
      'ZIP', '7z', 'TAR', 'TAR.GZ', 'TAR.BZ2', 'TAR.XZ',
    ]);

    await format.setValue('zip');
    expect(name.element.value).toBe('backup.zip');
    await format.setValue('7z');
    expect(name.element.value).toBe('backup.7z');

    const passwords = wrapper.findAll<HTMLInputElement>('input[type="password"]');
    for (const input of passwords) {
      expect(input.attributes('autocomplete')).toBe('off');
      expect(input.attributes('data-1p-ignore')).toBe('');
      expect(input.attributes('data-bwignore')).toBe('true');
      expect(input.attributes('data-lpignore')).toBe('true');
    }
    await passwords[0].setValue('secret');
    await passwords[1].setValue('different');
    await wrapper.find('.fe-btn--primary').trigger('click');
    expect(wrapper.emitted('submit')).toBeUndefined();
    expect(wrapper.find('.fe-form__error').text()).toMatch(/match/i);

    await passwords[1].setValue('secret');
    const checks = wrapper.findAll<HTMLInputElement>('input[type="checkbox"]');
    await checks[0].setValue(true);
    const selects = wrapper.findAll<HTMLSelectElement>('select');
    await selects[1].setValue('128');
    await wrapper.find('.fe-btn--primary').trigger('click');

    expect(wrapper.emitted('submit')?.at(-1)?.[0]).toEqual({
      name: 'backup.7z',
      format: '7z',
      password: 'secret',
      encrypt_filenames: true,
      compression: 5,
      solid: true,
      dictionary_size_mb: 128,
    });
  });

  it('creates compressed TAR names and hides encryption-only controls', async () => {
    const wrapper = mount(ArchiveCreateModal, {
      props: { open: false, locale: 'en', count: 1, suggestedName: 'backup' },
    });
    await wrapper.setProps({ open: true });
    await nextTick();

    const format = wrapper.find<HTMLSelectElement>('select');
    await format.setValue('tar.gz');
    expect(wrapper.find<HTMLInputElement>('input[autocomplete="off"]').element.value).toBe('backup.tar.gz');
    expect(wrapper.findAll('input[type="password"]')).toHaveLength(0);
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(0);

    await wrapper.find('.fe-btn--primary').trigger('click');
    expect(wrapper.emitted('submit')?.at(-1)?.[0]).toEqual({
      name: 'backup.tar.gz',
      format: 'tar.gz',
      password: undefined,
      encrypt_filenames: false,
      compression: 5,
      solid: undefined,
      dictionary_size_mb: undefined,
    });
  });

  // A server without 7-Zip can make a plain ZIP only (capabilities say
  // `encryption: false`): no password fields, rather than a dialog that fails
  // with PROVIDER_UNAVAILABLE once filled in.
  it('offers no password where the server cannot encrypt', async () => {
    const wrapper = mount(ArchiveCreateModal, {
      props: { open: false, locale: 'en', count: 1, suggestedName: 'backup', allowedFormats: ['zip'], encryption: false },
    });
    await wrapper.setProps({ open: true });
    await nextTick();
    expect(wrapper.findAll('input[type="password"]')).toHaveLength(0);
    await wrapper.find('.fe-btn--primary').trigger('click');
    expect(wrapper.emitted('submit')?.at(-1)?.[0]).toMatchObject({ format: 'zip', password: undefined });
  });

  // 7-Zip encrypts a ZIP with ASCII only; the dialog says so before sending.
  it('refuses a non-ASCII ZIP password in the dialog and in Turkish', async () => {
    const wrapper = mount(ArchiveCreateModal, {
      props: { open: false, locale: 'tr', count: 1, suggestedName: 'rapor', defaultFormat: 'zip' },
    });
    await wrapper.setProps({ open: true });
    await nextTick();
    const passwords = wrapper.findAll<HTMLInputElement>('input[type="password"]');
    expect(passwords).toHaveLength(2);
    await passwords[0].setValue('şifre');
    await passwords[1].setValue('şifre');
    await wrapper.find('.fe-btn--primary').trigger('click');
    expect(wrapper.emitted('submit')).toBeUndefined();
    expect(wrapper.find('.fe-form__error').text()).toContain('7z');
    expect(wrapper.find('.fe-form__error').text()).toContain('ç, ğ, ı, İ, ö, ş, ü');

    await wrapper.find<HTMLSelectElement>('select').setValue('7z');
    await nextTick();
    const again = wrapper.findAll<HTMLInputElement>('input[type="password"]');
    await again[0].setValue('şifre');
    await again[1].setValue('şifre');
    await wrapper.find('.fe-btn--primary').trigger('click');
    expect(wrapper.emitted('submit')?.at(-1)?.[0]).toMatchObject({ format: '7z', password: 'şifre' });
  });

  it('limits choices to the operator-configured formats', async () => {
    const wrapper = mount(ArchiveCreateModal, {
      props: {
        open: false,
        locale: 'en',
        count: 1,
        suggestedName: 'backup.zip',
        defaultFormat: 'tar.xz',
        allowedFormats: ['tar', 'tar.xz'],
      },
    });
    await wrapper.setProps({ open: true });
    await nextTick();

    const format = wrapper.find<HTMLSelectElement>('select');
    expect(format.findAll('option').map((option) => option.text())).toEqual(['TAR', 'TAR.XZ']);
    expect(format.element.value).toBe('tar.xz');
    expect(wrapper.find<HTMLInputElement>('input[autocomplete="off"]').element.value).toBe('backup.tar.xz');
  });

  it('honours an operator-configured ZIP default', async () => {
    const wrapper = mount(ArchiveCreateModal, {
      props: {
        open: false,
        locale: 'en',
        count: 1,
        suggestedName: 'backup',
        defaultFormat: 'zip',
        allowedFormats: ['zip', '7z'],
      },
    });
    await wrapper.setProps({ open: true });
    await nextTick();

    expect(wrapper.find<HTMLInputElement>('input[autocomplete="off"]').element.value).toBe('backup.zip');
    expect(wrapper.find<HTMLSelectElement>('select').element.value).toBe('zip');
  });
});

describe('ArchivePasswordModal', () => {
  it('focuses the password input when opened', async () => {
    vi.useFakeTimers();
    const wrapper = mount(ArchivePasswordModal, {
      attachTo: document.body,
      props: { open: false, locale: 'en', archiveName: 'protected.7z' },
    });

    await wrapper.setProps({ open: true });
    await nextTick();
    vi.advanceTimersByTime(31);

    const password = wrapper.find<HTMLInputElement>('input[type="password"]');
    expect(document.activeElement).toBe(password.element);
    expect(password.attributes('autocomplete')).toBe('off');
    expect(password.attributes('data-1p-ignore')).toBe('');
    expect(password.attributes('data-bwignore')).toBe('true');
    expect(password.attributes('data-lpignore')).toBe('true');
    wrapper.unmount();
    vi.useRealTimers();
  });
});

describe('OperationsCenter archive progress', () => {
  it('renders archive counters as a percentage instead of implementation units', async () => {
    const operation = {
      key: 'ops:42', kind: 'archive-create', name: 'backup.7z', percent: 72,
      status: 'running', error: null, queued: false, cancelling: false,
      doneCount: 72, totalCount: 100, uploadedBytes: null, totalBytes: null,
      cancellable: true, retryable: false, startedAt: Date.now(), settledAt: null,
    };
    const center = {
      active: ref([operation]), history: ref([]), hasError: ref(false),
      overallPercent: ref(72), runningCount: ref(1),
      cancel: vi.fn(), retry: vi.fn(), dismiss: vi.fn(), clearHistory: vi.fn(),
    };
    const wrapper = mount(OperationsCenter, {
      props: { center: center as never, locale: 'en' },
    });

    await wrapper.find('.fe-opc__badge').trigger('click');
    const state = wrapper.find('.fe-opc__state').text();
    expect(state).toBe('72%');
    expect(state).not.toContain('/100');
  });
});
