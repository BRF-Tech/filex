// The AI assistant in the explorer (task #269): the toolbar button the server
// offers or not, the panel that draws what the server says, the approval card
// and the undo - and the event stream they read.
//
// RED PROOF: before 0.57 the toolbar drew a disabled "AI assistant - coming
// soon" button for everybody (data-testid header-ai, `disabled`), there was no
// panel and no lib/assistant.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import Toolbar from '@brftech/filex-core/src/components/Toolbar.vue';
import AssistantPanel from '@brftech/filex-core/src/components/AssistantPanel.vue';
import {
  assistantBase,
  sseParser,
  type AssistantClient,
  type AssistantConversation,
  type AssistantEvent,
  type AssistantStep,
} from '@brftech/filex-core/src/lib/assistant';

const toolbarProps = {
  viewMode: 'list' as const,
  searchQuery: '',
  trashActive: false,
  actions: [],
  locale: 'en' as const,
  shell: 'drive' as const,
};

describe('the toolbar button', () => {
  it('is not there at all unless the server offers it - not a disabled one', async () => {
    const w = mount(Toolbar, { props: toolbarProps, attachTo: document.body });
    await nextTick();
    expect(w.find('[data-testid="header-ai"]').exists()).toBe(false);
    expect(w.find('.fe-toolbar__soon').exists()).toBe(false);
    expect(w.text()).not.toContain('coming soon');
    w.unmount();
  });

  it('is a live button when it is offered, pressed while the panel is open', async () => {
    const w = mount(Toolbar, { props: { ...toolbarProps, assistantButton: true, assistantOpen: false }, attachTo: document.body });
    await nextTick();
    const btn = w.find('[data-testid="header-ai"]');
    expect(btn.exists()).toBe(true);
    expect(btn.attributes('disabled')).toBeUndefined();
    expect(btn.attributes('aria-label')).toBe('AI assistant');
    await btn.trigger('click');
    expect(w.emitted('toggle-assistant')).toHaveLength(1);
    await w.setProps({ assistantOpen: true });
    expect(w.find('[data-testid="header-ai"]').attributes('aria-pressed')).toBe('true');
    w.unmount();
  });
});

describe('the event stream', () => {
  it('splits events across chunks, whatever the line ends', () => {
    const got: AssistantEvent[] = [];
    const feed = sseParser((e) => got.push(e));
    feed('event: text\ndata: {"delta":"Hel');
    feed('lo"}\n\nevent: text\r\ndata: {"delta":" there"}\r\n\r\n');
    feed('event: done\ndata: {"reason":"end","conversation":{"id":3}}', true);
    expect(got.map((e) => e.name)).toEqual(['text', 'text', 'done']);
    expect((got[0].data as { delta: string }).delta).toBe('Hello');
    expect((got[1].data as { delta: string }).delta).toBe(' there');
  });

  it('lives under /api/files, beside the manager the explorer was given', () => {
    expect(assistantBase('/api/files/manager')).toBe('/api/files/assistant');
    expect(assistantBase('https://files.example.com/api/files/manager?x=1')).toBe('https://files.example.com/api/files/assistant');
  });
});

const step = (over: Partial<AssistantStep>): AssistantStep => ({
  id: 1,
  tool: 'file_move',
  label: 'Move',
  class: 'change',
  status: 'done',
  status_label: 'Done',
  target: 'main://docs/a.txt',
  to: 'main://docs/b.txt',
  can_undo: true,
  ...over,
});

const conversation = (over: Partial<AssistantConversation>): AssistantConversation => ({
  id: 7,
  title: 'rename a.txt',
  updated_ms: 0,
  provider: 'openai',
  model: 'm',
  items: [],
  ...over,
});

function fakeClient(over: Partial<AssistantClient> = {}): AssistantClient {
  return {
    me: vi.fn().mockResolvedValue({ state: 'on', permitted: true, button: true, retention_days: 30, prefs: { confirm_all: false }, provider: 'openai', model: 'gpt-test' }),
    setPrefs: vi.fn(),
    list: vi.fn().mockResolvedValue([]),
    get: vi.fn(),
    remove: vi.fn(),
    clear: vi.fn(),
    chat: vi.fn(),
    decide: vi.fn(),
    undo: vi.fn(),
    ...over,
  } as AssistantClient;
}

describe('the panel', () => {
  it('says who reads what is written, sends the message with the selection, and draws the stream', async () => {
    const done = conversation({
      items: [
        { kind: 'user', text: 'rename a.txt', paths: ['main://docs/a.txt'] },
        { kind: 'step', step: step({}) },
        { kind: 'assistant', text: 'Renamed.' },
      ],
    });
    const chat = vi.fn(async (_body: unknown, onEvent: (e: AssistantEvent) => void) => {
      onEvent({ name: 'conversation', data: { id: 7, title: 'rename a.txt' } });
      onEvent({ name: 'step', data: step({ status: 'running', can_undo: false }) });
      onEvent({ name: 'text', data: { delta: 'Renamed.' } });
      onEvent({ name: 'done', data: { reason: 'end', conversation: done } });
    });
    const client = fakeClient({ chat });
    const w = mount(AssistantPanel, { props: { client, locale: 'en', selection: ['main://docs/a.txt'] }, attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="assistant-provider"]').text()).toBe('OpenAI · gpt-test');

    await w.find('[data-testid="assistant-input"]').setValue('rename a.txt');
    await w.find('[data-testid="assistant-send"]').trigger('click');
    await flushPromises();
    expect(chat).toHaveBeenCalledWith(
      { conversation_id: undefined, message: 'rename a.txt', paths: ['main://docs/a.txt'] },
      expect.any(Function),
      expect.anything(),
    );
    expect(w.text()).toContain('Renamed.');
    expect(w.find('[data-testid="assistant-step-1"]').text()).toContain('Move');
    expect(w.emitted('changed')).toBeUndefined();
    w.unmount();
  });

  it('a step that cannot be undone waits on a card; the answer goes to the server', async () => {
    const waiting = step({ id: 2, tool: 'file_share', label: 'Public link', class: 'confirm', status: 'waiting', status_label: 'Waiting for your approval', question: 'Create a public link to main://docs/a.txt?', note: 'This cannot be undone from here.', args: { path: 'main://docs/a.txt' }, can_undo: false, to: undefined });
    const decide = vi.fn(async (_id: number, _approve: boolean, onEvent: (e: AssistantEvent) => void) => {
      onEvent({ name: 'done', data: { reason: 'end', conversation: conversation({ items: [{ kind: 'step', step: { ...waiting, status: 'done', status_label: 'Done' } }] }) } });
    });
    const client = fakeClient({ get: vi.fn().mockResolvedValue(conversation({ waiting })), decide, list: vi.fn().mockResolvedValue([{ id: 7, title: 'share', created_ms: 0, updated_ms: 0 }]) });
    const w = mount(AssistantPanel, { props: { client, locale: 'en', selection: [] }, attachTo: document.body });
    await flushPromises();
    await w.find('[data-testid="assistant-history"]').trigger('click');
    await flushPromises();
    await w.find('.fe-assistant__conv-open').trigger('click');
    await flushPromises();

    const card = w.find('[data-testid="assistant-approval"]');
    expect(card.exists()).toBe(true);
    expect(card.text()).toContain('Create a public link to main://docs/a.txt?');
    expect(card.text()).toContain('This cannot be undone from here.');
    await w.find('[data-testid="assistant-approve"]').trigger('click');
    await flushPromises();
    expect(decide).toHaveBeenCalledWith(7, true, expect.any(Function), expect.anything());
    expect(w.find('[data-testid="assistant-approval"]').exists()).toBe(false);
    w.unmount();
  });

  it('an undoable step carries Undo; undoing it tells the explorer to reload', async () => {
    const after = conversation({ items: [{ kind: 'step', step: step({ status: 'undone', status_label: 'Undone', can_undo: false }) }] });
    const undo = vi.fn().mockResolvedValue({ step: step({ status: 'undone', can_undo: false }), conversation: after });
    const client = fakeClient({
      get: vi.fn().mockResolvedValue(conversation({ items: [{ kind: 'step', step: step({}) }] })),
      list: vi.fn().mockResolvedValue([{ id: 7, title: 'x', created_ms: 0, updated_ms: 0 }]),
      undo,
    });
    const w = mount(AssistantPanel, { props: { client, locale: 'en', selection: [] }, attachTo: document.body });
    await flushPromises();
    await w.find('[data-testid="assistant-history"]').trigger('click');
    await flushPromises();
    await w.find('.fe-assistant__conv-open').trigger('click');
    await flushPromises();
    await w.find('[data-testid="assistant-undo-1"]').trigger('click');
    await flushPromises();
    expect(undo).toHaveBeenCalledWith(7, 1);
    expect(w.emitted('changed')).toHaveLength(1);
    expect(w.find('[data-testid="assistant-undo-1"]').exists()).toBe(false);
    w.unmount();
  });
});
