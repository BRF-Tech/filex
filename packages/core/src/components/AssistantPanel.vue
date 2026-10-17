<script setup lang="ts">
/**
 * The AI assistant's panel (task #269) - the same component in the web
 * explorer, an embedded one and the desktop app, docked where the details
 * panel docks.
 *
 * What it draws, it is told: the conversation comes from the server already
 * said in the person's language (lib/assistant), the steps with their names,
 * statuses and approval questions, the notes between the turns. The panel
 * adds the person's own message while it travels, the reply's words while
 * they stream, and nothing else of its own.
 *
 * ⚠ A step that cannot be undone (a public link, a permission, an app run)
 * waits on a card with two buttons - the server holds the call until the
 * person answers. A change that can (a move, a rename, an edit, a delete to
 * the trash) is done at once and carries an Undo.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Ban, Check, CircleAlert, History, LoaderCircle, Plus, Send, Sparkles, Square, Trash2, Undo2, X } from 'lucide-vue-next';
import { useLocale } from '../composables/useLocale';
import { markdownToSafeHtml } from '../lib/markdownHtml';
import { serverWords } from '../lib/errorWords';
import type {
  AssistantClient,
  AssistantConversation,
  AssistantEvent,
  AssistantItem,
  AssistantMe,
  AssistantStep,
  AssistantSummary,
} from '../lib/assistant';

const props = defineProps<{
  client: AssistantClient;
  locale?: string;
  /** What is selected in the focused pane: offered to go with a message. */
  selection: string[];
  /** A narrow explorer: the panel covers it, like the details panel. */
  narrow?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** A step changed files: the listing on screen is stale. */
  (e: 'changed'): void;
}>();

const { t, formatDate } = useLocale(() => props.locale ?? 'en');

const me = ref<AssistantMe | null>(null);
const conv = ref<AssistantConversation | null>(null);
const showHistory = ref(false);
const history = ref<AssistantSummary[]>([]);
const input = ref('');
const withSelection = ref(true);
const busy = ref(false);
const error = ref('');
let controller: AbortController | null = null;

/** The request in flight: the person's message, the reply's words so far and
 *  the steps as they arrive - drawn under the conversation until `done`
 *  replaces all of it with what the server kept. */
const live = ref<{ text: string; paths: string[]; reply: string; steps: AssistantStep[] } | null>(null);

const scroller = ref<HTMLElement | null>(null);
const field = ref<HTMLTextAreaElement | null>(null);

const items = computed<AssistantItem[]>(() => conv.value?.items ?? []);
const waiting = computed<AssistantStep | null>(() => {
  const fromLive = live.value?.steps.find((s) => s.status === 'waiting');
  return fromLive ?? conv.value?.waiting ?? null;
});
const selectionCount = computed(() => props.selection.length);
const providerLine = computed(() => {
  const m = me.value;
  if (!m?.provider || !m.model) return '';
  return `${providerName(m.provider)} · ${m.model}`;
});

/** A provider's name as its maker writes it - a name, not a sentence. */
function providerName(kind: string): string {
  switch (kind) {
    case 'anthropic':
      return 'Anthropic';
    case 'openai':
      return 'OpenAI';
    case 'ollama':
      return 'Ollama';
  }
  return kind;
}

/** The last part of a path, for a step's line (the whole path is its title). */
function leaf(p?: string): string {
  if (!p) return '';
  const cut = p.replace(/\/+$/, '');
  const i = cut.lastIndexOf('/');
  return i >= 0 && i < cut.length - 1 ? cut.slice(i + 1) : cut;
}

const rendered = ref<Record<string, string>>({});
/** An assistant's words as Markdown, sanitized (lib/markdownHtml); the text
 *  itself until that is ready, or when markdown-it is not there. */
async function render(text: string) {
  if (rendered.value[text] !== undefined) return;
  // The assistant's own policy: a file it read can steer what it writes, and
  // nothing in the answer may make the browser fetch from elsewhere.
  const html = await markdownToSafeHtml(text, 'assistant');
  if (html !== null) rendered.value = { ...rendered.value, [text]: html };
}
watch(
  items,
  (list) => {
    for (const it of list) if (it.kind === 'assistant' && it.text) void render(it.text);
  },
  { immediate: true },
);

function scrollDown() {
  void nextTick(() => {
    const el = scroller.value;
    if (el) el.scrollTop = el.scrollHeight;
  });
}

function upsertStep(s: AssistantStep) {
  if (!live.value) return;
  const i = live.value.steps.findIndex((x) => x.id === s.id);
  if (i >= 0) live.value.steps.splice(i, 1, s);
  else live.value.steps.push(s);
  if (s.class !== 'read' && s.status === 'done') emit('changed');
}

function onEvent(e: AssistantEvent) {
  switch (e.name) {
    case 'conversation':
      if (!conv.value || conv.value.id !== e.data.id) {
        conv.value = { id: e.data.id, title: e.data.title, updated_ms: Date.now(), provider: '', model: '', items: conv.value?.items ?? [] };
      }
      break;
    case 'text':
      if (live.value) live.value.reply += e.data.delta;
      break;
    case 'step':
    case 'approval':
      upsertStep(e.data);
      break;
    case 'error':
      error.value = e.data.message || e.data.error;
      break;
    case 'done':
      conv.value = e.data.conversation;
      live.value = null;
      break;
  }
  scrollDown();
}

async function run(go: (signal: AbortSignal) => Promise<void>) {
  error.value = '';
  busy.value = true;
  controller = new AbortController();
  try {
    await go(controller.signal);
  } catch (e) {
    if ((e as Error)?.name !== 'AbortError') error.value = serverWords(e, props.locale);
    // A stopped or broken stream: what the server kept is the truth.
    if (conv.value?.id) {
      try {
        conv.value = await props.client.get(conv.value.id);
      } catch {
        /* the error above already says it */
      }
    }
    live.value = null;
  } finally {
    busy.value = false;
    controller = null;
    scrollDown();
  }
}

async function send() {
  const text = input.value.trim();
  if (!text || busy.value) return;
  const paths = withSelection.value ? [...props.selection] : [];
  input.value = '';
  live.value = { text, paths, reply: '', steps: [] };
  scrollDown();
  await run((signal) =>
    props.client.chat(
      { conversation_id: conv.value?.id || undefined, message: text, paths: paths.length ? paths : undefined },
      onEvent,
      signal,
    ),
  );
}

async function decide(approve: boolean) {
  const id = conv.value?.id;
  if (!id || busy.value) return;
  live.value = { text: '', paths: [], reply: '', steps: [] };
  await run((signal) => props.client.decide(id, approve, onEvent, signal));
}

function stop() {
  controller?.abort();
}

async function undo(step: AssistantStep) {
  const id = conv.value?.id;
  if (!id || busy.value) return;
  error.value = '';
  try {
    const out = await props.client.undo(id, step.id);
    conv.value = out.conversation;
    emit('changed');
  } catch (e) {
    error.value = serverWords(e, props.locale);
  }
}

function newConversation() {
  if (busy.value) return;
  conv.value = null;
  live.value = null;
  error.value = '';
  showHistory.value = false;
  void nextTick(() => field.value?.focus());
}

async function openHistory() {
  showHistory.value = !showHistory.value;
  if (!showHistory.value) return;
  try {
    history.value = await props.client.list();
  } catch (e) {
    error.value = serverWords(e, props.locale);
  }
}

async function openConversation(id: number) {
  try {
    conv.value = await props.client.get(id);
    showHistory.value = false;
    scrollDown();
  } catch (e) {
    error.value = serverWords(e, props.locale);
  }
}

async function removeConversation(id: number) {
  try {
    await props.client.remove(id);
    history.value = history.value.filter((c) => c.id !== id);
    if (conv.value?.id === id) conv.value = null;
  } catch (e) {
    error.value = serverWords(e, props.locale);
  }
}

function onKey(ev: KeyboardEvent) {
  if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing) {
    ev.preventDefault();
    void send();
  }
}

/** A waiting step's arguments, one line each, for the card. */
function argLines(step: AssistantStep): Array<[string, string]> {
  return Object.entries(step.args ?? {}).map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)]);
}

onMounted(async () => {
  try {
    me.value = await props.client.me();
  } catch {
    /* the panel works without the provider line */
  }
  void nextTick(() => field.value?.focus());
});
onBeforeUnmount(() => controller?.abort());
</script>

<template>
  <aside
    class="fe-assistant"
    :class="{ 'fe-assistant--overlay': narrow }"
    role="complementary"
    :aria-label="t('ai.assistant')"
    data-testid="assistant-panel"
  >
    <header class="fe-assistant__head">
      <Sparkles class="fe-assistant__mark" aria-hidden="true" />
      <div class="fe-assistant__head-text">
        <h2 class="fe-assistant__title">{{ conv?.title || t('ai.assistant') }}</h2>
        <p v-if="providerLine" class="fe-assistant__provider" data-testid="assistant-provider">{{ providerLine }}</p>
      </div>
      <button
        type="button"
        class="fe-assistant__iconbtn"
        :title="t('assistant.new')"
        :aria-label="t('assistant.new')"
        :disabled="busy"
        data-testid="assistant-new"
        @click="newConversation"
      >
        <Plus aria-hidden="true" />
      </button>
      <button
        type="button"
        class="fe-assistant__iconbtn"
        :class="{ 'is-active': showHistory }"
        :title="t('assistant.history')"
        :aria-label="t('assistant.history')"
        :aria-pressed="showHistory"
        data-testid="assistant-history"
        @click="openHistory"
      >
        <History aria-hidden="true" />
      </button>
      <button
        type="button"
        class="fe-assistant__iconbtn"
        :title="t('assistant.close')"
        :aria-label="t('assistant.close')"
        data-testid="assistant-close"
        @click="emit('close')"
      >
        <X aria-hidden="true" />
      </button>
    </header>

    <div v-if="showHistory" class="fe-assistant__history" data-testid="assistant-history-list">
      <p v-if="history.length === 0" class="fe-assistant__muted">{{ t('assistant.history_empty') }}</p>
      <ul v-else class="fe-assistant__convs">
        <li v-for="c in history" :key="c.id" class="fe-assistant__conv">
          <button type="button" class="fe-assistant__conv-open" @click="openConversation(c.id)">
            <span class="fe-assistant__conv-title"><bdi>{{ c.title || t('ai.assistant') }}</bdi></span>
            <span class="fe-assistant__conv-date">{{ formatDate(c.updated_ms, { time: true }) }}</span>
          </button>
          <button
            type="button"
            class="fe-assistant__iconbtn"
            :title="t('assistant.delete')"
            :aria-label="t('assistant.delete')"
            @click="removeConversation(c.id)"
          >
            <Trash2 aria-hidden="true" />
          </button>
        </li>
      </ul>
    </div>

    <div v-else ref="scroller" class="fe-assistant__scroll" aria-live="polite">
      <p v-if="items.length === 0 && !live" class="fe-assistant__intro" data-testid="assistant-intro">{{ t('assistant.intro') }}</p>

      <template v-for="(it, i) in items" :key="i">
        <div v-if="it.kind === 'user'" class="fe-assistant__msg fe-assistant__msg--user">
          <p class="fe-assistant__text">{{ it.text }}</p>
          <ul v-if="it.paths && it.paths.length" class="fe-assistant__chips">
            <li v-for="p in it.paths" :key="p" class="fe-assistant__chip" :title="p"><bdi>{{ leaf(p) }}</bdi></li>
          </ul>
        </div>
        <div v-else-if="it.kind === 'assistant'" class="fe-assistant__msg fe-assistant__msg--bot">
          <!-- eslint-disable-next-line vue/no-v-html -- markdown-it output through lib/sanitizeHtml's assistant policy (lib/markdownHtml) -->
          <div v-if="it.text && rendered[it.text] !== undefined" class="fe-assistant__md" v-html="rendered[it.text]"></div>
          <p v-else class="fe-assistant__text">{{ it.text }}</p>
        </div>
        <div v-else-if="it.kind === 'note'" class="fe-assistant__note" role="note">{{ it.text }}</div>
        <div
          v-else-if="it.kind === 'step' && it.step && it.step.status !== 'waiting'"
          class="fe-assistant__step"
          :class="`is-${it.step.status}`"
          :data-testid="`assistant-step-${it.step.id}`"
        >
          <Check v-if="it.step.status === 'done'" class="fe-assistant__step-icon" aria-hidden="true" />
          <Undo2 v-else-if="it.step.status === 'undone'" class="fe-assistant__step-icon" aria-hidden="true" />
          <Ban v-else-if="it.step.status === 'denied' || it.step.status === 'not_run'" class="fe-assistant__step-icon" aria-hidden="true" />
          <CircleAlert v-else class="fe-assistant__step-icon" aria-hidden="true" />
          <span class="fe-assistant__step-body">
            <span class="fe-assistant__step-label">{{ it.step.label }}</span>
            <span v-if="it.step.target" class="fe-assistant__step-target" :title="it.step.target"><bdi>{{ leaf(it.step.target) }}</bdi></span>
            <span v-if="it.step.to" class="fe-assistant__step-to" :title="it.step.to"><bdi>{{ leaf(it.step.to) }}</bdi></span>
            <span class="fe-assistant__step-status">{{ it.step.status_label }}</span>
            <span v-if="it.step.error" class="fe-assistant__step-error">{{ it.step.error }}</span>
          </span>
          <button
            v-if="it.step.can_undo"
            type="button"
            class="fe-assistant__undo"
            :disabled="busy"
            :data-testid="`assistant-undo-${it.step.id}`"
            @click="undo(it.step)"
          >
            {{ t('assistant.undo') }}
          </button>
        </div>
      </template>

      <template v-if="live">
        <div v-if="live.text" class="fe-assistant__msg fe-assistant__msg--user">
          <p class="fe-assistant__text">{{ live.text }}</p>
          <ul v-if="live.paths.length" class="fe-assistant__chips">
            <li v-for="p in live.paths" :key="p" class="fe-assistant__chip" :title="p"><bdi>{{ leaf(p) }}</bdi></li>
          </ul>
        </div>
        <div
          v-for="s in live.steps.filter((x) => x.status !== 'waiting')"
          :key="`live-${s.id}`"
          class="fe-assistant__step"
          :class="`is-${s.status}`"
        >
          <LoaderCircle v-if="s.status === 'running'" class="fe-assistant__step-icon fe-assistant__spin" aria-hidden="true" />
          <Check v-else-if="s.status === 'done'" class="fe-assistant__step-icon" aria-hidden="true" />
          <CircleAlert v-else class="fe-assistant__step-icon" aria-hidden="true" />
          <span class="fe-assistant__step-body">
            <span class="fe-assistant__step-label">{{ s.label }}</span>
            <span v-if="s.target" class="fe-assistant__step-target" :title="s.target"><bdi>{{ leaf(s.target) }}</bdi></span>
            <span class="fe-assistant__step-status">{{ s.status_label }}</span>
          </span>
        </div>
        <div v-if="live.reply" class="fe-assistant__msg fe-assistant__msg--bot">
          <p class="fe-assistant__text">{{ live.reply }}</p>
        </div>
        <p v-else-if="busy" class="fe-assistant__muted">
          <LoaderCircle class="fe-assistant__spin fe-assistant__inline-icon" aria-hidden="true" />{{ t('assistant.working') }}
        </p>
      </template>

      <!-- The card for a step that cannot be undone: the server's question,
           why it waits, what exactly will be done, and the two answers. -->
      <section v-if="waiting" class="fe-assistant__card" role="group" :aria-label="waiting.label" data-testid="assistant-approval">
        <p class="fe-assistant__card-question">{{ waiting.question }}</p>
        <p v-if="waiting.note" class="fe-assistant__card-note">{{ waiting.note }}</p>
        <dl v-if="argLines(waiting).length" class="fe-assistant__card-args">
          <template v-for="[k, v] in argLines(waiting)" :key="k">
            <dt>{{ k }}</dt>
            <dd><bdi>{{ v }}</bdi></dd>
          </template>
        </dl>
        <div class="fe-assistant__card-actions">
          <button type="button" class="fe-btn" :disabled="busy" data-testid="assistant-decline" @click="decide(false)">
            {{ t('assistant.decline') }}
          </button>
          <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" data-testid="assistant-approve" @click="decide(true)">
            {{ t('assistant.approve') }}
          </button>
        </div>
      </section>

      <p v-if="error" class="fe-assistant__error" role="alert" data-testid="assistant-error">{{ error }}</p>
    </div>

    <footer class="fe-assistant__foot">
      <label v-if="selectionCount > 0" class="fe-assistant__with">
        <input v-model="withSelection" type="checkbox" data-testid="assistant-with-selection" />
        <span>{{ t('assistant.with_selection', { count: selectionCount }) }}</span>
      </label>
      <div class="fe-assistant__compose">
        <textarea
          ref="field"
          v-model="input"
          class="fe-assistant__input"
          rows="2"
          :placeholder="t('assistant.placeholder')"
          :aria-label="t('assistant.placeholder')"
          data-testid="assistant-input"
          @keydown="onKey"
        ></textarea>
        <button
          v-if="busy"
          type="button"
          class="fe-btn fe-assistant__send"
          :title="t('assistant.stop')"
          :aria-label="t('assistant.stop')"
          data-testid="assistant-stop"
          @click="stop"
        >
          <Square aria-hidden="true" />
        </button>
        <button
          v-else
          type="button"
          class="fe-btn fe-btn--primary fe-assistant__send"
          :title="t('assistant.send')"
          :aria-label="t('assistant.send')"
          :disabled="!input.trim()"
          data-testid="assistant-send"
          @click="send"
        >
          <Send aria-hidden="true" />
        </button>
      </div>
    </footer>
  </aside>
</template>
