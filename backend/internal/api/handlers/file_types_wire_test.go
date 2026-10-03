package handlers

// The wire fixtures of Default apps (0.50), WRITTEN BY THE SERVER - the rule
// app_plugins_wire_test.go explains: a hand-written fixture is the client's
// belief about the wire, so it can only agree with the client. These are the
// bytes the handlers send, built by the real assoc.Service over a store and a
// set of apps:
//
//	admin-file-types.json        GET /api/admin/file-types
//	app-plugin-file-types.json   the `file_types` of an install's review
//	app-plugin-thumb-limits.json GET /api/admin/app-plugins/{id}/thumbnails
//
// The web tests read them (web/tests/components/defaultApps.test.ts,
// appPluginFileTypes.test.ts).
//
//	FILEX_UPDATE_WIRE_FIXTURES=1 go test ./internal/api/handlers/ -run TestFileTypesWireFixtures

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// wireAssocStore is the rules table, in memory.
type wireAssocStore struct{ rows []*model.FileAssociation }

func (s *wireAssocStore) ListFileAssociations(context.Context) ([]*model.FileAssociation, error) {
	return s.rows, nil
}

func (s *wireAssocStore) PutFileAssociation(_ context.Context, a *model.FileAssociation) error {
	for i, r := range s.rows {
		if r.Capability == a.Capability && r.Ext == a.Ext {
			s.rows[i] = a
			return nil
		}
	}
	s.rows = append(s.rows, a)
	return nil
}

func (s *wireAssocStore) DeleteFileAssociation(_ context.Context, capability, ext string) (bool, error) {
	for i, r := range s.rows {
		if r.Capability == capability && r.Ext == ext {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// wireAssocApps are the apps installed: two that open .drawio, one that draws
// the thumbnails of Java archives, one that would redraw PNGs.
type wireAssocApps struct{}

func (wireAssocApps) OpenHandlers() []assoc.AppHandler {
	return []assoc.AppHandler{
		{Handler: assoc.Handler{ID: assoc.OpenID("drawio", "editor"), App: "drawio", View: "editor", Version: "1.2.0",
			Label: wire.Text{"en": "draw.io", "tr": "draw.io"}}, Ext: []string{"drawio"}},
		{Handler: assoc.Handler{ID: assoc.OpenID("zeta", "viewer"), App: "zeta", View: "viewer", Version: "2.0.0",
			Label: wire.Text{"en": "Zeta viewer", "tr": "Zeta görüntüleyici"}}, Ext: []string{"drawio"}},
	}
}

func (wireAssocApps) ThumbnailHandlers() []assoc.AppHandler {
	return []assoc.AppHandler{
		{Handler: assoc.Handler{ID: assoc.ThumbID("pkglist"), App: "pkglist", Version: "0.1.0",
			Label: wire.Text{"en": "Package contents", "tr": "Paket içeriği"}}, Ext: []string{"apk"}, Mime: []string{"application/java-archive"}},
		{Handler: assoc.Handler{ID: assoc.ThumbID("pngplus"), App: "pngplus", Version: "1.0.0",
			Label: wire.Text{"en": "PNG+", "tr": "PNG+"}}, Ext: []string{"png"}},
	}
}

func wireAssocService(t *testing.T) *assoc.Service {
	t.Helper()
	svc := assoc.New(&wireAssocStore{})
	svc.SetSource(wireAssocApps{})
	svc.SetBuiltinThumb(thumb.BuiltinDraws)
	// The one change the administrator made: PNGs are drawn by filex alone.
	_, err := svc.Put(context.Background(), assoc.CapThumbnail, "png", assoc.Rule{Order: []string{assoc.Builtin}, Off: []string{assoc.ThumbID("pngplus")}}, nil)
	require.NoError(t, err)
	return svc
}

func TestFileTypesWireFixtures(t *testing.T) {
	svc := wireAssocService(t)
	ctx := context.Background()
	req := httptest.NewRequest("GET", "/api/admin/file-types", nil)
	fixtures := map[string]any{
		"admin-file-types.json": NewFileTypesAdmin(svc, false).body(req),
		// An app being installed that opens .drawio (after draw.io and Zeta
		// by default) and draws the thumbnails of .whl files, which nobody
		// draws yet (so it would be first).
		"app-plugin-file-types.json": map[string]any{
			"file_types": svc.InstallKinds(ctx,
				[]assoc.AppHandler{{Handler: assoc.Handler{ID: assoc.OpenID("sign", "editor"), App: "sign", View: "editor", Version: "1.2.0",
					Label: wire.Text{"en": "Editor", "tr": "Düzenleyici"}}, Ext: []string{"drawio"}}},
				[]assoc.AppHandler{{Handler: assoc.Handler{ID: assoc.ThumbID("sign"), App: "sign", Version: "1.2.0",
					Label: wire.Text{"en": "e-Signature", "tr": "e-İmza"}}, Ext: []string{"whl"}}},
			),
		},
		// An app's limits: the time per file raised to 20 s, the rest the
		// defaults (memory is the manifest's).
		"app-plugin-thumb-limits.json": &wasmplugin.ThumbLimitsAnswer{
			Values:   wasmplugin.ThumbLimits{MaxInputMB: 32, TimeoutS: 20, MemoryMB: 64, Concurrency: 2},
			Stored:   wasmplugin.ThumbLimits{TimeoutS: 20},
			Defaults: wasmplugin.ThumbLimits{MaxInputMB: 32, TimeoutS: 10, MemoryMB: 64, Concurrency: 2},
			Min:      wasmplugin.ThumbLimits{MaxInputMB: 1, TimeoutS: 1, MemoryMB: 16, Concurrency: 1},
			Max:      wasmplugin.ThumbLimits{MaxInputMB: 256, TimeoutS: 60, MemoryMB: 256, Concurrency: 8},
			Ext:      []string{"jar", "apk"},
			Mime:     []string{"application/java-archive"},
		},
	}
	update := os.Getenv("FILEX_UPDATE_WIRE_FIXTURES") != ""
	for name, body := range fixtures {
		got := wireBytes(t, body)
		path := filepath.Join("testdata", "wire", name)
		if update {
			require.NoError(t, os.WriteFile(path, got, 0o644))
			continue
		}
		want, err := os.ReadFile(path)
		require.NoError(t, err, "missing wire fixture - run with FILEX_UPDATE_WIRE_FIXTURES=1")
		require.Equalf(t, string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), string(got),
			"%s no longer matches what the server sends. Regenerate it (FILEX_UPDATE_WIRE_FIXTURES=1), then run the web tests that read it.", name)
	}
}
