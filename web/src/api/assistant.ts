import { api } from './client';

/** A provider the assistant can talk to, as the settings page offers it. */
export interface AssistantProvider {
  kind: 'anthropic' | 'openai' | 'ollama';
  default_base_url: string;
  /** Known ahead for Anthropic; the others list theirs (Test). */
  suggested_models: string[] | null;
}

/** Admin → Settings → AI assistant (GET /api/admin/assistant). ⚠ No key in
 *  it: `has_key` says whether one is stored. */
export interface AssistantSettings {
  enabled: boolean;
  hide_button: boolean;
  provider: '' | 'anthropic' | 'openai' | 'ollama';
  base_url: string;
  model: string;
  max_tool_rounds: number;
  max_output_tokens: number;
  max_request_tokens: number;
  timeout_seconds: number;
  requests_per_minute: number;
  retention_days: number;
  has_key: boolean;
  state: 'on' | 'off' | 'not_configured';
  needs_key: boolean;
  secret_key_configured: boolean;
  providers: AssistantProvider[];
  bounds: Record<string, [number, number]>;
}

export type AssistantSettingsPatch = Partial<
  Omit<AssistantSettings, 'has_key' | 'state' | 'needs_key' | 'secret_key_configured' | 'providers' | 'bounds'>
> & { api_key?: string; clear_api_key?: boolean };

/** What the connection test found; `message` is the server's sentence. */
export interface AssistantTestResult {
  ok: boolean;
  models: string[];
  model_listed?: boolean;
  message: string;
  detail?: string;
}

export const AssistantAdminApi = {
  async get(): Promise<AssistantSettings> {
    return (await api.get<AssistantSettings>('/admin/assistant')).data;
  },
  async update(patch: AssistantSettingsPatch): Promise<AssistantSettings> {
    return (await api.patch<AssistantSettings>('/admin/assistant', patch)).data;
  },
  async test(form: { provider?: string; base_url?: string; model?: string; api_key?: string }): Promise<AssistantTestResult> {
    return (await api.post<AssistantTestResult>('/admin/assistant/test', form)).data;
  },
};
