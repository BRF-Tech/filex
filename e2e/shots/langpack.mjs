// Apps, listed — and a LANGUAGE PACK among them.
//
//   node e2e/shots/langpack.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/langpack/:
//
//   apps-list-1440.png   Plugins → Apps: the two apps filex ships alongside
//                        itself and the THREE language packs that ship with
//                        it, each row saying what it is and how much of THIS
//                        filex it translates
//
// ⚠⚠ Three packs, and which three is not a choice this file gets to make.
// README: "Spanish, German and French ship as examples." There is a fourth
// pack on the maintainer's machine, `G:/filex-lang-ar`, and it is filex's
// right-to-left TEST FIXTURE — not published, not advertised (Burak,
// 2026-09-19). This picture is in README.md and in docs/APP-PLUGINS.md, so a
// row for a language nobody can install would be the vitrine advertising
// something that does not exist. `findApp` cannot even see it
// (e2e/helpers/app-locations.mjs).
//
// ⚠⚠ A language pack is a manifest and nothing that runs (docs/APP-PLUGINS.md →
// Language packs), so it is the one "app" with no `plugin.wasm` to find: its
// entry in e2e/helpers/app-locations.mjs is `dataOnly`, and the install below
// sends the manifest alone. A lookup that insisted on a module could never find
// one, and the scene would report "not built" for a thing that has no build.
//
// ⚠ The coverage figure is READ OFF THE SCREEN before the shutter, not assumed.
// A pack whose catalogue has drifted behind the product reports a lower number
// — which is honest and fine — but one reporting NOTHING means the server found
// no keys to compare and the picture would be selling an empty promise.
//
// ⚠⚠ …and at THIS release every shipped pack is complete, so the picture has
// to show that: `100% translated`, with nothing after it. The trailing clauses
// are conditional (AppPluginLanguages.vue) — "· the rest shows in English"
// below 100%, "· N strings this version of filex does not have" when the pack
// carries keys this binary has retired — and a pack that has drifted since the
// last sync brings them back. The run REFUSES rather than quietly photograph
// a stale pack: re-sync it from filex-lang-template and shoot again.
//
// Environment: FILEX_BIN, FILEX_SIGN_APP_DIR, FILEX_CONVERT_APP_DIR,
// FILEX_LANG_ES_APP_DIR, FILEX_LANG_DE_APP_DIR, FILEX_LANG_FR_APP_DIR,
// SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { readFileSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, findApp, installApp, log, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'langpack';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

/** Install a data-only app: the manifest, and no module at all. */
async function installPack(admin, pack) {
  const fd = new FormData();
  fd.append('manifest', new Blob([readFileSync(pack.manifestPath)]), 'filex-app.json');
  const res = await admin.call('/api/admin/app-plugins', { method: 'POST', body: fd });
  if (!res.ok) throw new Error(`install ${pack.name}: ${res.status} ${(await res.text()).slice(0, 400)}`);
  return res.json();
}

async function main() {
  const sign = findApp('sign');
  const convert = findApp('convert');
  /* The packs that ship, in the order the list draws them. */
  const packs = [findApp('lang-es'), findApp('lang-de'), findApp('lang-fr')];
  const pack = packs[0];
  log(
    `sign ${sign.manifest.version}, convert ${convert.manifest.version}, ` +
      packs.map((p) => `${p.manifest.name} ${p.manifest.version}`).join(', '),
  );

  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });

    await installApp(admin, sign);
    await installApp(admin, convert);
    for (const p of packs) await installPack(admin, p);

    // What the server itself says about each pack — the same numbers the rows
    // below draw, so a bad one is caught before a browser is opened.
    const listed = await admin.json('/api/admin/app-plugins');
    for (const p of packs) {
      const name = p.manifest.name;
      const row = (listed.plugins ?? []).find((x) => x.name === name);
      if (!row) throw new Error(`${name} is not in /api/admin/app-plugins after installing it`);
      if (row.kind !== 'language_pack') throw new Error(`${name} installed as "${row.kind}", not a language pack`);
      const covered = (row.languages ?? []).filter((l) => (l.percent ?? 0) > 0);
      if (!covered.length) {
        throw new Error(
          `${name} reports no coverage at all (${JSON.stringify(row.languages)}) — the picture would ` +
            'show a pack that translates nothing',
        );
      }
      // ⚠⚠ Complete, and nothing trailing. See the note at the top of this
      // file: at this release every shipped pack is in step with the
      // catalogue, and the row's conditional clauses are the visible sign
      // that one has drifted.
      for (const l of row.languages ?? []) {
        if ((l.percent ?? 0) !== 100 || (l.unknown ?? 0) > 0) {
          throw new Error(
            `${name} (${l.code}) is ${l.percent}% with ${l.unknown ?? 0} strings this filex does not have — ` +
              'this picture says every shipped pack is complete, so re-sync the pack against this tree ' +
              'before shooting it',
          );
        }
      }
      log(`${name}: ${covered.map((l) => `${l.code} ${l.percent}%`).join(', ')}`);
    }

    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await admin.post('/api/notifications/read-all', {});

    await page.goto(`${inst.url}/admin/plugins`);
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-plugins').waitFor({ timeout: 20_000 });
    for (const name of [sign.manifest.name, convert.manifest.name, ...packs.map((p) => p.manifest.name)]) {
      await page.getByTestId(`app-plugin-${name}`).waitFor({ timeout: 20_000 });
    }
    for (const p of packs) {
      await page.getByTestId(`app-plugin-kind-${p.manifest.name}`).waitFor({ timeout: 15_000 });
    }

    // ⚠ The row must SAY the number, not merely have it in a payload: this
    // picture is the one place a reader learns that a pack states how far it
    // goes and that the rest stays English.
    const text = (await page.getByTestId('app-plugins').innerText()).replace(/\s+/g, ' ');
    if (!/\d+%/.test(text)) {
      throw new Error(`the Apps list shows no coverage percentage for ${pack.manifest.name}: "${text.slice(0, 400)}"`);
    }
    /* ⚠ And what it says is what the server said: every pack complete, with
       no trailing clause after the percentage. Checked on the DRAWN text, not
       only in the payload — the clauses are the component's own `v-if`s
       (AppPluginLanguages.vue) and a wrong one here is a wrong PICTURE. */
    for (const p of packs) {
      for (const code of Object.keys(p.manifest.ui_locales ?? {})) {
        const line = page.getByTestId(`app-plugin-language-${code}`);
        const said = ((await line.innerText()) ?? '').replace(/\s+/g, ' ').trim();
        if (!said.includes('100%')) {
          throw new Error(`${p.manifest.name} (${code}) does not read 100% on screen: "${said}"`);
        }
        if (/100%[^·]*·/.test(said)) {
          throw new Error(
            `${p.manifest.name} (${code}) has something after its percentage: "${said}" — this picture says ` +
              'every shipped pack is complete',
          );
        }
      }
    }
    // ⚠⚠ MEASURED, in the browser, at THREE widths — not only the one the
    // picture is taken at. The Label cell of a language-pack row is the widest
    // thing this table draws (the pack's name AND its badge on one line, the
    // coverage under them), so it is the cell that decides the column's width,
    // and it is the first to break when the window is narrow. Two ways it has
    // already gone wrong: the three pieces were siblings of a flex cell and
    // were drawn over each other (v0.43.0, first take), and the column was too
    // narrow so the name came out as "Spanish la…" (v0.43.0, after srcfix's
    // no-wrap fix). Both are invisible to a jsdom test, which has no layout.
    /* ⚠ EVERY pack row, not just the first. "Deutsches Sprachpaket" is wider
       than "Spanish language pack", so the row that decides the column — and
       the row that breaks first — is not necessarily the one at the top. */
    const seenAt = [];
    for (const width of [958, 1280, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await sleep(500);
      for (const measured of packs) {
      const seen = await page.evaluate((name) => {
        const badge = document.querySelector(`[data-testid="app-plugin-kind-${name}"]`);
        const cell = badge?.closest('.fe-list__cell');
        const label = document.querySelector(`[data-testid="app-plugin-label-${name}"]`);
        if (!cell || !label) return { fault: 'the language pack row has no Label cell' };

        // ⚠⚠ Per LINE, not per element. An inline element that wraps reports
        // ONE bounding rect spanning every line it touches, which starts where
        // the container's left edge is — so the coverage line's union box
        // swallows the name above it and a union-box test calls a perfectly
        // readable row an overlap (measured 2026-09-23). `getClientRects()`
        // gives the boxes actually painted, one per line, which is what a
        // reader sees.
        const lines = [];
        for (const el of cell.querySelectorAll('*')) {
          if (el.children.length || !(el.textContent ?? '').trim()) continue;
          for (const r of el.getClientRects()) {
            if (r.width > 0 && r.height > 0) lines.push({ text: (el.textContent ?? '').trim().slice(0, 30), r });
          }
        }
        let overlap = '';
        for (let i = 0; i < lines.length && !overlap; i++) {
          for (let j = i + 1; j < lines.length && !overlap; j++) {
            const a = lines[i].r;
            const b = lines[j].r;
            const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
            const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
            if (w > 2 && h > 2) {
              const at = (x) => `${Math.round(x.left)},${Math.round(x.top)} ${Math.round(x.width)}x${Math.round(x.height)}`;
              overlap =
                `"${lines[i].text}" (${at(a)}) and "${lines[j].text}" (${at(b)}) are drawn on top of each other ` +
                `by ${Math.round(w)}x${Math.round(h)}px`;
            }
          }
        }

        // The name is CUT when the text is wider than the box it is painted in
        // — `truncate` keeps it from overflowing, and the ellipsis is what a
        // reader sees instead of the rest of the name.
        const cut = label.scrollWidth > label.clientWidth + 1;

        // Does the table still fit the box it was given? A narrow window may
        // legitimately scroll it sideways; what must not happen is a column
        // squeezed to nothing.
        // ⚠ Scoped to THIS table. `document.querySelectorAll` here counted the
        // columns of every table on the page (17 of them, one 0px wide) and
        // reported a squeeze that belonged to somebody else's markup.
        const table = cell.closest('.fe-list') ?? cell.closest('[class*="tbl"]') ?? cell.parentElement;
        const heads = table ? [...table.querySelectorAll('.fe-list__head .fe-list__col')] : [];
        return {
          fault: '',
          overlap,
          cut,
          label: `${Math.round(label.scrollWidth)}px of text in ${Math.round(label.clientWidth)}px`,
          cellWidth: Math.round(cell.getBoundingClientRect().width),
          overflow: table ? Math.max(0, table.scrollWidth - table.clientWidth) : -1,
          narrowest: heads.length ? Math.min(...heads.map((h) => Math.round(h.getBoundingClientRect().width))) : -1,
          columns: heads.length,
        };
      }, measured.manifest.name);

      if (seen.fault) throw new Error(`${measured.manifest.name}: ${seen.fault}`);
      log(
        `${width}px ${measured.manifest.name}: label ${seen.label}, cell ${seen.cellWidth}px, ` +
          `${seen.columns} columns (narrowest ${seen.narrowest}px), table overflows by ${seen.overflow}px`,
      );
      seenAt.push({ width, pack: measured.manifest.name, ...seen });
      if (seen.overlap) {
        throw new Error(`the ${measured.manifest.name} row is unreadable at ${width}: ${seen.overlap}`);
      }
      // ⚠⚠ The name must be whole at the width the PICTURE is taken at, and
      // that is the only width where it can be promised. Below about 1100 the
      // table has more declared column width than box, and DataTable shrinks
      // every column to fit rather than scrolling sideways — so at 958 the
      // Label cell is ~122px whatever the column declares, and `truncate` is
      // doing exactly the job it is there for. Measured, printed, and NOT
      // failed: no width in AppPluginsTab.vue can change it, and a check that
      // demanded it would only tempt the next person to shave a column that
      // fits nowhere.
      if (seen.narrowest === 0) throw new Error(`a column of this table is squeezed to nothing at ${width}`);
      }
    }
    // ⚠⚠ The pack's name must be WHOLE where the product has room for it.
    // Measured 2026-09-23, `label` column 240 and the badge allowed to wrap:
    //
    //     958px   cell 122px   the name needs 135px, gets 122px  (cut)
    //     1280px  cell 139px   the name needs 135px, gets 135px
    //     1440px  cell 192px   the name needs 135px, gets 135px
    //
    // Two things had to be true together. Widening the column alone did not
    // do it — this table declares about 1090px of columns plus a pinned
    // Actions column into a content box of roughly 1100px at 1440, so
    // DataTable shrinks every column proportionally and a declared 240 arrives
    // as 192. What finished it is `flex-wrap` on the row inside the cell: a
    // flex container breaks its lines before it shrinks anything, so the badge
    // drops to its own line and the name is measured against the whole column
    // instead of the column minus a badge.
    //
    // 958px keeps a cut name and that is the honest answer, not a gap in the
    // check: the column is 122px there whatever it declares, and no width in
    // AppPluginsTab.vue can change it. `truncate` is doing the job it is for.
    for (const v of seenAt) {
      if (!v.cut) continue;
      if (v.width >= 1280) {
        throw new Error(
          `${v.pack}'s name is cut at ${v.width} (${v.label}) — the Apps list's language-pack rows are the ` +
            'rows this picture exists to explain, so they have to show their names. Check `flex-wrap` on the ' +
            'row inside the Label cell and the column width, both in ' +
            'web/src/components/plugins/AppPluginsTab.vue.',
        );
      }
      log(`${v.pack}'s name is cut at ${v.width}px (${v.label}) — expected below 1280, see the note in this file`);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await sleep(500);

    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'apps-list-1440.png');
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
