package plugintest_test

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── a small plugin to test the kit against ─────────────────────────────

func text(en, tr string) wire.Text { return wire.Text{"en": en, "tr": tr} }

func manifest() wire.Manifest {
	return wire.Manifest{
		ManifestVersion: wire.ProtocolVersion,
		Name:            "demo",
		Version:         "1.0.0",
		Label:           text("Demo", "Deneme"),
		Languages:       []string{"en", "tr"},
		Permissions:     []string{"files:read", "files:write", "state", "engines:ffmpeg", "public_pages", "sign", "mail:send"},
		Actions: []wire.Action{{
			ID:      "shout",
			Label:   text("Shout", "Bağır"),
			Applies: wire.Applies{Kind: "file", Ext: []string{"txt"}},
			View:    "options",
			Output:  wire.Output{Mode: "sibling", Name: "{stem}-loud{ext}"},
		}},
		Views: []wire.View{{
			ID: "options", Placement: "modal", Label: text("Shout", "Bağır"), Size: "md",
		}},
		PublicPages: []wire.PublicPage{{
			ID: "read", Label: text("Read it", "Oku"), PIN: "optional", DefaultTTLDays: 7, MaxTTLDays: 30,
		}},
	}
}

// demo is a plugin whose screens and action are written the way the kit
// wants them: one primary button, a readable choice, both languages.
func demo(host *plugintest.Host) *pluginkit.Plugin {
	return &pluginkit.Plugin{
		Manifest: manifest(),
		Actions: map[string]pluginkit.ActionFunc{
			"shout": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				var outs []wire.OutputRef
				for _, f := range in.Inputs {
					b, err := host.ReadInput(f.Ref)
					if err != nil {
						return nil, err
					}
					body := strings.ToUpper(string(b))
					if in.Params["punctuation"] == "yes" {
						body += "!"
					}
					ref, err := host.WriteOutput(strings.TrimSuffix(f.Name, ".txt")+"-loud.txt", []byte(body))
					if err != nil {
						return nil, err
					}
					outs = append(outs, ref)
					host.Progress(int64(len(outs)), int64(len(in.Inputs)), "shouted "+f.Name)
				}
				return &wire.ActionRunOutput{OK: true, Outputs: outs, Message: text("Done", "Bitti")}, nil
			},
		},
		Views: map[string]pluginkit.ViewFunc{
			"options": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				tr := in.Context.Locale == "tr"
				pick := func(en, t string) string {
					if tr {
						return t
					}
					return en
				}
				s := &wire.Surface{
					Title: text("Shout", "Bağır"),
					Nodes: []wire.Node{{Type: "form", Props: map[string]any{
						"fields": []wire.Field{{
							Key: "punctuation", Type: "select", Label: pick("Punctuation", "Noktalama"),
							Options: []wire.FieldOption{
								{Value: "yes", Label: pick("With !", "! ile")},
								{Value: "no", Label: pick("Plain", "Düz")},
							},
						}},
						"values": map[string]any{"punctuation": "no"},
					}}},
					Actions: []wire.SurfaceAction{
						{ID: "submit", Label: text("Shout", "Bağır"), Primary: true},
						{ID: "cancel", Label: text("Cancel", "Vazgeç")},
					},
				}
				if in.Event == "submit" {
					return &wire.Surface{Job: &wire.JobRequest{ActionID: "shout", Params: map[string]any{"punctuation": "yes"}}}, nil
				}
				return s, nil
			},
		},
		Pages: map[string]pluginkit.PageFunc{
			"read": func(in *wire.ViewEventInput) (*wire.Surface, error) {
				var body string
				if len(in.Context.Inputs) > 0 {
					b, err := host.ReadInput(in.Context.Inputs[0].Ref)
					if err != nil {
						return nil, err
					}
					body = string(b)
				}
				return &wire.Surface{
					Title: text("Read it", "Oku"),
					Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": text(body, body)}}},
					Done:  true,
				}, nil
			},
		},
	}
}

func harness(t *testing.T) *plugintest.Harness {
	t.Helper()
	h := plugintest.New(nil)
	h.Host = plugintest.NewHost(manifest())
	h.Plugin = demo(h.Host)
	return h
}

func find(t *testing.T, r plugintest.Report, sev plugintest.Severity, substr string) plugintest.Finding {
	t.Helper()
	for _, f := range r {
		if f.Severity == sev && strings.Contains(f.Message, substr) {
			return f
		}
	}
	t.Fatalf("no %s finding mentioning %q in:\n%s", sev, substr, join(r))
	return plugintest.Finding{}
}

func none(t *testing.T, r plugintest.Report, sev plugintest.Severity, substr string) {
	t.Helper()
	for _, f := range r {
		if f.Severity == sev && strings.Contains(f.Message, substr) {
			t.Fatalf("unexpected %s finding %q at %s", sev, f.Message, f.Where)
		}
	}
}

func join(r plugintest.Report) string {
	var b strings.Builder
	for _, f := range r {
		b.WriteString("  " + f.String() + "\n")
	}
	if b.Len() == 0 {
		return "  (nothing)"
	}
	return b.String()
}

// ── the fake host tells the truth about permissions ────────────────────

func TestHostRefusesWhatTheManifestNeverAsked(t *testing.T) {
	m := manifest()
	m.Permissions = []string{"files:read"}
	h := plugintest.NewHost(m)
	h.EnterJob()

	if _, err := h.WriteOutput("x.txt", []byte("hi")); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("writing without files:write must be refused, got %v", err)
	}
	if _, err := h.EngineRun(pluginkit.EngineRequest{Engine: "ffmpeg"}); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("running an ungranted engine must be refused, got %v", err)
	}
	if err := h.MailSend("a@b.test", "hi", "there"); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("mail without mail:send must be refused, got %v", err)
	}
	if _, _, err := h.Setting("k"); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("settings without the settings permission must be refused, got %v", err)
	}
}

func TestScreenMayNotDoWhatOnlyAJobMay(t *testing.T) {
	h := plugintest.NewHost(manifest())
	h.InstallEngine("ffmpeg")
	ref := h.AddInput(plugintest.File{Name: "a.txt", Data: []byte("x")})
	h.EnterScreen()

	if _, err := h.WriteOutput("a.txt", []byte("x")); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("a screen may not create files, got %v", err)
	}
	if err := h.StateSet(ref.Ref, "k", "v"); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("a screen may not write state, got %v", err)
	}
	if _, err := h.EngineRun(pluginkit.EngineRequest{Engine: "ffmpeg"}); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("a screen may not run engines, got %v", err)
	}
	if _, err := h.ShareCreate(pluginkit.PageCreate{PageID: "read"}); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("a screen may not open public links, got %v", err)
	}
	// Reading is fine from a screen.
	if _, err := h.ReadInput(ref.Ref); err != nil {
		t.Fatalf("a screen may read its inputs: %v", err)
	}
}

func TestHostErrorCodesMatchTheRealOnes(t *testing.T) {
	h := plugintest.NewHost(manifest())
	h.EnterJob()
	h.InstallEngine("ffmpeg")

	if _, err := h.ReadInput("in:9"); !plugintest.IsCode(err, wire.ErrNotFound) {
		t.Fatalf("an unknown ref is not_found, got %q", plugintest.Code(err))
	}
	if _, err := h.EngineRun(pluginkit.EngineRequest{Engine: "ffmpeg", Args: []string{"../etc/passwd"}}); !plugintest.IsCode(err, wire.ErrInvalid) {
		t.Fatalf("a path argument is invalid, got %q", plugintest.Code(err))
	}
	if _, err := h.EngineRun(pluginkit.EngineRequest{Engine: "imagemagick"}); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("an ungranted engine is permission_denied, got %q", plugintest.Code(err))
	}
	h2 := plugintest.NewHost(func() wire.Manifest {
		m := manifest()
		m.Permissions = append(m.Permissions, "engines:imagemagick")
		return m
	}())
	h2.EnterJob()
	if _, err := h2.EngineRun(pluginkit.EngineRequest{Engine: "imagemagick"}); !plugintest.IsCode(err, wire.ErrUnavailable) {
		t.Fatalf("a granted engine that is not installed is unavailable, got %q", plugintest.Code(err))
	}
	ref := h.AddInput(plugintest.File{Name: "a.txt", Data: []byte("x")})
	if err := h.StateSet(ref.Ref, "k", strings.Repeat("x", 70<<10)); !plugintest.IsCode(err, wire.ErrTooLarge) {
		t.Fatalf("state over 64 KiB is too_large, got %q", plugintest.Code(err))
	}
	h.MailPerHour = 1
	m2 := manifest()
	_ = m2
	if err := h.MailSend("a@b.test", "s", "b"); err != nil {
		t.Fatalf("first mail: %v", err)
	}
	if err := h.MailSend("a@b.test", "s", "b"); !plugintest.IsCode(err, wire.ErrBusy) {
		t.Fatalf("over the mail quota is busy, got %q", plugintest.Code(err))
	}
	if _, err := h.HTTPDo(pluginkit.HTTPRequest{URL: "https://example.test/x"}); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("an ungranted host is permission_denied, got %q", plugintest.Code(err))
	}
}

// ── running the plugin ─────────────────────────────────────────────────

func TestRunAnAction(t *testing.T) {
	h := harness(t)
	out, err := h.Do("shout", map[string]any{"punctuation": "yes"}, plugintest.File{Name: "note.txt", Data: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || len(out.Outputs) != 1 {
		t.Fatalf("expected one output, got %+v", out)
	}
	if out.Outputs[0].Name != "note-loud.txt" {
		t.Fatalf("output name: %q", out.Outputs[0].Name)
	}
	got, ok := h.Host.Bytes(out.Outputs[0].Ref)
	if !ok || string(got) != "HELLO!" {
		t.Fatalf("output bytes: %q (%v)", got, ok)
	}
	if len(h.Host.ProgressLog) != 1 {
		t.Fatalf("expected one progress line, got %v", h.Host.ProgressLog)
	}
}

func TestSurfaceToJobRoundTrip(t *testing.T) {
	h := harness(t)
	h.Select(plugintest.File{Name: "note.txt", Data: []byte("hey")})
	s, err := h.Submit("options", nil, map[string]any{"punctuation": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Job == nil {
		t.Fatalf("submit should queue a job, got %+v", s)
	}
	out, err := h.Queue(s)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := h.Host.Bytes(out.Outputs[0].Ref)
	if string(b) != "HEY!" {
		t.Fatalf("queued job produced %q", b)
	}
}

func TestRegisteredMatchesTheManifest(t *testing.T) {
	h := harness(t)
	if r := plugintest.InspectRegistered(h.Plugin).Errors(); len(r) > 0 {
		t.Fatalf("the demo plugin should match its manifest:\n%s", join(r))
	}
	broken := demo(h.Host)
	delete(broken.Views, "options")
	find(t, plugintest.InspectRegistered(broken), plugintest.SevError, "nothing is registered to draw it")
}

// ── surface rules ──────────────────────────────────────────────────────

func TestGoodSurfacePasses(t *testing.T) {
	h := harness(t)
	s, err := h.Open("options", plugintest.File{Name: "note.txt", Data: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if r := plugintest.InspectSurface(h.Manifest(), s).Errors(); len(r) > 0 {
		t.Fatalf("the demo screen should pass:\n%s", join(r))
	}
	plugintest.CheckSurface(t, h.Manifest(), s)
	if got := plugintest.ChoiceValues(s, "punctuation"); len(got) != 2 {
		t.Fatalf("the choice should read as two buttons, got %v", got)
	}
	if a, ok := plugintest.PrimaryAction(s); !ok || a.ID != "submit" {
		t.Fatalf("primary action: %+v (%v)", a, ok)
	}
}

func TestSurfaceRulesCatchTheBadOnes(t *testing.T) {
	m := manifest()

	t.Run("unknown node type", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "dropdown"}}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "unknown node type")
	})

	t.Run("a select with no options is not a choice", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "form", Props: map[string]any{
			"fields": []wire.Field{{Key: "k", Type: "select", Label: "Pick"}},
		}}}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "ROW OF CHOICE BUTTONS")
	})

	t.Run("two primary buttons", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": text("a", "a")}}},
			Actions: []wire.SurfaceAction{
				{ID: "a", Label: text("A", "A"), Primary: true},
				{ID: "b", Label: text("B", "B"), Primary: true},
			}}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "one step asks ONE thing")
	})

	t.Run("a condition on a field that does not exist", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "form", Props: map[string]any{
			"fields": []wire.Field{
				{Key: "name", Type: "string", Label: "Name", ShowWhen: &wire.Condition{Key: "mode", Equals: []string{"new"}}},
			},
		}}}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "can never be true")
	})

	t.Run("a hidden field still carries a value", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "form", Props: map[string]any{
			"fields": []wire.Field{
				{Key: "mode", Type: "select", Label: "Where", Options: []wire.FieldOption{{Value: "version", Label: "New version"}, {Value: "new", Label: "New file"}}},
				{Key: "name", Type: "string", Label: "Name", ShowWhen: &wire.Condition{Key: "mode", Equals: []string{"new"}}},
			},
			"values": map[string]any{"mode": "version", "name": "report.pdf"},
		}}}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "DROPS it before the job runs")
	})

	t.Run("two fields with one key", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{
			{Type: "form", Props: map[string]any{"fields": []wire.Field{{Key: "k", Type: "string", Label: "A"}}}},
			{Type: "form", Props: map[string]any{"fields": []wire.Field{{Key: "k", Type: "string", Label: "B"}}}},
		}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "arrive FLAT")
	})

	t.Run("a list column format the host does not know", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "list", Props: map[string]any{
			"columns": []map[string]any{{"key": "due", "label": text("Due", "Son tarih"), "format": "day"}},
			"rows":    []map[string]any{{"id": "r", "cells": map[string]any{"due": "2026-09-29"}}},
		}}}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "is not date or datetime")
	})

	t.Run("steps without exactly one active", func(t *testing.T) {
		s := &wire.Surface{Nodes: []wire.Node{{Type: "steps", Props: map[string]any{
			"items": []map[string]any{
				{"id": "a", "label": text("A", "A"), "state": "done"},
				{"id": "b", "label": text("B", "B"), "state": "todo"},
			},
		}}}, Done: true}
		find(t, plugintest.InspectSurface(m, s), plugintest.SevError, "steps are active")
	})
}

func TestSubmittedDropsHiddenValues(t *testing.T) {
	fields := []wire.Field{
		{Key: "mode", Type: "select", Label: "Where", Options: []wire.FieldOption{{Value: "version", Label: "v"}, {Value: "new", Label: "n"}}},
		{Key: "name", Type: "string", Label: "Name", ShowWhen: &wire.Condition{Key: "mode", Equals: []string{"new"}}, RequiredWhen: &wire.Condition{Key: "mode", Equals: []string{"new"}}},
	}
	got := plugintest.Submitted(fields, map[string]any{"mode": "version", "name": "ghost.pdf"})
	if _, still := got["name"]; still {
		t.Fatalf("a hidden field's value must not reach the job: %v", got)
	}
	got = plugintest.Submitted(fields, map[string]any{"mode": "new", "name": "real.pdf"})
	if got["name"] != "real.pdf" {
		t.Fatalf("a visible field's value must reach the job: %v", got)
	}
	if req := plugintest.RequiredKeys(fields, map[string]any{"mode": "new"}); len(req) != 1 || req[0] != "name" {
		t.Fatalf("required_when should make `name` required when the output is a new file: %v", req)
	}
	if req := plugintest.RequiredKeys(fields, map[string]any{"mode": "version"}); len(req) != 0 {
		t.Fatalf("a hidden field is never required: %v", req)
	}
}

// ── language rules ─────────────────────────────────────────────────────

func TestEveryDeclaredLanguageIsThere(t *testing.T) {
	h := harness(t)
	s, err := h.Open("options", plugintest.File{Name: "note.txt", Data: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if r := plugintest.InspectLanguages(h.Manifest(), s, plugintest.LangOpts{}).Errors(); len(r) > 0 {
		t.Fatalf("the demo screen speaks both languages:\n%s", join(r))
	}
	plugintest.CheckManifestLanguages(t, h.Manifest())

	half := &wire.Surface{
		Title:   wire.Text{"en": "Sign"},
		Nodes:   []wire.Node{{Type: "text", Props: map[string]any{"text": wire.Text{"en": "Draw your signature"}}}},
		Actions: []wire.SurfaceAction{{ID: "ok", Label: wire.Text{"en": "Done"}, Primary: true}},
	}
	r := plugintest.InspectLanguages(h.Manifest(), half, plugintest.LangOpts{})
	find(t, r, plugintest.SevError, "the manifest promises \"tr\"")
	if len(r.Errors()) != 3 {
		t.Fatalf("every Text on the screen should be reported, got:\n%s", join(r))
	}

	pasted := &wire.Surface{Title: wire.Text{"en": "Draw / Write / Upload", "tr": "Draw / Write / Upload"}}
	find(t, plugintest.InspectLanguages(h.Manifest(), pasted, plugintest.LangOpts{}), plugintest.SevWarn, "the same words")
}

func TestLocaleParityCatchesAScreenHalfInOneLanguage(t *testing.T) {
	h := harness(t)
	byLocale, err := h.OpenInLocales("options", plugintest.File{Name: "note.txt", Data: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if len(byLocale) != 2 {
		t.Fatalf("expected the screen in both languages, got %d", len(byLocale))
	}
	r := plugintest.InspectLocaleParity(byLocale, plugintest.LangOpts{})
	if errs := r.Errors(); len(errs) > 0 {
		t.Fatalf("the demo screen has the same shape in both languages:\n%s", join(errs))
	}
	none(t, r, plugintest.SevWarn, "identical in")

	// The complaint this exists for: the pad's buttons are Turkish in both
	// renders, while everything around them is translated.
	pad := map[string]*wire.Surface{
		"en": {Title: text("Sign", "İmzala"), Nodes: []wire.Node{{Type: "signature-pad", Props: map[string]any{
			"id": "sig", "label": "Çiz / Yaz / Yükle",
		}}}, Done: true},
		"tr": {Title: text("Sign", "İmzala"), Nodes: []wire.Node{{Type: "signature-pad", Props: map[string]any{
			"id": "sig", "label": "Çiz / Yaz / Yükle",
		}}}, Done: true},
	}
	find(t, plugintest.InspectLocaleParity(pad, plugintest.LangOpts{}), plugintest.SevWarn, "identical in")

	missing := map[string]*wire.Surface{
		"en": {Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": text("a", "a")}}, {Type: "divider"}}, Done: true},
		"tr": {Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": text("a", "a")}}}, Done: true},
	}
	find(t, plugintest.InspectLocaleParity(missing, plugintest.LangOpts{}), plugintest.SevError, "same shape in every language")

	blank := map[string]*wire.Surface{
		"en": {Nodes: []wire.Node{{Type: "form", Props: map[string]any{"fields": []wire.Field{{Key: "k", Type: "string", Label: "Name"}}}}}, Done: true},
		"tr": {Nodes: []wire.Node{{Type: "form", Props: map[string]any{"fields": []wire.Field{{Key: "k", Type: "string", Label: ""}}}}}, Done: true},
	}
	find(t, plugintest.InspectLocaleParity(blank, plugintest.LangOpts{}), plugintest.SevError, "must not go blank")
}

func TestSameTextAllowlist(t *testing.T) {
	same := map[string]*wire.Surface{
		"en": {Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": wire.Text{"en": "JPEG", "tr": "JPEG"}}}}, Done: true},
		"tr": {Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": wire.Text{"en": "JPEG", "tr": "JPEG"}}}}, Done: true},
	}
	find(t, plugintest.InspectLocaleParity(same, plugintest.LangOpts{}), plugintest.SevWarn, "identical in")
	none(t, plugintest.InspectLocaleParity(same, plugintest.LangOpts{SameAllowed: []string{"JPEG"}}), plugintest.SevWarn, "identical in")
}

// ── manifest rules ─────────────────────────────────────────────────────

func TestManifestRules(t *testing.T) {
	plugintest.CheckManifest(t, manifest())

	bad := manifest()
	bad.Permissions = append(bad.Permissions, "files:delete")
	find(t, plugintest.InspectManifest(bad), plugintest.SevError, "the set is closed")

	bad = manifest()
	bad.Actions[0].View = "nowhere"
	find(t, plugintest.InspectManifest(bad), plugintest.SevError, "which the manifest does not declare")

	bad = manifest()
	bad.Languages = []string{"tr"}
	find(t, plugintest.InspectManifest(bad), plugintest.SevError, "English is required")

	bad = manifest()
	bad.Permissions = []string{"files:read"}
	find(t, plugintest.InspectManifest(bad), plugintest.SevError, "never asks for files:write")
}

// ── shares ─────────────────────────────────────────────────────────────

func TestSharesAreRealLinks(t *testing.T) {
	h := harness(t)
	in := h.Select(plugintest.File{Name: "nda.pdf", Data: []byte("%PDF-1.4")})[0]

	h.Host.EnterJob()
	created, err := h.Host.ShareCreate(pluginkit.PageCreate{
		PageID: "read", Subject: "Please read", PIN: "auto", TTLDays: 90,
		State: map[string]any{"who": "outside@example.test"},
		Files: []wire.OutputRef{{Ref: in.Ref, Name: "nda.pdf"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.PIN == "" {
		t.Fatalf(`PIN "auto" must mint one and hand it back once`)
	}
	sh, _ := h.Host.ShareByToken(created.Token)
	if sh.ExpiresAt.IsZero() {
		t.Fatalf("a share without an expiry is not a share")
	}
	if len(sh.Files) != 1 || !strings.HasPrefix(sh.Files[0].Ref, "pub:") {
		t.Fatalf("the exposed file should be a copy the visitor reads: %+v", sh.Files)
	}

	s, err := h.Page("read", created.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	plugintest.CheckSurface(t, h.Manifest(), s)

	if err := h.Host.ShareRevoke(created.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Page("read", created.Token, nil); err == nil {
		t.Fatalf("a revoked link must not open")
	}

	h.Host.EnterJob()
	if _, err := h.Host.ShareCreate(pluginkit.PageCreate{PageID: "nosuch"}); !plugintest.IsCode(err, wire.ErrInvalid) {
		t.Fatalf("a page the manifest never declared is invalid, got %q", plugintest.Code(err))
	}
}

// ── signing ────────────────────────────────────────────────────────────

func TestSigningProducesAVerifiableSignature(t *testing.T) {
	h := plugintest.NewHost(manifest())
	h.EnterJob()

	info, err := h.HostSignInfo()
	if err != nil || !info.Available {
		t.Fatalf("signing should be available: %+v %v", info, err)
	}
	issued, err := h.CertIssue("Burak", "burak@example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := h.NewSigner(issued)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("the document"))
	sig, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Cert.CheckSignature(x509.ECDSAWithSHA256, []byte("the document"), sig); err != nil {
		t.Fatalf("the signature must verify against the leaf: %v", err)
	}
	if err := h.KeyDestroy(issued.KeyRef); err != nil {
		t.Fatal(err)
	}
	if _, err := h.HostSign(issued.KeyRef, digest[:]); !plugintest.IsCode(err, wire.ErrNotFound) {
		t.Fatalf("a destroyed key signs nothing, got %q", plugintest.Code(err))
	}

	screen := plugintest.NewHost(manifest())
	screen.EnterScreen()
	if _, err := screen.CertIssue("Burak", "", 30); !plugintest.IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("a screen may not issue certificates, got %q", plugintest.Code(err))
	}
}

// ── golden screens ─────────────────────────────────────────────────────

func TestGoldenWritesThenCompares(t *testing.T) {
	old := plugintest.GoldenDir
	plugintest.GoldenDir = t.TempDir()
	defer func() { plugintest.GoldenDir = old }()

	h := harness(t)
	s, err := h.Open("options", plugintest.File{Name: "note.txt", Data: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGINTEST_UPDATE", "1")
	plugintest.Golden(t, "options", s)

	t.Setenv("PLUGINTEST_UPDATE", "0")
	plugintest.Golden(t, "options", s)

	rec := &recorder{}
	changed := *s
	changed.Actions = append([]wire.SurfaceAction(nil), s.Actions...)
	changed.Actions[0].Primary = false
	plugintest.Golden(rec, "options", &changed)
	if len(rec.errors) == 0 || !strings.Contains(rec.errors[0], "changed shape") {
		t.Fatalf("a changed screen must be reported: %v", rec.errors)
	}
}

// recorder is a TB that records instead of failing, so the kit can test
// its own failure paths.
type recorder struct {
	errors []string
	logs   []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

// v0.43.0 wave 2: a personal key is written for one person
// (wire.PersonalState), never as `@me` — that is only how filex SHOWS it —
// and a lock's reason may name a manifest message, which the fake host
// refuses when the manifest does not declare it, as filex does.
func TestHostPersonalStateAndLockMessages(t *testing.T) {
	m := manifest()
	m.Permissions = append(m.Permissions, "state", "files:lock")
	m.Messages = map[string]wire.Text{"held": {"en": "held for {who}", "tr": "{who} için tutuluyor"}}
	h := plugintest.NewHost(m)
	h.EnterJob()
	ref := h.AddInput(plugintest.File{Name: "a.pdf", Data: []byte("%PDF")}).Ref

	if err := h.StateSet(ref, "todo@me", "1"); !plugintest.IsCode(err, wire.ErrInvalid) {
		t.Fatalf("a key stored as @me would read as everybody's; want refused, got %v", err)
	}
	if err := h.StateSet(ref, wire.PersonalState("todo", 7), "1"); err != nil {
		t.Fatalf("a personal key: %v", err)
	}

	if _, err := h.FileLockMessage(ref, 1, "nope", nil); !plugintest.IsCode(err, wire.ErrInvalid) {
		t.Fatalf("a message the manifest does not declare must be refused, got %v", err)
	}
	if _, err := h.FileLockMessage(ref, 1, "held", map[string]string{"who": "Ayşe"}); err != nil {
		t.Fatal(err)
	}
	if got := h.LockReason(ref); got["tr"] != "Ayşe için tutuluyor" || got["en"] != "held for Ayşe" {
		t.Fatalf("the lock's reason in every language: %v", got)
	}
}

// The v0.43 manifest words are checked where an author meets them — at `go
// test` — the way filex checks them at install.
func TestManifestWave2Words(t *testing.T) {
	withAction := func(a wire.Action) wire.Manifest {
		m := manifest()
		m.Actions = append(m.Actions, a)
		return m
	}
	find(t, plugintest.InspectManifest(withAction(wire.Action{ID: "c", Label: text("C", "C"),
		Output: wire.Output{Mode: "version", Elsewhere: true}})), plugintest.SevError, "output.elsewhere is for a new file")
	find(t, plugintest.InspectManifest(withAction(wire.Action{ID: "c", Label: text("C", "C"),
		Output: wire.Output{Mode: "folder"}})), plugintest.SevError, "the person's choice for one job")
	find(t, plugintest.InspectManifest(withAction(wire.Action{ID: "f", Label: text("F", "F"),
		Applies: wire.Applies{State: []string{"todo@7"}}, Output: wire.Output{Mode: "none"}})), plugintest.SevError, "a personal key is named")
	noWrite := manifest()
	noWrite.Permissions = []string{"files:read"}
	noWrite.Actions = []wire.Action{{ID: "r", Label: text("R", "R"), Applies: wire.Applies{Writable: true}, Output: wire.Output{Mode: "none"}}}
	find(t, plugintest.InspectManifest(noWrite), plugintest.SevError, "applies.writable")
	half := manifest()
	half.Messages = map[string]wire.Text{"held": {"en": "held"}}
	find(t, plugintest.InspectManifestLanguages(half, plugintest.LangOpts{}), plugintest.SevError, "has no tr")
}
