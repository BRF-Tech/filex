// 87-search-fuzzy — filename search ORDER, not just "search answers".
//
// The reporter of issue #15 came back after v0.29.0 with a sentence worth
// pinning: "Fuzzy is distance matching. You shipped string matching.
// Code/main.go and example/main.go are the same thing to the search, and
// `Code main` returns nothing. […] with real fuzzy the word order shouldn't
// matter either." v0.30.x answers that by porting VS Code's Quick Open scorer
// (backend/internal/search/scorer.go) and re-ranking what Bleve retrieves.
//
// So the assertions here are FILTERING and ORDERING properties, not "the
// endpoint returns an array" — 85-search already covers the envelope:
//
//   - a query whose pieces are answered by DIFFERENT parts of the path keeps
//     only the candidate that answers all of them ("Code main" was 9 results)
//   - piece order carries no meaning ("main code" == "Code main"), and the
//     answer is that same single file either way
//   - the filename is scored separately from its directory, so `cypfuzz-main`
//     answers with exactly the two files whose NAME says so
//   - an all-digit word is matched literally, not fuzzily — one digit is one
//     edit, so `2026` used to return a 2025 file
//   - a query nothing answers returns nothing
//
// ⚠ One honest caveat, measured rather than assumed. Every case above except
// the RANK one goes red with `ScoreName` stubbed out (the pre-scorer state).
// The rank case does NOT: on a corpus this small Bleve's own merged score
// already puts `cypfuzzreport.txt` ahead of `cypfuzzreport-final.txt`, and the
// orderings were byte-identical with and without the scorer. It is kept
// because ordering is a real contract and a real regression surface, but it
// guards the pipeline, not this component — see the comment on that case.
//
// ⚠ These are cross-storage queries against the Bleve index, so the fixtures
// have to be indexed before anything is asserted. `save-text` writes the file
// and the ops pipeline indexes it; the `before` hook below waits for the index
// to actually hold them rather than sleeping.

const STORAGE = () => (Cypress.env('SEEDED_STORAGE') as string) || '';

/** Fixtures this spec owns. The names are the test: two identically-named
 *  files in differently-named folders (the reporter's own example), a pair
 *  that differs only by a suffix, and a pair that differs only by a year. */
const FIXTURES = [
  'CypressCode/cypfuzz-main.go',
  'cypressexample/cypfuzz-main.go',
  'cypfuzzreport.txt',
  'cypfuzzreport-final.txt',
  'cypfuzz annual report 2025.txt',
  'cypfuzz annual report 2026.txt',
];

type Hit = { name: string; path?: string; matched?: string };

/** GET /api/files/search with a name-scoped query, returned in rank order.
 *  ⚠ `scope=name`: without it a content match can answer, and then the case
 *  would be measuring the extracted-text index instead of the filename scorer
 *  — which is exactly the mistake that produced a wrong public reply about
 *  this very issue (the hit was `package main` inside the file, not the name). */
function nameSearch(q: string) {
  const tok = window.sessionStorage.getItem('filex.bearer');
  return cy
    .request({
      url: `/api/files/search?scope=name&limit=50&q=${encodeURIComponent(q)}`,
      headers: tok ? { Authorization: `Bearer ${tok}` } : {},
    })
    .then((res) => {
      const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
      const hits = (body.results ?? []) as Hit[];
      // Only this spec's fixtures. The instance carries whatever earlier specs
      // left behind, and an ordering assertion has to be about a known set.
      return hits.filter((h) => (h.name || '').startsWith('cypfuzz'));
    });
}

describe('fuzzy filename search', () => {
  before(function () {
    if (!STORAGE()) this.skip();
    cy.apiLogin().then((tok) => {
      const h = { Authorization: `Bearer ${tok}` };
      for (const f of FIXTURES) {
        cy.request({
          method: 'POST',
          url: '/api/files/save-text',
          headers: h,
          body: { path: `${STORAGE()}://${f}`, content: 'package main' },
        });
      }
      // ⚠ Wait for the INDEX, not for a timer. Nothing here is asserting
      // indexing latency, and a fixed sleep is how a suite becomes flaky on a
      // slower machine. `cypfuzzreport` is the least ambiguous of the fixtures.
      cy.request({
        method: 'POST',
        url: '/api/admin/search/rebuild',
        headers: h,
        failOnStatusCode: false,
      });
    });
    // Poll until the fixtures are retrievable at all.
    const waitForIndex = (attempt = 0) => {
      cy.apiLogin();
      nameSearch('cypfuzzreport').then((hits) => {
        if (hits.length >= 2) return;
        if (attempt > 40) {
          throw new Error(
            `the search index never picked up the fixtures (last answer: ${hits.length} hits)`,
          );
        }
        cy.wait(250, { log: false });
        waitForIndex(attempt + 1);
      });
    };
    waitForIndex();
  });

  beforeEach(function () {
    if (!STORAGE()) this.skip();
    cy.apiLogin();
  });

  it('keeps only the candidate that answers EVERY word — "CypressCode cypfuzz-main"', () => {
    // The reporter's case. `CypressCode` is answered by a folder and
    // `cypfuzz-main` by a filename; the file under cypressexample/ answers the
    // second and nothing answers the first, so it is dropped rather than
    // ranked. Before the scorer both files came back and the query was
    // meaningless.
    nameSearch('CypressCode cypfuzz-main').then((hits) => {
      const paths = hits.map((h) => h.path);
      expect(paths, 'the folder-qualified file is found').to.include(
        '/CypressCode/cypfuzz-main.go',
      );
      expect(paths, 'the same-named file in another folder is NOT a hit').to.not.include(
        '/cypressexample/cypfuzz-main.go',
      );
    });
  });

  it('word order carries no meaning', () => {
    // "with real fuzzy the word order shouldn't matter either" — the pieces are
    // matched independently, so these two queries are the same question.
    nameSearch('CypressCode cypfuzz-main').then((a) => {
      nameSearch('cypfuzz-main CypressCode').then((b) => {
        // ⚠ Both halves. Comparing the two answers ALONE passes against a
        // build that does not score at all — measured: with the scorer stubbed
        // out both queries returned the same unfiltered four rows and this case
        // stayed green. Pinning the answer itself is what makes it a guard.
        expect(
          a.map((h) => h.path),
          'the answer is the one file that answers both words',
        ).to.deep.eq(['/CypressCode/cypfuzz-main.go']);
        expect(
          b.map((h) => h.path),
          'reversing the words gives the same answer',
        ).to.deep.eq(a.map((h) => h.path));
      });
    });
  });

  it('an exact filename hit ranks above a longer name that merely contains it', () => {
    // ⚠ Rank, not membership, is the assertion — and be honest about what it
    // guards. Stubbing `ScoreName` out (the pre-scorer state) does NOT flip
    // this pair: measured on this corpus, Bleve's own merged score already put
    // `cypfuzzreport.txt` first, and every ordering in this file was identical
    // with and without the scorer. So this case guards the PIPELINE's ordering
    // rather than the scorer specifically — it goes red if anything, Bleve
    // included, stops putting an exact name first. It was red-proved by
    // reversing the final result order, not by removing the scorer.
    nameSearch('cypfuzzreport').then((hits) => {
      const names = hits.map((h) => h.name);
      const exact = names.indexOf('cypfuzzreport.txt');
      const longer = names.indexOf('cypfuzzreport-final.txt');
      expect(exact, 'the exact name is a hit').to.be.greaterThan(-1);
      expect(longer, 'the longer name is also a hit').to.be.greaterThan(-1);
      expect(exact, `exact before longer (got ${names.join(', ')})`).to.be.lessThan(longer);
    });
  });

  it('the filename scores separately from its directory', () => {
    // A query answered entirely by the FILENAME must outrank one answered by a
    // folder above it — that separation is the whole reason the scorer exists,
    // and it is what makes `Code/main.go` beat `example/main.go` rather than
    // tie with it.
    nameSearch('cypfuzz-main').then((hits) => {
      const paths = hits.map((h) => h.path).sort();
      // ⚠ The EXACT set, not `include.members`. A membership check passes
      // against a build that returns everything — measured: with the scorer
      // stubbed out this query answered with four rows, the two annual-report
      // fixtures among them, because `cypfuzz` alone was enough to be a
      // candidate and nothing dropped the ones that never answered `main`.
      expect(paths, 'exactly the two files whose NAME answers the query').to.deep.eq([
        '/CypressCode/cypfuzz-main.go',
        '/cypressexample/cypfuzz-main.go',
      ]);
    });
  });

  it('an all-digit word is matched literally, not fuzzily', () => {
    // ⚠ The typo pass used to apply to every word, and one digit is one edit —
    // so `2026` returned the 2025 file too, ranked second. Mixed words stay
    // fuzzy; a standalone number does not.
    nameSearch('cypfuzz 2026').then((hits) => {
      const names = hits.map((h) => h.name);
      expect(names, 'the 2026 file is found').to.include('cypfuzz annual report 2026.txt');
      expect(names, 'the 2025 file is NOT a hit for 2026').to.not.include(
        'cypfuzz annual report 2025.txt',
      );
    });
  });

  it('a query nothing answers returns nothing, rather than everything', () => {
    // The scorer is also the FILTER. A regression that scored but never
    // dropped would leave every case above passing on membership and turn
    // search into "sorted by relevance, containing everything".
    nameSearch('cypfuzz zzzzqqqq').then((hits) => {
      expect(hits.map((h) => h.name), 'no candidate answers both words').to.deep.eq([]);
    });
  });
});
