package wasmplugin

// The rows apps add to filex's "New" menu (`new_documents`): what the
// explorer offers, and the bytes a new file of that kind starts as.

import (
	"context"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// AppNewDocument is one row an app adds to the "New" menu, as the explorer
// is told it. Key is what a create request names (`app:<plugin>:<ext>`).
type AppNewDocument struct {
	Key      string    `json:"key"`
	Plugin   string    `json:"plugin"`
	View     string    `json:"view"`
	Ext      string    `json:"ext"`
	Label    wire.Text `json:"label"`
	Mime     string    `json:"mime"`
	Template bool      `json:"template"`
}

const newDocKeyPrefix = "app:"

// NewDocumentKey is the key a row is asked for by.
func NewDocumentKey(plugin, ext string) string { return newDocKeyPrefix + plugin + ":" + ext }

// IsNewDocumentKey reports whether a create request names an app's row.
func IsNewDocumentKey(key string) bool { return strings.HasPrefix(key, newDocKeyPrefix) }

// NewDocuments is every row the running apps add to the "New" menu, app by
// app in name order and in each app's own order. A row is offered only while
// the app runs with its interface loaded, and only when the grant holds its
// kind (`ui-new:.<ext>`).
func (r *Registry) NewDocuments() []AppNewDocument {
	var out []AppNewDocument
	for _, p := range r.All() {
		for _, d := range p.Manifest.NewDocuments {
			if row, ok := r.newDocOf(p, d); ok {
				out = append(out, row)
			}
		}
	}
	return out
}

// NewDocument is the row a key names, when it is offered.
func (r *Registry) NewDocument(key string) (AppNewDocument, bool) {
	p, d, ok := r.newDocByKey(key)
	if !ok {
		return AppNewDocument{}, false
	}
	return r.newDocOf(p, d)
}

// NewDocumentBytes is what a new file of the row a key names starts as: the
// template from the app's package, or nothing. Refused for a row that is not
// offered (unknown, not granted, the app switched off).
func (r *Registry) NewDocumentBytes(ctx context.Context, key string) ([]byte, error) {
	p, d, ok := r.newDocByKey(key)
	if !ok {
		return nil, fmt.Errorf("no such new document: %q", key)
	}
	if _, offered := r.newDocOf(p, d); !offered {
		return nil, fmt.Errorf("new document %q is not offered", key)
	}
	if d.Template == "" {
		return []byte{}, nil
	}
	ui := p.UI()
	if ui == nil {
		return nil, fmt.Errorf("the app's interface is not loaded")
	}
	rc, size, err := ui.open(d.Template)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", d.Template, err)
	}
	defer rc.Close()
	if size > maxNewDocTemplate {
		return nil, fmt.Errorf("template %s is larger than 16 MiB", d.Template)
	}
	return io.ReadAll(io.LimitReader(rc, maxNewDocTemplate))
}

func (r *Registry) newDocByKey(key string) (*Installed, wire.NewDocument, bool) {
	rest, ok := strings.CutPrefix(key, newDocKeyPrefix)
	if !ok {
		return nil, wire.NewDocument{}, false
	}
	plugin, ext, ok := strings.Cut(rest, ":")
	if !ok || plugin == "" || ext == "" {
		return nil, wire.NewDocument{}, false
	}
	p, found := r.ByName(plugin)
	if !found || p.Manifest == nil {
		return nil, wire.NewDocument{}, false
	}
	for _, d := range p.Manifest.NewDocuments {
		if d.Ext == ext {
			return p, d, true
		}
	}
	return nil, wire.NewDocument{}, false
}

func (r *Registry) newDocOf(p *Installed, d wire.NewDocument) (AppNewDocument, bool) {
	if state, _ := p.State(); state != StateRunning || p.UI() == nil {
		return AppNewDocument{}, false
	}
	if !p.Grants.Has(Permission(permPrefixUINew + "." + d.Ext)) {
		return AppNewDocument{}, false
	}
	v, ok := p.Manifest.View(d.View)
	if !ok || v.Placement != "viewer" || v.UI == "" {
		return AppNewDocument{}, false
	}
	kind := strings.TrimSpace(strings.SplitN(mime.TypeByExtension("."+d.Ext), ";", 2)[0])
	if kind == "" {
		kind = "application/octet-stream"
		for _, mt := range v.Applies.Mime {
			if !strings.HasSuffix(mt, "/*") {
				kind = mt
				break
			}
		}
	}
	return AppNewDocument{
		Key: NewDocumentKey(p.Row.Name, d.Ext), Plugin: p.Row.Name, View: d.View, Ext: d.Ext,
		Label: d.Label, Mime: kind, Template: d.Template != "",
	}, true
}
