// A fake filex server speaking the staged upload protocol (docs/UPLOADS.md).
//
// It is a real implementation of the parts that matter — the numbered-part
// store, the contiguous offset, the refusal of a short chunk — because the
// client's whole job is to behave correctly against those rules. A stub that
// simply answered `{offset: whatever the client sent}` would pass every test
// while the product re-uploaded gigabytes.
//
// ⚠ It also COUNTS bytes. "Did it resume?" is not a question about which
// endpoints were called; it is a question about how many bytes crossed the
// wire, and that is what the tests assert.

export interface Session {
  id: string;
  path: string;
  name: string;
  total: number;
  chunkSize: number;
  parts: Map<number, Uint8Array>;
  state: 'staging' | 'committing' | 'aborted';
}

export interface HttpError extends Error {
  status?: number;
  detail?: string;
}

/** How the server should treat the next chunk PUT. */
export type ChunkFault =
  | { kind: 'network' } // connection dies; nothing is stored
  | { kind: 'short'; deliver: number } // only `deliver` bytes arrive → 400, offset unmoved
  | { kind: 'status'; code: number };

export class FakeStagedServer {
  sessions = new Map<string, Session>();
  beginCalls = 0;
  commitCalls = 0;
  statusCalls = 0;
  abortCalls = 0;
  /** Every chunk body actually accepted, in arrival order. */
  putBytes: number[] = [];
  /** Bytes the client pushed onto the wire, accepted or not. */
  wireBytes = 0;
  /** Faults keyed by (uploadId, startOffset) — consumed on first use. */
  private faults: Array<{ match: (start: number) => boolean; fault: ChunkFault }> = [];
  private seq = 0;
  /** Set to make `begin` answer as an older server with no staged path. */
  unsupported: number | null = null;

  // How GET /ops/{id} answers — the transfer the client now waits for.
  opStatus: 'running' | 'ok' | 'failed' | 'partial' = 'ok';
  opError = '';
  /** Make the op row unreadable, so the wait cannot resolve from it. */
  opUnreadable = false;
  opCalls = 0;

  failChunkAt(start: number, fault: ChunkFault): void {
    this.faults.push({ match: (s) => s === start, fault });
  }

  private takeFault(start: number): ChunkFault | null {
    const idx = this.faults.findIndex((f) => f.match(start));
    if (idx === -1) return null;
    const [f] = this.faults.splice(idx, 1);
    return f.fault;
  }

  /** Contiguous run from part 1 — the resume point, exactly as the server
   *  computes it. A part written past a hole does not move it. */
  offset(s: Session): number {
    let off = 0;
    for (let n = 1; ; n++) {
      const p = s.parts.get(n);
      if (!p) break;
      off += p.length;
    }
    return off;
  }

  received(s: Session): number {
    let t = 0;
    for (const p of s.parts.values()) t += p.length;
    return t;
  }

  complete(s: Session): boolean {
    const want = Math.ceil(s.total / s.chunkSize);
    return s.parts.size === want && this.offset(s) === s.total;
  }

  /** The assembled object, in part order. */
  assembled(id: string): Uint8Array {
    const s = this.sessions.get(id);
    if (!s) return new Uint8Array();
    const out = new Uint8Array(s.total);
    let off = 0;
    for (let n = 1; n <= Math.ceil(s.total / s.chunkSize); n++) {
      const p = s.parts.get(n);
      if (!p) break;
      out.set(p, off);
      off += p.length;
    }
    return out;
  }

  private err(status: number, message: string): HttpError {
    const e = new Error(message) as HttpError;
    e.status = status;
    return e;
  }

  // ── JSON surface (begin / status / commit / abort) ────────────────────────

  async json(url: string, init: RequestInit = {}): Promise<unknown> {
    const method = (init.method ?? 'GET').toUpperCase();
    const path = url.replace(/^https?:\/\/[^/]+/, '');

    if (path.endsWith('/upload/begin') && method === 'POST') {
      this.beginCalls++;
      if (this.unsupported != null) throw this.err(this.unsupported, 'no staged uploads here');
      const body = JSON.parse(String(init.body ?? '{}')) as {
        path: string;
        name: string;
        size: number;
        chunk_size?: number;
      };
      const id = `up-${++this.seq}`;
      const chunkSize = body.chunk_size && body.chunk_size > 0 ? body.chunk_size : 8 * 1024 * 1024;
      this.sessions.set(id, {
        id,
        path: body.path,
        name: body.name,
        total: body.size,
        chunkSize,
        parts: new Map(),
        state: 'staging',
      });
      return { id, chunk_size: chunkSize, offset: 0, total_size: body.size, state: 'staging' };
    }

    const commit = path.match(/\/upload\/([^/]+)\/commit$/);
    if (commit && method === 'POST') {
      this.commitCalls++;
      const s = this.sessions.get(decodeURIComponent(commit[1]));
      if (!s) throw this.err(404, 'upload not found');
      if (!this.complete(s)) throw this.err(409, 'upload incomplete');
      s.state = 'committing';
      return { id: s.id, op_id: 42, node_id: 7, path: `${s.path}/${s.name}`, transfer_state: 'staged' };
    }

    const one = path.match(/\/upload\/([^/]+)$/);
    if (one) {
      const id = decodeURIComponent(one[1]);
      const s = this.sessions.get(id);
      if (method === 'DELETE') {
        this.abortCalls++;
        if (!s) throw this.err(404, 'upload not found');
        this.sessions.delete(id);
        return { ok: true };
      }
      if (method === 'GET') {
        this.statusCalls++;
        if (!s) throw this.err(404, 'upload not found');
        return {
          id: s.id,
          offset: this.offset(s),
          received: this.received(s),
          total_size: s.total,
          chunk_size: s.chunkSize,
          state: s.state,
          complete: this.complete(s),
        };
      }
    }

    const op = path.match(/\/ops\/(\d+)$/);
    if (op && method === 'GET') {
      this.opCalls++;
      if (this.opUnreadable) throw this.err(404, 'op not found');
      return { id: Number(op[1]), status: this.opStatus, error: this.opError };
    }

    throw this.err(404, `no route: ${method} ${path}`);
  }

  // ── the chunk PUT ────────────────────────────────────────────────────────

  /** Returns the response body, or throws an HttpError / 'network'. */
  putChunk(
    id: string,
    contentRange: string,
    body: Uint8Array,
  ): { offset: number; received: number; total_size: number; state: string } {
    const s = this.sessions.get(id);
    if (!s) throw this.err(404, 'upload not found');
    if (s.state !== 'staging') throw this.err(409, `upload is ${s.state}`);

    const m = contentRange.match(/^bytes (\d+)-(\d+)\/(\d+)$/);
    if (!m) throw this.err(400, 'bad Content-Range');
    const start = Number(m[1]);
    const end = Number(m[2]);
    const total = Number(m[3]);
    if (total !== s.total) throw this.err(400, 'Content-Range total mismatch');
    if (start % s.chunkSize !== 0) throw this.err(400, 'chunk must start on the grid');
    const claimed = end - start + 1;

    this.wireBytes += body.length;

    const fault = this.takeFault(start);
    if (fault?.kind === 'network') throw new Error('network');
    if (fault?.kind === 'status') throw this.err(fault.code, `refused ${fault.code}`);
    const arrived = fault?.kind === 'short' ? body.slice(0, fault.deliver) : body;

    if (arrived.length !== claimed) {
      // The offset does NOT move. Accepting a partial chunk is how a resumable
      // upload silently corrupts a file.
      throw this.err(400, 'SHORT_CHUNK');
    }
    s.parts.set(start / s.chunkSize + 1, arrived);
    this.putBytes.push(arrived.length);
    return {
      offset: this.offset(s),
      received: this.received(s),
      total_size: s.total,
      state: s.state,
    };
  }
}

/** An in-memory Storage for the resume bookmarks. */
export function memoryStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
    /** test-only peek */
    _map: map,
  };
}

/** Install an XMLHttpRequest that routes chunk PUTs into the fake server. */
export function installXHR(server: FakeStagedServer): () => void {
  const original = globalThis.XMLHttpRequest;
  class FakeXHR {
    static UNSENT = 0;
    status = 0;
    responseText = '';
    withCredentials = false;
    upload = { onprogress: null as ((ev: { lengthComputable: boolean; loaded: number }) => void) | null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    private method = '';
    private url = '';
    private headers: Record<string, string> = {};

    open(method: string, url: string) {
      this.method = method;
      this.url = url;
    }
    setRequestHeader(k: string, v: string) {
      this.headers[k] = v;
    }
    abort() {
      this.onabort?.();
    }
    send(blob: Blob) {
      const id = decodeURIComponent(this.url.split('/').pop() ?? '');
      void blob.arrayBuffer().then((buf) => {
        const bytes = new Uint8Array(buf);
        this.upload.onprogress?.({ lengthComputable: true, loaded: bytes.length });
        try {
          const out = server.putChunk(id, this.headers['Content-Range'] ?? '', bytes);
          this.status = 200;
          this.responseText = JSON.stringify(out);
          this.onload?.();
        } catch (err) {
          const status = (err as HttpError).status;
          if (!status) {
            this.onerror?.();
            return;
          }
          this.status = status;
          this.responseText = JSON.stringify({ error: (err as Error).message });
          this.onload?.();
        }
      });
    }
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (globalThis as any).XMLHttpRequest = FakeXHR;
  return () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (globalThis as any).XMLHttpRequest = original;
  };
}
