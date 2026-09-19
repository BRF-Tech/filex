// The admin panel says, once, that FILEX_PUBLIC_URL was never set.
//
// Issue #32: a compose file set `FILEX_APPLICATION_URL` — a variable filex has
// never read — and every share link came out as http://localhost:5212. Nothing
// in the panel said so; the person who found out was whoever got the link.
// The server now says `public_url_configured` on /capabilities, and the store
// turns that into one fact the layout can put on the door.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { useCapabilitiesStore } from '@/stores/capabilities';
import type { Capabilities } from '@/api/types';

const fetchMock = vi.fn<() => Promise<Partial<Capabilities>>>();

vi.mock('@/api/capabilities', () => ({
  CapabilitiesApi: { fetch: () => fetchMock() },
}));

const base: Partial<Capabilities> = { version: '0.42.1', build: 'test', storage_drivers: ['local'] };

describe('capabilities.publicUrlUnset', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fetchMock.mockReset();
  });

  it('is quiet until the server has actually answered', () => {
    const caps = useCapabilitiesStore();
    expect(caps.publicUrlUnset).toBe(false);
  });

  it('raises the sign when the operator never chose a public URL', async () => {
    fetchMock.mockResolvedValue({ ...base, public_url_configured: false });
    const caps = useCapabilitiesStore();
    await caps.fetch();
    expect(caps.publicUrlUnset).toBe(true);
  });

  it('stays quiet when one was chosen', async () => {
    fetchMock.mockResolvedValue({
      ...base,
      public_url_configured: true,
      public_url: 'https://files.example.com',
    });
    const caps = useCapabilitiesStore();
    await caps.fetch();
    expect(caps.publicUrlUnset).toBe(false);
  });

  it("stays quiet on a tenant host, whose own origin is the configured answer", async () => {
    fetchMock.mockResolvedValue({
      ...base,
      public_url_configured: false,
      public_url: 'https://files.tenant-a.test',
    });
    const caps = useCapabilitiesStore();
    await caps.fetch();
    expect(caps.publicUrlUnset).toBe(false);
  });

  it('stays quiet against a server too old to say either way', async () => {
    fetchMock.mockResolvedValue({ ...base });
    const caps = useCapabilitiesStore();
    await caps.fetch();
    expect(caps.publicUrlUnset).toBe(false);
  });
});
