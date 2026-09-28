/**
 * useDraftSave — "save this draft where it belongs", with its one question
 * (issue #71).
 *
 * Saving a draft never replaces a file. When the folder it is meant for
 * already holds a file of its name the server answers with the free name
 * beside it (`name (2).ext`) and moves nothing; the person is asked, and the
 * draft is saved under exactly that name only if they agree. The editor's
 * Save button, its close question and the Drafts view all ask it the same way
 * — this state, drawn by DraftConflictModal.
 *
 * `save(draft)` resolves with where the file went, or null when the person
 * declined; a refusal of the FIRST attempt (the folder is gone, no permission)
 * rejects, for the caller to say where it was pressed. A refusal while the
 * question is on screen is said inside it (`error`).
 */
import { ref } from 'vue';
import { draftFolderLabel, type DraftDto, type DraftsClient } from '../lib/drafts';

export interface SavedDraft {
  path: string;
  name: string;
  targetDir: string;
}

export interface DraftQuestion {
  key: string;
  name: string;
  suggested: string;
  folder: string;
}

export function useDraftSave(client: () => DraftsClient | null, say: (err: unknown) => string) {
  const busy = ref(false);
  const error = ref<string | null>(null);
  const question = ref<DraftQuestion | null>(null);
  let pending: ((r: SavedDraft | null) => void) | null = null;

  function settle(r: SavedDraft | null) {
    question.value = null;
    error.value = null;
    const p = pending;
    pending = null;
    p?.(r);
  }

  async function save(d: Pick<DraftDto, 'key'>): Promise<SavedDraft | null> {
    const api = client();
    if (!api) return null;
    busy.value = true;
    try {
      const out = await api.save(d.key);
      if (out.saved) return { path: out.path, name: out.name, targetDir: out.targetDir };
      question.value = {
        key: d.key,
        name: out.name,
        suggested: out.suggested,
        folder: draftFolderLabel(out.targetDir),
      };
      error.value = null;
    } finally {
      busy.value = false;
    }
    return new Promise<SavedDraft | null>((resolve) => {
      pending = resolve;
    });
  }

  /** The person agreed to the suggested name. */
  async function confirm() {
    const q = question.value;
    const api = client();
    if (!q || !api || busy.value) return;
    busy.value = true;
    error.value = null;
    try {
      const out = await api.save(q.key, q.suggested);
      if (out.saved) {
        settle({ path: out.path, name: out.name, targetDir: out.targetDir });
        return;
      }
      // Taken in the meantime too: ask again, with the next free name.
      question.value = { ...q, name: out.name || q.name, suggested: out.suggested };
    } catch (err) {
      error.value = say(err);
    } finally {
      busy.value = false;
    }
  }

  /** The person declined: nothing moved, the draft stays a draft. */
  function cancel() {
    if (busy.value) return;
    settle(null);
  }

  return { busy, error, question, save, confirm, cancel };
}
