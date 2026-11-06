/**
 * The server's `SECRET_NEEDED` (backend storage.KeepSecrets): a saved credential
 * has to be typed again, because the address changed or the save turns the
 * certificate check off. The sentence is the server's, in the reader's language
 * (`server.storage.secret_*`); this says which field it is about, so the form can
 * put it there and not only in a toast that goes away.
 */
import { extractError } from '@/api/client';

export interface SecretNeeded {
  field: string;
  message: string;
}

export function secretNeededOf(e: unknown, fallback: string): SecretNeeded | null {
  const data = (e as { response?: { data?: { error?: unknown; field?: unknown } } })?.response?.data;
  if (data?.error !== 'SECRET_NEEDED' || typeof data.field !== 'string') return null;
  return { field: data.field, message: extractError(e, fallback) };
}
