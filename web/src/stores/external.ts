import { defineStore } from 'pinia';
import { ref } from 'vue';
import { probeExternalFromBrowser, type BrowserProbeResult } from '@brftech/filex-core';
import {
  ExternalApi,
  externalPublicURL,
  type ExternalServiceUpdate,
  type ExternalTestResult,
} from '@/api/external';
import type { ExternalService } from '@/api/types';
import { extractError } from '@/api/client';

/**
 * Three machines must reach three addresses before the Office editor works:
 * the browser must load the editor's JS from the document server, filex must
 * reach the same URL, and the document server must reach FILEX_PUBLIC_URL to
 * fetch the document and post the save back.
 *
 * Only the middle one used to be checked, and its green badge was read as an
 * answer to all three — an operator on podman typed the container name
 * `http://onlyoffice`, filex reached it, Test went green, and the browser
 * could not resolve that name at all (issue #17, twice).
 *
 * So this store keeps TWO results per service and never merges them:
 *  - `items[].last_state`  — the probe from the filex server.
 *  - `browserProbes[id]`   — the probe from THIS browser, which is the very
 *                            browser that will open the editor.
 *  - `callbackProbes[id]` — the third leg, measured by asking the document
 *                            server to download a one-shot URL from filex and
 *                            watching for the request to arrive. It used to be
 *                            written off as unmeasurable; it is not.
 */
export const useExternalServicesStore = defineStore('external-services', () => {
  const items = ref<ExternalService[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  /** FILEX_PUBLIC_URL as the server reports it. */
  const publicUrl = ref('');
  /** Per-service browser probe results, keyed by service id. */
  const browserProbes = ref<Record<string, BrowserProbeResult>>({});
  /** Service ids with a browser probe in flight. */
  const browserProbing = ref<Record<string, boolean>>({});
  /**
   * Per-service result of the document-server-to-filex probe, from the last
   * Test. Absent means "not measured yet" — which the page must render as a
   * question, never as a pass.
   */
  const callbackProbes = ref<Record<string, ExternalTestResult['serviceToFilex']>>({});

  async function fetch(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      items.value = await ExternalApi.list();
      publicUrl.value = externalPublicURL();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load external services');
    } finally {
      loading.value = false;
    }
  }

  async function update(id: ExternalService['id'], patch: ExternalServiceUpdate): Promise<void> {
    const updated = await ExternalApi.update(id, patch);
    items.value = items.value.map((s) => (s.id === id ? updated : s));
    // The address changed, so any browser verdict about the old one is stale.
    // Dropping it is the honest move: a leftover green next to a new URL is
    // the exact class of bug this page is being fixed for.
    const next = { ...browserProbes.value };
    delete next[id];
    browserProbes.value = next;
    // Same for the callback verdict: it was about the address that just
    // changed.
    const nextCb = { ...callbackProbes.value };
    delete nextCb[id];
    callbackProbes.value = nextCb;
  }

  /** Probe from THIS browser. Safe to call unawaited; it never throws. */
  async function probeBrowser(id: ExternalService['id']): Promise<BrowserProbeResult | null> {
    const svc = items.value.find((s) => s.id === id);
    if (!svc || !svc.url) return null;
    browserProbing.value = { ...browserProbing.value, [id]: true };
    try {
      const res = await probeExternalFromBrowser(id, svc.url);
      browserProbes.value = { ...browserProbes.value, [id]: res };
      return res;
    } catch {
      return null;
    } finally {
      const next = { ...browserProbing.value };
      delete next[id];
      browserProbing.value = next;
    }
  }

  /** Probe every configured service from this browser, in parallel. */
  async function probeAllBrowser(): Promise<void> {
    await Promise.all(items.value.filter((s) => s.url).map((s) => probeBrowser(s.id)));
  }

  /**
   * Run BOTH probes. The server result and the browser result are stored
   * separately and reported separately — that separation is the fix.
   */
  async function test(id: ExternalService['id']): Promise<ExternalTestResult> {
    const [server] = await Promise.all([ExternalApi.test(id), probeBrowser(id)]);
    publicUrl.value = server.publicURL || publicUrl.value;
    callbackProbes.value = { ...callbackProbes.value, [id]: server.serviceToFilex };
    items.value = items.value.map((s) =>
      s.id === id
        ? {
            ...s,
            last_state: server.state,
            last_error: server.error,
            last_checked_at: new Date().toISOString(),
            advisories: server.advisories,
          }
        : s,
    );
    return server;
  }

  return {
    items,
    loading,
    error,
    publicUrl,
    browserProbes,
    browserProbing,
    callbackProbes,
    fetch,
    update,
    test,
    probeBrowser,
    probeAllBrowser,
  };
});
