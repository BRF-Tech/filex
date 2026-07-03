/**
 * File-type matrix: preview + download for every extension we ship in
 * the demo fixture. Catches MIME regressions (svg/md/csv inline preview
 * was broken before commit 7e2959b — vfStream now picks ext-first MIME)
 * and the PDF embed fallback (commit b67f92a — Use native browser PDF
 * viewer when pdfjs worker fetch fails).
 *
 * Strategy: drive the API directly. UI rendering is covered in 30-files
 * + 70-multi-storage. Here we just want to know that the bytes come
 * back with the right Content-Type for every extension we expect a
 * viewer to handle.
 *
 * Fixtures (uploaded once at suite start, deleted at end):
 *   - text/markdown:  demo.md
 *   - text/plain:     users.csv (CSV, displays as plain in some agents)
 *   - text/html:      sample.html
 *   - text/yaml:      config.yaml
 *   - app/json:       config.json
 *   - app/xml:        sample.xml
 *   - image/jpeg:     landscape.jpg
 *   - image/png-ish:  square.jpg
 *   - image/svg:      logo.svg
 *   - image/webp:     photo.webp
 *   - video/mp4:      sample.mp4
 *   - audio/mp3:      silence-2s.mp3
 *   - app/pdf:        dummy.pdf
 *   - app/zip:        sample.zip
 *   - text/code:      demo.{js,py}, main.go
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

// Each Playwright `request` fixture is a fresh context — cookies set by
// apiLogin() in beforeAll don't survive into the per-test request. We
// extract a Bearer token once and inject it via a custom test fixture
// so every test uses an authenticated APIRequestContext.
const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await playwright.request.newContext({ baseURL });
    const login = await ctx.post('/api/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    if (!login.ok()) {
      throw new Error(`authedRequest login failed: ${login.status()} ${await login.text()}`);
    }
    const { token } = await login.json();
    const authedCtx = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    await use(authedCtx);
    await authedCtx.dispose();
    await ctx.dispose();
  },
});

const __dirname = path.dirname(fileURLToPath(import.meta.url));

interface Sample {
  name: string;
  expectMime: RegExp;
  minSize?: number;
  contentRegex?: RegExp; // optional content sniff for text formats
}

const SAMPLES: Sample[] = [
  { name: 'demo.md', expectMime: /^text\/markdown/, contentRegex: /^# / },
  { name: 'config.json', expectMime: /^application\/json/, contentRegex: /^\{/ },
  { name: 'config.yaml', expectMime: /^text\/yaml/, contentRegex: /:/ },
  { name: 'sample.xml', expectMime: /^application\/xml/, contentRegex: /^<\?xml|^<[a-z]/ },
  { name: 'sample.html', expectMime: /^text\/html/, contentRegex: /<html|<!doctype/i },
  { name: 'logo.svg', expectMime: /^image\/svg\+xml/, contentRegex: /<svg/i },
  { name: 'landscape.jpg', expectMime: /^image\/jpeg/, minSize: 16 },
  { name: 'square.jpg', expectMime: /^image\/jpeg/, minSize: 16 },
  { name: 'photo.webp', expectMime: /^image\/webp/, minSize: 16 },
  { name: 'sample.mp4', expectMime: /^video\/mp4/, minSize: 16 },
  { name: 'silence-2s.mp3', expectMime: /^(audio\/mpeg|audio\/mp3)/, minSize: 16 },
  { name: 'dummy.pdf', expectMime: /^application\/pdf/, minSize: 16 },
  { name: 'sample.zip', expectMime: /^application\/(zip|octet-stream)/ },
  { name: 'users.csv', expectMime: /^(text\/csv|text\/plain)/, contentRegex: /,/ },
  { name: 'demo.js', expectMime: /^(application\/javascript|text\/javascript|text\/plain)/, contentRegex: /./ },
  { name: 'demo.py', expectMime: /^(text\/x-python|text\/plain)/, contentRegex: /./ },
  { name: 'main.go', expectMime: /^(text\/x-go|text\/plain)/, contentRegex: /package/ },
];

const STORAGE = process.env.E2E_FILES_STORAGE ?? 'main';
const FOLDER = `e2e-types-${process.env.E2E_RUN_ID ?? Date.now()}`;
const FIXTURES = path.join(__dirname, '../fixtures/file-types');

async function ensureFixtures() {
  if (!fs.existsSync(FIXTURES)) {
    fs.mkdirSync(FIXTURES, { recursive: true });
  }
  // Generate just-enough fixtures if missing — keeps the suite hermetic
  // even when the demo fixtures aren't pre-seeded on the box.
  const writeIfMissing = (file: string, body: Buffer | string) => {
    const p = path.join(FIXTURES, file);
    if (!fs.existsSync(p)) fs.writeFileSync(p, body);
  };

  writeIfMissing('demo.md', '# filex sample\n\n**bold** and a [link](https://example.com).\n');
  writeIfMissing('config.json', JSON.stringify({ version: 1, demo: true, items: [1, 2, 3] }, null, 2));
  writeIfMissing('config.yaml', 'version: 1\ndemo: true\nitems:\n  - 1\n  - 2\n');
  writeIfMissing('sample.xml', '<?xml version="1.0"?><root><a>1</a><b>2</b></root>');
  writeIfMissing('sample.html', '<!doctype html><html><body><h1>filex sample</h1></body></html>');
  writeIfMissing('logo.svg', '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><circle cx="32" cy="32" r="30" fill="#3b82f6"/></svg>');
  writeIfMissing('users.csv', 'id,name,email\n1,Ada,ada@example.com\n2,Test,test@example.com\n');
  writeIfMissing('demo.js', "export const hello = () => console.log('hi from filex demo');\n");
  writeIfMissing('demo.py', "def hello():\n    print('hi from filex demo')\n");
  writeIfMissing('main.go', 'package main\n\nimport "fmt"\n\nfunc main() { fmt.Println("hi") }\n');

  // Binary fixtures — only generate placeholders if not present. Real
  // sizes don't matter; the API contract is what we're checking.
  if (!fs.existsSync(path.join(FIXTURES, 'landscape.jpg'))) {
    // Tiny 1×1 JPEG (~ 700 bytes). Just enough to satisfy the
    // content-type sniffer.
    const jpeg = Buffer.from(
      '/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD3+iiigD//Z',
      'base64',
    );
    fs.writeFileSync(path.join(FIXTURES, 'landscape.jpg'), jpeg);
    fs.writeFileSync(path.join(FIXTURES, 'square.jpg'), jpeg);
  }
  if (!fs.existsSync(path.join(FIXTURES, 'photo.webp'))) {
    const webp = Buffer.from(
      'UklGRiYAAABXRUJQVlA4IBoAAAAwAQCdASoBAAEAAUAmJaQAA3AA/v9YBQAA',
      'base64',
    );
    fs.writeFileSync(path.join(FIXTURES, 'photo.webp'), webp);
  }
  if (!fs.existsSync(path.join(FIXTURES, 'sample.mp4'))) {
    fs.writeFileSync(
      path.join(FIXTURES, 'sample.mp4'),
      Buffer.from('AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDE=', 'base64'),
    );
  }
  if (!fs.existsSync(path.join(FIXTURES, 'silence-2s.mp3'))) {
    fs.writeFileSync(
      path.join(FIXTURES, 'silence-2s.mp3'),
      Buffer.from('SUQzAwAAAAAAB1RFTkMAAAACQABUWFhYAAAAEgAAAA==', 'base64'),
    );
  }
  if (!fs.existsSync(path.join(FIXTURES, 'dummy.pdf'))) {
    const pdf = Buffer.from(
      '%PDF-1.4\n1 0 obj\n<<>>\nendobj\nxref\n0 1\n0000000000 65535 f\ntrailer\n<<>>\n%%EOF',
    );
    fs.writeFileSync(path.join(FIXTURES, 'dummy.pdf'), pdf);
  }
  if (!fs.existsSync(path.join(FIXTURES, 'sample.zip'))) {
    fs.writeFileSync(
      path.join(FIXTURES, 'sample.zip'),
      Buffer.from('UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA==', 'base64'),
    );
  }
}

async function uploadFixture(request: APIRequestContext, name: string, folder: string) {
  const buf = fs.readFileSync(path.join(FIXTURES, name));
  const res = await request.post('/api/files/manager?action=upload', {
    multipart: {
      path: `${STORAGE}://${folder}`,
      'file[]': {
        name,
        mimeType: 'application/octet-stream', // server detects real type
        buffer: buf,
      },
    },
  });
  if (!res.ok()) {
    throw new Error(`upload ${name} failed: ${res.status()} ${await res.text()}`);
  }
}

test.describe('File-type preview/download MIME contract', () => {
  test.beforeAll(async ({ authedRequest: request }) => {
    await ensureFixtures();
    // Create the test folder.
    await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE}://`, name: FOLDER },
    });
    // Upload all samples.
    for (const s of SAMPLES) {
      await uploadFixture(request, s.name, FOLDER);
    }
  });

  test.afterAll(async ({ authedRequest: request }) => {
    // Best-effort cleanup — delete each file then the folder.
    for (const s of SAMPLES) {
      await request.post('/api/files/manager?action=delete', {
        data: {
          path: `${STORAGE}://${FOLDER}`,
          items: [{ path: `${STORAGE}://${FOLDER}/${s.name}` }],
        },
      });
    }
    await request.post('/api/files/manager?action=delete', {
      data: {
        path: `${STORAGE}://`,
        items: [{ path: `${STORAGE}://${FOLDER}` }],
      },
    });
  });

  for (const s of SAMPLES) {
    test(`preview ${s.name} returns ${s.expectMime}`, async ({ authedRequest: request }) => {
      const res = await request.get(
        `/api/files/manager?action=preview&path=${encodeURIComponent(
          `${STORAGE}://${FOLDER}/${s.name}`,
        )}`,
      );
      expect(res.status(), `preview ${s.name}`).toBe(200);
      const ct = res.headers()['content-type'] ?? '';
      expect(ct, `Content-Type for ${s.name}`).toMatch(s.expectMime);
      const body = await res.body();
      if (s.minSize) {
        expect(body.length, `byte size for ${s.name}`).toBeGreaterThanOrEqual(s.minSize);
      }
      if (s.contentRegex) {
        expect(body.toString('utf8').slice(0, 200), `head bytes for ${s.name}`).toMatch(
          s.contentRegex,
        );
      }
    });

    test(`download ${s.name} carries Content-Disposition: attachment`, async ({ authedRequest: request }) => {
      const res = await request.get(
        `/api/files/manager?action=download&path=${encodeURIComponent(
          `${STORAGE}://${FOLDER}/${s.name}`,
        )}`,
      );
      expect(res.status(), `download ${s.name}`).toBe(200);
      const cd = res.headers()['content-disposition'] ?? '';
      expect(cd, `Content-Disposition for ${s.name}`).toMatch(/^attachment/);
      expect(cd, `filename for ${s.name}`).toContain(s.name);
    });
  }
});
