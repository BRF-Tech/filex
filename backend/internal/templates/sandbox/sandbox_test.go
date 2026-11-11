package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type host struct{}

func (host) Call(fn string, args []any) (string, error) {
	switch fn {
	case "t":
		vars, _ := args[1].(map[string]string)
		if args[0] == "mail.greeting" {
			return "Hello, " + vars["name"], nil
		}
		return args[0].(string), nil
	case "tpl":
		return "{name} shared {link}", nil
	case "bytes":
		return fmt.Sprintf("%v B", args[0]), nil
	}
	return "", fmt.Errorf("no %s", fn)
}

func render(t *testing.T, limit time.Duration, req Request) (string, error, time.Duration) {
	t.Helper()
	if err := Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if req.Host == nil {
		req.Host = host{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	out, err := Render(ctx, req)
	return out, err, time.Since(start)
}

// The source the module was built from is the source in this tree.
func TestEngineIsBuiltFromTheseSources(t *testing.T) {
	want, err := os.ReadFile(SourcesFile)
	if err != nil {
		t.Fatalf("%v - run sh backend/internal/templates/sandbox/build.sh", err)
	}
	got, err := SourceDigest("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Fatalf("engine.wasm.gz was built from other sources (%s, these are %s): the dialect, the guest, build.sh "+
			"or the Liquid library changed - run sh backend/internal/templates/sandbox/build.sh and commit both files",
			strings.TrimSpace(string(want)), got)
	}
}

// The public tree is this source under another module path (the export
// renames every import): the digest must say the same of both, and still see
// any other change. Red before 0.60.0's fix - the GitHub gate failed
// TestEngineIsBuiltFromTheseSources on the public tree alone.
func TestDigestIsTheSameUnderAnotherModulePath(t *testing.T) {
	here, err := SourceDigest("../../..")
	if err != nil {
		t.Fatal(err)
	}
	gomod, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	own := strings.Fields(strings.SplitN(string(gomod), "\n", 2)[0])[1]
	const public = "github.com/example/renamed/backend"
	copyTree := func(edit func(rel string, b []byte) []byte) string {
		dst := t.TempDir()
		for _, rel := range []string{"go.mod", "go.sum", "internal/templates/sandbox/build.sh"} {
			b, err := os.ReadFile(filepath.Join("../../..", rel))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dst, rel), edit(rel, b))
		}
		for _, dir := range []string{"internal/templates/engine", "internal/templates/sandbox/guest"} {
			matches, _ := filepath.Glob(filepath.Join("../../..", dir, "*.go"))
			if len(matches) == 0 {
				t.Fatalf("no Go file in %s", dir)
			}
			for _, m := range matches {
				b, err := os.ReadFile(m)
				if err != nil {
					t.Fatal(err)
				}
				rel := filepath.ToSlash(filepath.Join(dir, filepath.Base(m)))
				writeFile(t, filepath.Join(dst, rel), edit(rel, b))
			}
		}
		return dst
	}
	renamed := copyTree(func(_ string, b []byte) []byte { return []byte(strings.ReplaceAll(string(b), own, public)) })
	got, err := SourceDigest(renamed)
	if err != nil {
		t.Fatal(err)
	}
	if got != here {
		t.Fatalf("the same sources under %s digest as %s, here %s", public, got, here)
	}
	changed := copyTree(func(rel string, b []byte) []byte {
		if strings.HasPrefix(rel, "internal/templates/sandbox/guest/") {
			return append(b, []byte("\n// a change\n")...)
		}
		return b
	})
	if got, _ := SourceDigest(changed); got == here {
		t.Fatal("a change to the guest left the digest as it was")
	}
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRendersWithTheHost(t *testing.T) {
	out, err, _ := render(t, 5*time.Second, Request{
		Name: "mail.share", Source: `{{ 'mail.greeting' | t: name: who }} {{ size | bytes }} {{ x }}`,
		Data: map[string]any{"who": "Ada", "size": 2048, "x": "<b>"},
	})
	if err != nil || out != "Hello, Ada 2048 B &lt;b&gt;" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestLayoutFramesTheContent(t *testing.T) {
	out, err, _ := render(t, 5*time.Second, Request{
		Name: "page.pin", Source: `<p>{{ x }}</p>`,
		Layout: `<main>{{ content }}</main>{{ theme.css }}`, LayoutAs: "page.layout",
		Data: map[string]any{"x": "<i>", "theme": map[string]any{"css": "<style>a{}</style>"}},
		Safe: []string{"theme.css"},
	})
	if err != nil || out != "<main><p>&lt;i&gt;</p></main><style>a{}</style>" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestTemplateErrorsAreTheTemplates(t *testing.T) {
	_, err, _ := render(t, 5*time.Second, Request{Name: "mail.share", Source: "a\n{% if %}"})
	var te *TemplateError
	if !errors.As(err, &te) || !strings.Contains(te.Msg, "line 2") {
		t.Fatalf("%v", err)
	}
	_, err, _ = render(t, 5*time.Second, Request{Name: "x", Source: `{% include 'go.mod' %}`})
	if !errors.As(err, &te) {
		t.Fatalf("include of a file: %v", err)
	}
}

// ⚠⚠ The measurements this package exists for (docs/TEMPLATES.md, Security):
// each of these ran without end, or asked for gigabytes, in the server.
func TestHostileTemplatesEndAtACeiling(t *testing.T) {
	cases := map[string]string{
		"empty loop":       `{% for i in (1..1000000000) %}{% endfor %}`,
		"nested loops":     `{% for a in (1..100000) %}{% for b in (1..100000) %}{% assign z = b %}{% endfor %}{% endfor %}`,
		"string doubling":  `{% assign s = "xxxxxxxxxx" %}{% for i in (1..40) %}{% assign s = s | append: s %}{% endfor %}{{ s | size }}`,
		"array doubling":   `{% assign a = "a,a,a,a" | split: "," %}{% for i in (1..40) %}{% assign a = a | concat: a %}{% endfor %}{{ a | size }}`,
		"writing loop":     `{% for i in (1..100000000) %}xxxxxxxxxxxxxxxx{% endfor %}`,
		"self include":     `{% include 'loop' %}`,
		"deep nesting":     strings.Repeat("{% if true %}", 60000) + "x" + strings.Repeat("{% endif %}", 60000),
		"huge range break": `{% for i in (1..1000000000) %}{% break %}{% endfor %}{% for i in (1..1000000000) %}{% endfor %}`,
		// osteele/liquid #149: slice with a negative length panics the
		// library. Here it ends the instance, not the server.
		"slice negative": `{{ "abcdef" | slice: 2, -3 }}`,
	}
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if len(src) > MaxSource {
				if _, err, _ := render(t, time.Second, Request{Name: name, Source: src}); !errors.Is(err, ErrTooLarge) {
					t.Fatalf("an oversized template: %v", err)
				}
				src = src[:MaxSource]
			}
			out, err, took := render(t, 300*time.Millisecond, Request{
				Name: name, Source: src, Partials: map[string]string{"loop": `{% include 'loop' %}`},
			})
			if err == nil && name != "slice negative" {
				t.Fatalf("rendered (%d bytes): it must end at a ceiling", len(out))
			}
			if took > 2*time.Second {
				t.Fatalf("took %v: the time limit did not hold", took)
			}
			t.Logf("%-16s %6v %v", name, took.Round(time.Millisecond), firstLine(err))
		})
	}
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 256<<20 {
		t.Fatalf("the server's heap grew by %d MB", grew>>20)
	}
}

func firstLine(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// One render cannot see another's data: every render is a fresh instance.
func TestRendersShareNothing(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := Slot(context.Background())
			if err != nil {
				errs <- err
				return
			}
			defer release()
			src := `{% assign mine = tenant %}{{ mine }}{{ leaked }}`
			if i%2 == 1 {
				src = `{{ mine }}{{ tenant }}`
			}
			out, err, _ := render(t, 5*time.Second, Request{Name: "x", Source: src, Data: map[string]any{"tenant": fmt.Sprint("t", i)}})
			if err != nil {
				errs <- err
				return
			}
			if out != fmt.Sprint("t", i) {
				errs <- fmt.Errorf("render %d answered %q", i, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func BenchmarkPage(b *testing.B) {
	if err := Prepare(); err != nil {
		b.Fatal(err)
	}
	files := make([]any, 50)
	for i := range files {
		files[i] = map[string]any{"name": fmt.Sprintf("file-%d <x>.pdf", i), "url": fmt.Sprintf("/s/abc/%d", i), "size": i * 1000}
	}
	req := Request{Name: "page", Host: host{},
		Source: `<ul>{% for f in files %}<li><a href="{{ f.url }}">{{ f.name }}</a> {{ f.size | bytes }}</li>{% endfor %}</ul>`,
		Layout: `<!doctype html><html><body>{{ content }}</body></html>`, LayoutAs: "layout",
		Data: map[string]any{"files": files}}
	for i := 0; i < b.N; i++ {
		if _, err := Render(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}
