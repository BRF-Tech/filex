// GitHub #124: the built-in viewer reads a PDF from its own path,
// `/api/files/pdf/{name}?path=`, the one API path filex's pages may frame.
// Chrome checks the viewer's <object> against the page's frame-src, and the
// manager's preview URL was refused there (measured 2026-10-20). The rule
// lives in the package (lib/pdfPreview + useFileApi.previewUrl), so the
// explorer, Quick Look and the editor tab all follow it; a server that does
// not say `pdf_preview` keeps the manager's URL, which it still answers.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { resolveEndpoints, useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { isPdfPath, pdfPreviewAddress } from '@brftech/filex-core/src/lib/pdfPreview';

afterEach(() => vi.unstubAllGlobals());

function stubCapabilities(body: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })),
  );
}

describe('the PDF preview address', () => {
  it('names the file for the viewer and carries the path as the manager does', () => {
    expect(pdfPreviewAddress('/api/files/pdf', 'docs://reports/Q3 plan.pdf')).toBe(
      '/api/files/pdf/Q3%20plan.pdf?path=docs%3A%2F%2Freports%2FQ3+plan.pdf',
    );
    expect(pdfPreviewAddress('https://files.example.com/filex/api/files/pdf/', 'main://a.PDF')).toBe(
      'https://files.example.com/filex/api/files/pdf/a.PDF?path=main%3A%2F%2Fa.PDF',
    );
    const u = new URL(pdfPreviewAddress('https://x.example/api/files/pdf', 'main://dir/ç#?&.pdf'));
    expect(u.pathname).toBe('/api/files/pdf/%C3%A7%23%3F%26.pdf');
    expect(u.searchParams.get('path')).toBe('main://dir/ç#?&.pdf');
  });

  it('is for a PDF only, by the extension the server reads', () => {
    expect(isPdfPath('main://a.pdf')).toBe(true);
    expect(isPdfPath('main://A.PDF')).toBe(true);
    expect(isPdfPath('main://a.pdf.txt')).toBe(false);
    expect(isPdfPath('main://pdf')).toBe(false);
    expect(isPdfPath('main://folder.pdf/x.html')).toBe(false);
  });

  it('is derived from apiBase like every other endpoint, and an explicit one wins', () => {
    expect(resolveEndpoints({ apiBase: 'https://files.example.com' } as never).pdfPreview).toBe(
      'https://files.example.com/api/files/pdf',
    );
    expect(resolveEndpoints({ apiBase: '/proxy/filex', pdfPreview: '/custom/pdf' } as never).pdfPreview).toBe('/custom/pdf');
    expect(resolveEndpoints({ endpoint: '/legacy/manager' } as never).pdfPreview).toBeNull();
  });
});

describe('useFileApi().previewUrl', () => {
  it('keeps the manager for a PDF until the server says pdf_preview', async () => {
    stubCapabilities({});
    const api = useFileApi({ apiBase: '/proxy/filex' } as never);
    expect(api.previewUrl('main://a.pdf')).toContain('/proxy/filex/api/files/manager?');
    await api.capabilities();
    expect(api.previewUrl('main://a.pdf')).toContain('/proxy/filex/api/files/manager?');
  });

  it('uses the PDF path for a PDF once the server says pdf_preview, and the manager for the rest', async () => {
    stubCapabilities({ pdf_preview: true });
    const api = useFileApi({ apiBase: '/proxy/filex' } as never);
    await api.capabilities();
    expect(api.previewUrl('main://docs/a.pdf')).toBe('/proxy/filex/api/files/pdf/a.pdf?path=main%3A%2F%2Fdocs%2Fa.pdf');
    expect(api.previewUrl('main://docs/a.txt')).toContain('/proxy/filex/api/files/manager?');
    expect(api.previewUrl('main://docs/a.txt')).toContain('action=preview');
    // The download stays the download.
    expect(api.downloadUrl('main://docs/a.pdf')).toContain('action=download');
  });

  it('uses a path the host names from the start (the web app, for its own server)', () => {
    const api = useFileApi({ apiBase: '', pdfPreview: '/api/files/pdf' } as never);
    expect(api.previewUrl('main://a.pdf')).toBe('/api/files/pdf/a.pdf?path=main%3A%2F%2Fa.pdf');
  });

  it('never uses it for a legacy host with only a manager endpoint', async () => {
    stubCapabilities({ pdf_preview: true });
    const api = useFileApi({ endpoint: '/legacy/manager', capabilities: '/legacy/caps' } as never);
    await api.capabilities();
    expect(api.previewUrl('main://a.pdf')).toContain('/legacy/manager?');
  });
});

describe('the built-in viewer', () => {
  const REPO = path.resolve(__dirname, '../../..');
  const modal = readFileSync(path.join(REPO, 'packages/core/src/modals/PreviewModal.vue'), 'utf8');
  const pdfBranch = modal.slice(modal.indexOf("v-else-if=\"kind === 'pdf'\""), modal.indexOf("v-else-if=\"kind === 'markdown'\""));

  it('reads the PDF from the preview URL, and its fallback opens that preview, not the download', () => {
    expect(pdfBranch).toContain('<object :data="src" type="application/pdf"');
    expect(pdfBranch).toMatch(/<a :href="src"[^>]*target="_blank"[^>]*>\{\{ t\('viewer\.open_in_new_tab'\) \}\}<\/a>/);
    expect(pdfBranch).not.toContain(':href="download"');
  });

  it('is handed the same rule in the editor tab and the web app', () => {
    const editor = readFileSync(path.join(REPO, 'web/src/views/Editor.vue'), 'utf8');
    expect(editor).toContain('const previewUrl = (p: string) => fileApi.previewUrl(p);');
    expect(editor).toContain("pdfPreview: api('/api/files/pdf')");
    const explore = readFileSync(path.join(REPO, 'web/src/views/Explore.vue'), 'utf8');
    expect(explore).toContain('pdfPreview: `${getServerRoot()}/api/files/pdf`');
  });
});
