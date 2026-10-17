// Types for fake-llm.mjs (the AI assistant's fake model, task #269).

export interface FakeLlm {
  /** The base URL to set on the assistant (OpenAI provider), ending in /v1. */
  url: string;
  /** Every request body it was sent, in order. */
  requests: Array<{ model: string; messages: Array<{ role: string; content: unknown }>; tools?: unknown[] }>;
  close(): Promise<void>;
}

export function startFakeLlm(opts: { storage: string; file: string }): Promise<FakeLlm>;
