package officeengine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The engine end to end with the Office editor's REAL modules: this test
// binary is the engine process (TestMain, childEnv), x2t-server.wasm and the
// layout run under wazero, the documents are real. It needs the app's
// release files, so it runs only when told where they are, as the Document
// Server's integration test does (internal/server office_engine_ds_test.go):
//
//	FILEX_TEST_OFFICE_ENGINE_DIR    a directory with engine.zip and ui.zip (the
//	                                app's release), letter.docx and report.xlsx;
//	                                protected.docx (a password-protected docx)
//	                                when there is one
//	FILEX_TEST_OFFICE_ENGINE_CACHE  a compilation cache kept between runs
//	                                (optional: the first compile takes minutes)
//
// What it measures is the app's own SERVER.md: letter.docx to PDF (3 pages)
// and to odt, report.xlsx to csv with the delimiter asked for, the
// thumbnails at the Document Server's sizes, a password-protected docx as a
// password failure, a deadline that ends the process and the next job on a
// new one. letter.docx draws 247 x 320; report.xlsx 320 x 320, because a
// sheet's thumbnail is laid out on the square 120 mm page filex asks for
// (onlyoffice.ThumbLayoutParams): the Document Server's own x2t and
// doctrenderer draw that json 320 x 320 as well, and 226 x 320 (the A4 page
// of SERVER.md) only without it (measured 2026-10-16 on the DS image).
//
// After every job it reads each engine process's resident memory: two
// rounds of thumbnails after the conversions, all in the same processes,
// must stay under the per-instance cap - the process does not climb towards
// its limit job by job (memory.go).
func TestEngine_TheRealModules(t *testing.T) {
	r, dir := realEngine(t, "letter.docx, report.xlsx")

	held := func(after string) {
		r.mu.Lock()
		type one struct {
			purpose Purpose
			pid     int
		}
		var ps []one
		for purpose, pl := range r.pools {
			for _, p := range pl.idle {
				ps = append(ps, one{purpose, p.pid})
			}
		}
		r.mu.Unlock()
		sort.Slice(ps, func(i, j int) bool { return ps[i].purpose < ps[j].purpose })
		var line []string
		for _, p := range ps {
			rss, ok := processRSS(p.pid)
			if !ok {
				continue
			}
			peak, _ := processPeakRSS(p.pid)
			line = append(line, fmt.Sprintf("%s pid %d rss %d MiB (peak %d MiB)", p.purpose, p.pid, rss>>20, peak>>20))
			assert.Less(t, rss, int64(MaxMBDefault)<<20, "after %s: the %s process holds less than one instance's cap", after, p.purpose)
		}
		if len(line) > 0 {
			t.Logf("MEASURE memory after %s: %s", after, strings.Join(line, "; "))
		}
	}

	out := t.TempDir()
	jobs := 0
	convert := func(name, to string, delimiter int) ([]byte, error) {
		ext := strings.TrimPrefix(filepath.Ext(name), ".")
		// Every conversion its own result: the engine does not write over a
		// file that is there.
		jobs++
		dst := filepath.Join(out, fmt.Sprintf("%02d-%s-%s.%s", jobs, strings.TrimSuffix(name, "."+ext), to, to))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		started := time.Now()
		err := r.Convert(ctx, Conversion{Src: filepath.Join(dir, name), From: ext, To: to, Delimiter: delimiter, Dst: dst, MaxBytes: 64 << 20})
		t.Logf("MEASURE %s -> %s: %s (%v)", name, to, time.Since(started), err)
		held(name + " -> " + to)
		if err != nil {
			return nil, err
		}
		return os.ReadFile(dst)
	}

	pdf, err := convert("letter.docx", "pdf", 0)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")), "a PDF")
	assert.Len(t, pageRe.FindAll(pdf, -1), 3, "letter.docx is three pages (the Document Server's)")

	odt, err := convert("letter.docx", "odt", 0)
	require.NoError(t, err)
	assert.Contains(t, string(odt[:min(200, len(odt))]), "application/vnd.oasis.opendocument.text")

	csv, err := convert("report.xlsx", "csv", 2)
	require.NoError(t, err)
	first := strings.SplitN(strings.TrimPrefix(string(csv), string([]byte{0xef, 0xbb, 0xbf})), "\n", 2)[0]
	assert.Contains(t, first, ";", "the delimiter asked for")

	// Two rounds, in one thumbnail process: the second round's memory is
	// what the first left.
	thumbs := []struct {
		name string
		w, h int
	}{{"letter.docx", 247, 320}, {"report.xlsx", 320, 320}, {"letter.docx", 247, 320}, {"report.xlsx", 320, 320}}
	for i, th := range thumbs {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		var params json.RawMessage
		if strings.HasSuffix(th.name, ".xlsx") {
			// What filex sends (onlyoffice.ThumbLayoutParams("xlsx")).
			params = json.RawMessage(`{"spreadsheetLayout":{"ignorePrintArea":true,"fitToWidth":1,"fitToHeight":0,"gridLines":true,"headings":false,"pageSize":{"width":"120mm","height":"120mm"},"margins":{"left":"2mm","right":"2mm","top":"2mm","bottom":"2mm"}}}`)
		}
		started := time.Now()
		b, err := r.DrawThumbnail(ctx, Thumbnail{Src: filepath.Join(dir, th.name), From: strings.TrimPrefix(filepath.Ext(th.name), "."), Size: 320, Params: params, MaxBytes: 4 << 20})
		cancel()
		t.Logf("MEASURE thumbnail %s (%d): %s", th.name, i+1, time.Since(started))
		held(fmt.Sprintf("thumbnail %s (%d)", th.name, i+1))
		require.NoError(t, err, th.name)
		img, err := png.Decode(bytes.NewReader(b))
		require.NoError(t, err, th.name)
		assert.Equal(t, th.w, img.Bounds().Dx(), "%s: the Document Server's width", th.name)
		assert.Equal(t, th.h, img.Bounds().Dy(), "%s: the Document Server's height", th.name)
	}

	if _, err := os.Stat(filepath.Join(dir, "protected.docx")); err == nil {
		_, err := convert("protected.docx", "pdf", 0)
		e, ok := AsError(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, CodePassword, e.Code)
	}

	// A deadline the conversion cannot meet ends the process; the next job
	// runs on a new one.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err = r.Convert(ctx, Conversion{Src: filepath.Join(dir, "letter.docx"), From: "docx", To: "pdf", Dst: filepath.Join(out, "late.pdf")})
	cancel()
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	again, err := convert("letter.docx", "odt", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, again)
}

// realEngine is a Runner on the Office editor's real release, ready: this
// test binary is the engine process (TestMain, childEnv). It skips the test
// when FILEX_TEST_OFFICE_ENGINE_DIR is not set; `needs` says which of that
// directory's documents the test reads besides engine.zip and ui.zip.
func realEngine(t *testing.T, needs string) (*Runner, string) {
	t.Helper()
	dir := os.Getenv("FILEX_TEST_OFFICE_ENGINE_DIR")
	if dir == "" {
		t.Skip("needs the office editor's release: FILEX_TEST_OFFICE_ENGINE_DIR (engine.zip, ui.zip, " + needs + ")")
	}
	cache := os.Getenv("FILEX_TEST_OFFICE_ENGINE_CACHE")
	if cache == "" {
		cache = t.TempDir()
	}
	sum := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	exe, err := os.Executable()
	require.NoError(t, err)
	r := New(Options{
		Command: []string{exe}, Env: []string{childEnv + "=1"},
		CacheDir: cache, WorkDir: t.TempDir(),
		PrepareTimeout: 30 * time.Minute, StartTimeout: 10 * time.Minute,
	})
	t.Cleanup(r.Close)
	r.SetPackage(&Package{
		App: "office-editor", Version: "test",
		Engine: filepath.Join(dir, "engine.zip"), EngineSHA256: sum("engine.zip"),
		UI: filepath.Join(dir, "ui.zip"), UISHA256: sum("ui.zip"),
		EditorDir: "editor", HostVersion: "test",
	})
	t0 := time.Now()
	waitState(t, r, StateReady, 30*time.Minute)
	t.Logf("MEASURE prepared in %s", time.Since(t0))
	return r, dir
}

// pageRe finds a PDF's page objects (not its /Pages tree).
var pageRe = regexp.MustCompile(`/Type\s*/Page[^s]`)
