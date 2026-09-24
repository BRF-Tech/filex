import { nextTick, ref } from 'vue';
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import ArchiveCreateModal from '@brftech/filex-core/src/modals/ArchiveCreateModal.vue';
import ArchivePasswordModal from '@brftech/filex-core/src/modals/ArchivePasswordModal.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';

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
