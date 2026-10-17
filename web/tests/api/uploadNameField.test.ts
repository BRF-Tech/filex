// The explorer's small-file upload sends every file's NAME again, as a plain
// `name[]` field, one per `file[]`, in the same order (task #238).
//
// A browser escapes a multipart filename: `"` becomes `%22`, a line break
// `%0A` (the HTML form-data rules), and the server cannot tell that from a
// file really named `100%22.txt`. So `say "hi".txt` of 8 MiB or less was
// stored as `say %22hi%22.txt`, while the same file over 8 MiB (the chunked
// path, its name in JSON) kept its quotes. The server takes `name[]` when it
// is there.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { hostileName } from '../../../e2e/helpers/hostile-names.mjs';

afterEach(() => vi.unstubAllGlobals());

let sent: FormData | null = null;

class FakeXHR {
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null };
  status = 200;
  responseText = '{"files":[]}';
  withCredentials = false;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  open() {}
  setRequestHeader() {}
  send(form: FormData) {
    sent = form;
    this.onload?.();
  }
}

describe('the explorer upload names its files twice', () => {
  it('file[] as the browser writes it, name[] as the file is called', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR as unknown as typeof XMLHttpRequest);
    sent = null;
    const names = ['double-quote', 'turkish', 'nfd-butce', 'rlo-extension'].map((id) => hostileName(id).name);
    const api = useFileApi({ apiBase: '', auth: { kind: 'none' } });
    await api.uploadMultipart(
      'main://',
      names.map((n) => new File(['x'], n, { type: 'text/plain' })),
    );
    expect(sent).not.toBeNull();
    expect(sent!.getAll('name[]').map(String)).toEqual(names);
    expect(sent!.getAll('file[]').map((f) => (f as File).name)).toEqual(names);
  });
});
