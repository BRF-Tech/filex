/**
 * The AI assistant's client (task #269): the routes the explorer's panel and
 * the user settings dialog call, and the server-sent events a reply streams
 * as.
 *
 * ⚠⚠ Nothing is decided here. Whether the button is drawn is the server's
 * answer (`capabilities.assistant.button`); every sentence the panel shows - a
 * step's name, its status, an approval card's question, why a request stopped
 * - arrives already said in the person's language (backend
 * handlers/assistant_state.go). This file moves JSON; the panel draws it.
 *
 * ⚠ The routes live under /api/files/assistant, derived from the explorer's
 * manager endpoint like the drafts' and the permissions', so an embed whose
 * proxy forwards /api/files/* reaches them with the explorer's own credential.
 */

/** `capabilities.assistant`: the state for this caller and whether the
 *  explorer draws the button. */
export interface AssistantCapability {
  state: 'on' | 'off' | 'not_configured';
  permitted: boolean;
  button: boolean;
}

/** `GET /api/files/assistant`: the assistant as it stands for the person. */
export interface AssistantMe extends AssistantCapability {
  retention_days: number;
  prefs: AssistantPrefs;
  /** Who reads what the person writes - said when it is on. */
  provider?: string;
  model?: string;
}

export interface AssistantPrefs {
  /** Every change waits for the person's approval, not only the ones that
   *  cannot be undone. */
  confirm_all: boolean;
}

/** A step - one tool call - as the server says it. */
export interface AssistantStep {
  id: number;
  tool: string;
  label: string;
  class: 'read' | 'change' | 'confirm';
  status: 'running' | 'done' | 'failed' | 'waiting' | 'denied' | 'undone' | 'not_run';
  status_label: string;
  target?: string;
  to?: string;
  error?: string;
  can_undo: boolean;
  /** An approval card's: what will happen and why it waits. */
  question?: string;
  note?: string;
  args?: Record<string, unknown>;
}

/** One line of a conversation. */
export interface AssistantItem {
  kind: 'user' | 'assistant' | 'step' | 'note';
  text?: string;
  /** What the person had selected with that message. */
  paths?: string[];
  step?: AssistantStep;
}

/** A conversation as the panel draws it. */
export interface AssistantConversation {
  id: number;
  title: string;
  updated_ms: number;
  provider: string;
  model: string;
  items: AssistantItem[];
  /** The step waiting for the person, when one is. */
  waiting?: AssistantStep;
}

/** A row of the person's conversation list. */
export interface AssistantSummary {
  id: number;
  title: string;
  created_ms: number;
  updated_ms: number;
}

/** What a reply streams. */
export type AssistantEvent =
  | { name: 'conversation'; data: { id: number; title: string } }
  | { name: 'text'; data: { delta: string } }
  | { name: 'step'; data: AssistantStep }
  | { name: 'approval'; data: AssistantStep }
  | { name: 'done'; data: { reason: string; conversation: AssistantConversation } }
  | { name: 'error'; data: { error: string; message: string; detail?: string } };

/** The transport the client rides on: the explorer's (useFileApi) - its
 *  credential, its language, its credentials mode. */
export interface AssistantTransport {
  request(url: string, init?: RequestInit): Promise<Response>;
  json<T>(url: string, init?: RequestInit): Promise<T>;
  /** A refusal answered before a stream began, said as the transport says
   *  every other one (lib/errorWords requestFailure). */
  failure(status: number, body: string): Error;
}

/** /api/files/assistant, from the manager endpoint the explorer was given. */
export function assistantBase(managerEndpoint: string): string {
  return managerEndpoint.replace(/\/manager(\?.*)?$/, '/assistant');
}

/**
 * Splits a text stream into server-sent events. Feed it chunks as they come;
 * it calls `emit` for every complete event and keeps the rest for the next
 * chunk. An event whose data is not JSON is dropped - the server sends none.
 */
export function sseParser(emit: (e: AssistantEvent) => void): (chunk: string, end?: boolean) => void {
  let buf = '';
  const flush = (block: string) => {
    let name = '';
    const data: string[] = [];
    for (const line of block.split('\n')) {
      if (line.startsWith('event:')) name = line.slice(6).trim();
      else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
    }
    if (!name || data.length === 0) return;
    try {
      emit({ name, data: JSON.parse(data.join('\n')) } as AssistantEvent);
    } catch {
      /* not JSON: nothing the panel could draw */
    }
  };
  return (chunk: string, end = false) => {
    buf += chunk.replace(/\r\n/g, '\n');
    let i = buf.indexOf('\n\n');
    while (i >= 0) {
      flush(buf.slice(0, i));
      buf = buf.slice(i + 2);
      i = buf.indexOf('\n\n');
    }
    if (end && buf.trim()) {
      flush(buf);
      buf = '';
    }
  };
}

/** Reads a streamed reply to its end, event by event. A refusal before the
 *  stream (a 404, a 429) is thrown as the transport throws it. */
async function stream(
  t: AssistantTransport,
  url: string,
  body: unknown,
  onEvent: (e: AssistantEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await t.request(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify(body),
    signal,
  });
  if (!res.ok) {
    // The JSON refusal (assistant_off, assistant_busy…): said by the
    // transport, like every other refusal the explorer meets.
    throw t.failure(res.status, await res.text().catch(() => ''));
  }
  const feed = sseParser(onEvent);
  const reader = res.body?.getReader();
  if (!reader) {
    feed(await res.text(), true);
    return;
  }
  const dec = new TextDecoder();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    feed(dec.decode(value, { stream: true }));
  }
  feed(dec.decode(), true);
}

/** The client every surface uses - the panel and the settings dialog. */
export function assistantClient(t: AssistantTransport, base: string) {
  const root = base.replace(/\/+$/, '');
  const json = (method: string, body?: unknown): RequestInit => ({
    method,
    headers: { 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  return {
    me: () => t.json<AssistantMe>(`${root}`),
    setPrefs: (p: AssistantPrefs) => t.json<AssistantPrefs>(`${root}/prefs`, json('PUT', p)),
    list: async () => (await t.json<{ conversations: AssistantSummary[] }>(`${root}/conversations`)).conversations ?? [],
    get: (id: number) => t.json<AssistantConversation>(`${root}/conversations/${id}`),
    remove: (id: number) => t.json<void>(`${root}/conversations/${id}`, { method: 'DELETE' }),
    clear: () => t.json<void>(`${root}/conversations`, { method: 'DELETE' }),
    chat: (
      body: { conversation_id?: number; message: string; paths?: string[] },
      onEvent: (e: AssistantEvent) => void,
      signal?: AbortSignal,
    ) => stream(t, `${root}/chat`, body, onEvent, signal),
    decide: (id: number, approve: boolean, onEvent: (e: AssistantEvent) => void, signal?: AbortSignal) =>
      stream(t, `${root}/conversations/${id}/decide`, { approve }, onEvent, signal),
    undo: (id: number, stepId: number) =>
      t.json<{ step: AssistantStep; conversation: AssistantConversation }>(
        `${root}/conversations/${id}/undo`,
        json('POST', { step_id: stepId }),
      ),
  };
}

export type AssistantClient = ReturnType<typeof assistantClient>;
