// Who may encrypt, in the explorer (wiring:e2 policy; backend
// internal/e2epolicy). The three ways to START encrypting — New folder's
// "Create encrypted folder…", a folder's and a file's "Encrypt with E2EE…" —
// each ask the server's answer for their own path, administrators included,
// and where the tenant's policy wants an approval each offers "Request
// encryption…" instead.
//
// FileExplorer.vue is never mounted whole; its wiring is pinned by reading it
// (the pattern of sharingPermissionGate / refusalToasts). The pieces it wires
// are mounted in e2eRequestModal.test.ts and lib/e2eFilesMenu.test.ts; the
// client calls are exercised here with `fetch` answering as the server does.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { resolveEndpoints, useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { FXE_ACTION_KEYS } from '@brftech/filex-core/src/composables/useE2eFiles';

const explorer = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

/** One top-level function of the explorer's script, whole. ⚠ Ends at a line
 *  that is exactly `}`: a multi-line parameter list closes with `}) {`. */
function fn(name: string): string {
  const body = explorer.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}\\n`))?.[0] ?? '';
  expect(body, `${name} was not found`).not.toBe('');
  return body;
}

describe('the explorer asks before it offers encryption', () => {
  it('asks the server for the path — administrators included — and offers what it did before when it cannot', () => {
    const at = fn('e2eAnswerAt');
    // A server that says nothing of the policy in its capabilities predates it.
    expect(at).toMatch(/if \(!policy\) return 'allowed';/);
    expect(at).toMatch(/void askE2eAnswers\(\[\{ path, kind \}\]\);/);
    // #196 - this page's answer, else the remembered one (lib/menuAnswers),
    // else 'denied' while nothing is known.
    expect(at).toMatch(/return e2eKnownAt\(path, kind\) \?\? 'denied';/);
    // The per-folder permission question skips administrators; this one must not.
    expect(at).not.toMatch(/callerAdmin|heldPermissions|isAdmin/);
    expect(fn('askE2eAnswers')).toMatch(/await api\.e2eAllowedAt\(missing, \{/);
  });

  // ⚠ `capabilities.e2e_policy` is the row of the CALLER'S OWN tenant. For the
  // platform operator on another tenant's storage the server answers with THAT
  // storage's tenant, so a "switched off" (or `available: false`) in the
  // capabilities says nothing about it and must never hide what the server's
  // per-path answer would offer.
  it('never answers from the capabilities’ own-tenant row: only the server’s per-path answer hides a door', () => {
    const at = fn('e2eAnswerAt');
    expect(at).not.toMatch(/\.available\b|\.policy\b|'off'/);
    expect(at).not.toMatch(/return 'denied';/);
  });

  it('New folder: the encrypted option and the request follow the answer for the folder it creates in', () => {
    // ⚠ Both need the folder to be one this account may create in: "allowed"
    // is not "may create" (the server's answer does not look at files.create
    // or at a read-only storage), and the dialog also opens from a key.
    expect(explorer).toMatch(
      /const newFolderE2e = computed<E2eAnswer>\(\(\) =>\s*showNewFolder\.value && !e2eActive\.value && canWriteHere\.value \? e2eAnswerAt\(qualify\(currentPath\.value\), 'new_folder'\) : 'denied',?\s*\)/,
    );
    expect(explorer).toMatch(
      /<NewFolderModal[\s\S]*?:encrypted-option="newFolderE2e === 'allowed'[\s\S]*?:encrypted-request="newFolderE2e === 'request'[\s\S]*?@request-encrypted="requestEncryptedFolder/,
    );
    expect(fn('requestEncryptedFolder')).toMatch(
      /openE2eRequest\(qualify\(currentPath\.value\), 'new_folder', folderLabelOf\(currentPath\.value\)\)/,
    );
  });

  it('a folder: "Encrypt with E2EE…" only when allowed, "Request encryption…" when an approval is wanted', () => {
    expect(explorer).toMatch(
      /\{ key: 'e2e-convert', label: t\('e2e\.convert\.ctx'\), icon: 'lock', hidden: !e2eConvertAnswerIs\(sel, w, 'allowed'\) \}/,
    );
    expect(explorer).toMatch(
      /\{ key: 'e2e-request', label: t\('e2e\.request\.ctx'\), icon: 'lock', hidden: !e2eConvertAnswerIs\(sel, w, 'request'\) \}/,
    );
    expect(fn('e2eConvertAnswerIs')).toMatch(/return e2eCanConvert\(sel, writable\) && e2eAnswerAt\(sel\[0\]\.path, 'folder'\) === want;/);
    expect(explorer).toMatch(
      /case 'e2e-request':\s*if \(targets\[0\]\) openE2eRequest\(targets\[0\]\.path, 'folder', targets\[0\]\.basename\);/,
    );
  });

  it('a file: the file menu gets the answer for the file it would encrypt', () => {
    expect(explorer).toMatch(/const fxeTarget = fxeEncryptTarget\(sel, \{ canWrite: w, inEncrypted: e2eActive\.value \}\);/);
    expect(explorer).toMatch(/encryptAnswer: fxeTarget \? e2eAnswerAt\(fxeTarget\.path, 'file'\) : undefined,/);
    expect(explorer).toMatch(/requestEncrypt: \(n\) => openE2eRequest\(n\.path, 'file', n\.basename\)/);
  });

  it('one request dialog for all three, and its answer is said', () => {
    expect(explorer).toMatch(/<E2eRequestModal[\s\S]*?:api="api"[\s\S]*?@sent="onE2eRequestSent"/);
    expect(fn('onE2eRequestSent')).toMatch(/answer\.created \? t\('e2e\.request\.sent'\) : t\('e2e\.request\.already'\)/);
  });

  it('asks the answers of a listing again when it is read, without forgetting the ones it has (#196)', () => {
    // load() follows the newest listing (lib/listingTickets); each read is one
    // loadListing. An approval granted meanwhile reaches the explorer as an
    // access.changed frame (a new era); without a live socket every read is
    // one. The rows' answers are asked once the listing is on screen, and the
    // menu shows the remembered ones meanwhile instead of none.
    expect(fn('load')).toMatch(/tickets\.follow\(\(\) => loadListing\(path\)\)/);
    const listing = fn('loadListing');
    expect(listing).not.toMatch(/forgetE2eAnswers\(\);/);
    expect(listing).toMatch(/if \(!realtime\.connected\.value\) menuAnswers\.stale\(\);/);
    expect(listing).toMatch(/prefetchMenuAnswers\(\);/);
    // The policy and the permission list still forget them: other rules,
    // other answers.
    expect(explorer).toMatch(/capabilitiesData\.value\?\.e2e_policy\?\.policy,\s*\],\s*forgetE2eAnswers,/);
  });

  it('says a refused encryption in the server’s words, not a bare "could not create"', () => {
    const create = fn('submitEncryptedFolder');
    expect(create).toMatch(/showToast\(\{ message: failureText\(err, t\('e2e\.create\.failed'\)\) \}, ERROR_TOAST_MS\);/);
    expect(create).not.toMatch(/flashToast\(t\('e2e\.create\.failed'\)\)/);
    expect(fn('submitConvertFolder')).toMatch(
      /showToast\(\{ message: failureText\(err, t\('e2e\.convert\.failed'\)\) \}, ERROR_TOAST_MS\);/,
    );
    expect(fn('e2eRunConversion')).toMatch(/e2eConvErr\.value = failureText\(err, t\('e2e\.convert\.failed'\)\);/);
  });

  // The four rows that START an encryption make something — a key file, or a
  // `.fxe` — and the server asks files.create for that beside files.encrypt
  // (backend perm.FilesEncrypt: "carved out of files.create, which the same
  // write needs as well"). After the catalogue upgrade a place can allow the
  // one and deny the other; the row is then hidden like every row the account
  // may not use (gateByPermissions), not offered to be refused.
  it('the four rows that start an encryption need files.create, like every other row that creates', () => {
    const literal = explorer.match(/const ACTION_PERMS: Record<string, string\[\]> = (\{[\s\S]*?\n\});/)?.[1];
    expect(literal, 'ACTION_PERMS was not found').toBeTruthy();
    const perms = new Function(`return (${literal});`)() as Record<string, string[]>;
    for (const key of ['fxe-encrypt', 'fxe-request', 'e2e-convert', 'e2e-request']) {
      expect(perms[key], `${key} needs`).toEqual(['files.create']);
    }
    // They are the keys the two menus emit — a row renamed there would leave
    // the table without a failure — …
    expect(FXE_ACTION_KEYS).toEqual(expect.arrayContaining(['fxe-encrypt', 'fxe-request']));
    expect(explorer).toMatch(/\{ key: 'e2e-convert', /);
    expect(explorer).toMatch(/\{ key: 'e2e-request', /);
    // … and the table gates every row of the one list the menu and the toolbar
    // render: what that list hands out, on both of its ways out, is the gated
    // one, never the list it was gated from.
    const listed = fn('selectionActionList');
    expect(listed).toMatch(/const list = gateByPermissions\(selectionActionListAll\(sel\), sel\);/);
    expect(listed).toMatch(/return list\.map\(\(a\) =>/);
    expect(listed).toMatch(/\n {2}return list;\n\}\n$/);
    expect(listed.match(/\breturn\b/g), 'a third way out').toHaveLength(2);
    expect(fn('gateByPermissions')).toMatch(/const needs = ACTION_PERMS\[a\.key\];/);
    // The rows the table already gated are still gated by what they were.
    expect(perms.rename).toEqual(['files.rename']);
    expect(perms.convert).toEqual(['files.create']);
    expect(perms.paste).toEqual(['files.create', 'files.move']);
  });

  // An encrypted folder is made in two steps: the plain folder, then the key
  // file into it. A refusal at the second step (the policy switched, an
  // approval spent or expired after the listing) leaves the plain folder
  // behind, and the listing must show what is there. It is not deleted: that
  // is not this dialog's to decide.
  it('a creation refused after the plain folder was made shows the folder that is there now', () => {
    const create = fn('submitEncryptedFolder');
    expect(create).toMatch(/let folderMade = false;/);
    expect(create).toMatch(/await api\.newFolder\(dirWire, payload\.name\);\s*folderMade = true;/);
    // The refusal is said first (a refresh takes a moment), then the listing is read again.
    expect(create).toMatch(
      /showToast\(\{ message: failureText\(err, t\('e2e\.create\.failed'\)\) \}, ERROR_TOAST_MS\);[\s\S]*?if \(folderMade\) await load\(\);/,
    );
    // Shown, not deleted.
    expect(create).not.toMatch(/api\.(delete|trash|remove|purge)\w*\(/);
  });

  // The dialog says the sentence and nothing else: a refusal's second line, for
  // administrators, is for the screens that have room for one.
  it('gives the request dialog no administrator flag it would not use', () => {
    const block = explorer.match(/<E2eRequestModal\b[\s\S]*?\/>/)?.[0] ?? '';
    expect(block, '<E2eRequestModal> was not found').not.toBe('');
    expect(block).not.toMatch(/caller-admin|callerAdmin/);
  });
});

describe('the client: the answer, and the request', () => {
  // A question that fails says so in the console (pinned on its own below);
  // the tests that fail one on purpose keep that line out of the output.
  beforeEach(() => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function serve(answer: (body: { items?: { path: string }[] }) => Response) {
    const calls: { url: string; body: Record<string, unknown> }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init: RequestInit = {}) => {
        const body = init.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {};
        calls.push({ url: String(url), body });
        return answer(body as { items?: { path: string }[] });
      }),
    );
    return calls;
  }
  const json = (data: unknown, status = 200) =>
    new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

  it('derives both endpoints from apiBase, like every other one', () => {
    const ep = resolveEndpoints({ apiBase: 'https://files.example.com' } as never);
    expect(ep.e2eAllowed).toBe('https://files.example.com/api/files/e2e/allowed');
    expect(ep.e2eRequests).toBe('https://files.example.com/api/files/e2e/requests');
    expect(resolveEndpoints({ apiBase: '', e2eAllowed: '/custom/allowed' } as never).e2eAllowed).toBe('/custom/allowed');
  });

  it('answers one word per path, in the order asked, 1000 paths per question', async () => {
    const calls = serve((b) =>
      json({
        encrypt: (b.items ?? []).map((it) =>
          it.path.endsWith('/b') ? 'request' : it.path.endsWith('/c') ? 'denied' : 'allowed',
        ),
      }),
    );
    const paths = Array.from({ length: 1001 }, (_, i) => `docs://${i}/a`);
    paths[1] = 'docs://x/b';
    paths[1000] = 'docs://x/c';
    const got = await useFileApi({ apiBase: '' } as never).e2eAllowedAt(paths);
    expect(calls.map((c) => c.url)).toEqual(['/api/files/e2e/allowed', '/api/files/e2e/allowed']);
    expect((calls[0].body.items as unknown[]).length).toBe(1000);
    expect((calls[1].body.items as unknown[]).length).toBe(1);
    expect(got.length).toBe(1001);
    expect([got[0], got[1], got[1000]]).toEqual(['allowed', 'request', 'denied']);
  });

  it('never throws: a failed question, a server that predates it or a word it does not know answer allowed', async () => {
    serve(() => json({ error: 'not found' }, 404));
    expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt(['docs://a', 'docs://b'])).toEqual(['allowed', 'allowed']);
    serve(() => json({ encrypt: ['maybe'] }));
    expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt(['docs://a', 'docs://b'])).toEqual(['allowed', 'allowed']);
  });

  // ⚠ Fail-open on purpose — the server decides at every door anyway — but not
  // silent: a question that keeps failing is how a menu goes on offering what
  // the server then refuses, and the console is where somebody looks.
  it('says in the console that the question failed, and still answers allowed', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    try {
      serve(() => json({ error: 'the database is on fire' }, 500));
      expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt(['docs://a'])).toEqual(['allowed']);
      expect(warn).toHaveBeenCalledTimes(1);
      const [said, cause] = warn.mock.calls[0];
      expect(said).toMatch(/^\[filex\] /);
      expect(cause).toBeInstanceOf(Error);

      // A question that is answered, or that asks about nothing, is silent.
      warn.mockClear();
      serve(() => json({ encrypt: ['request'] }));
      expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt(['docs://a'])).toEqual(['request']);
      expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt([])).toEqual([]);
      expect(warn).not.toHaveBeenCalled();
    } finally {
      warn.mockRestore();
    }
  });

  // The server's own refusals of the question (handlers/e2e_policy_files.go
  // Allowed): a body it cannot read, and more than 1000 paths.
  it('the question’s own 400s — a bad body, too many items — answer allowed too', async () => {
    serve(() => json({ error: 'bad json' }, 400));
    expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt(['docs://a'])).toEqual(['allowed']);
    serve(() => json({ error: 'too many items' }, 400));
    expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt(['docs://a'])).toEqual(['allowed']);
  });

  // Operator decision 2026-10-03: the kinds are separate, and an approval
  // opens only its own kind. A question without one is answered for the kind
  // the catalogue says, which is not what the New folder dialog asks about.
  it('names the kind it asks about, and a bare path stays a bare path', async () => {
    const calls = serve((b) => json({ encrypt: (b.items ?? []).map(() => 'allowed') }));
    await useFileApi({ apiBase: '' } as never).e2eAllowedAt([
      { path: 'docs://Muhasebe', kind: 'new_folder' },
      { path: 'docs://Muhasebe/Ekip', kind: 'folder' },
      { path: 'docs://Muhasebe/rapor.pdf', kind: 'file' },
      'docs://Arsiv',
    ]);
    expect(calls).toHaveLength(1);
    expect(calls[0].body.items).toEqual([
      { path: 'docs://Muhasebe', kind: 'new_folder' },
      { path: 'docs://Muhasebe/Ekip', kind: 'folder' },
      { path: 'docs://Muhasebe/rapor.pdf', kind: 'file' },
      { path: 'docs://Arsiv' },
    ]);
  });

  it('asks the answers of one path per kind apart: a new folder in it is not the folder itself', () => {
    expect(fn('e2eAskKey')).toMatch(/return `\$\{kind\}:\$\{path\}`;/);
    expect(fn('askE2eAnswers')).toMatch(/const k = e2eAskKey\(a\.path, a\.kind\);\s*next\[k\] = answers\[i\] \?\? 'allowed';/);
  });

  it('asks nothing for no paths', async () => {
    const calls = serve(() => json({ encrypt: [] }));
    expect(await useFileApi({ apiBase: '' } as never).e2eAllowedAt([])).toEqual([]);
    expect(calls).toEqual([]);
  });

  it('sends a request with the path, the kind and the reason', async () => {
    const calls = serve(() => json({ request: { id: 4, status: 'pending' }, created: true }, 201));
    const got = await useFileApi({ apiBase: '/proxy/filex' } as never).e2eRequest({
      path: 'docs://Muhasebe',
      kind: 'folder',
      reason: 'Bordro',
    });
    expect(calls).toEqual([
      { url: '/proxy/filex/api/files/e2e/requests', body: { path: 'docs://Muhasebe', kind: 'folder', reason: 'Bordro' } },
    ]);
    expect(got.created).toBe(true);
    expect(got.request.id).toBe(4);
  });
});
