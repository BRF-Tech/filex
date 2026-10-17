package wasmplugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The kinds an app declares, and the files its release offers ─────────
//
// An app names the kinds of file an administrator may add to it
// (`ui.extras`, extras.go): its admin page shows a section for each, and only
// those are served or accepted. A kind may carry a CATALOG - a list of files
// the app's release offers, pinned by sha256 in the manifest like the
// interface bundle - so the administrator installs a language with one click
// instead of finding and uploading its files:
//
//	{
//	  "format": "filex-extras/1",
//	  "kind": "dictionaries",
//	  "items": [{
//	    "id": "pl_PL", "label": "Polski", "license": "LGPL-2.1-or-later",
//	    "files": [
//	      {"path": "pl_PL/pl_PL.aff.txt", "url": "dict-pl_PL.aff", "size": 251234, "sha256": "…"},
//	      {"path": "pl_PL/pl_PL.dic.txt", "url": "dict-pl_PL.dic", "size": 4210987, "sha256": "…"}
//	    ]
//	  }]
//	}
//
// The catalog is read where the app was installed from: `{tag}` is the tag a
// repository install followed, a relative address is read beside the
// manifest (the repository at that tag, or a URL install's manifest). A
// file's `url` is absolute (https) or relative to the catalog's own address.
// filex downloads every file of the item through the guarded client every
// app download takes (fetch.go: public addresses only, https), refuses any
// whose size or sha256 is not the catalog's, and stores them as an
// administrator's upload would be (PutExtra: the same paths, ceilings and
// atomic writes). docs/APP-PLUGINS-API.md → Files an administrator adds to an
// app.

// ExtraCatalogFormat is the catalog's `format`.
const ExtraCatalogFormat = "filex-extras/1"

const (
	// maxExtraCatalogBytes bounds the catalog document.
	maxExtraCatalogBytes = 1 << 20
	// maxExtraCatalogItems bounds its items (ONLYOFFICE publishes about 50
	// dictionaries).
	maxExtraCatalogItems = 500
	// maxExtraItemFiles bounds one item's files (a dictionary is two or three).
	maxExtraItemFiles = 8
	// extraInstallTimeout bounds one item's downloads.
	extraInstallTimeout = 10 * time.Minute
)

// extraItemRe is an item's id: a dictionary's language folder.
var extraItemRe = regexp.MustCompile(`^[A-Za-z_]{2,16}$`)

// Why a catalog could not be offered or installed (handlers say each in the
// reader's words, server.error.extras_*).
var (
	// ErrExtraUndeclared: the app does not name the kind in `ui.extras`.
	ErrExtraUndeclared = errors.New("extras: this app does not take files of that kind")
	// ErrExtraNoCatalog: the app names the kind but offers no catalog for it.
	ErrExtraNoCatalog = errors.New("extras: this app's release offers no catalog of that kind")
	// ErrExtraNoCatalogSource: the catalog's address needs a tag or a
	// manifest address this install does not have (an upload).
	ErrExtraNoCatalogSource = errors.New("extras: the catalog is read where the app was installed from, and this install has no such address")
	// ErrExtraItemMissing: the catalog has no such item.
	ErrExtraItemMissing = errors.New("extras: the catalog has no such item")
)

// ExtraCatalogError is a catalog, or one of its files, that could not be
// read or is not what the manifest or the catalog pins.
type ExtraCatalogError struct {
	// File is the item's file it is about ("" for the catalog itself).
	File string
	Msg  string
}

func (e *ExtraCatalogError) Error() string {
	if e.File != "" {
		return "extras: " + e.File + ": " + e.Msg
	}
	return "extras: the catalog: " + e.Msg
}

// ExtraCatalogFile is one file of a catalog item.
type ExtraCatalogFile struct {
	// Path is where it goes under the kind's folder (`pl_PL/pl_PL.dic.txt`).
	Path   string `json:"path"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ExtraCatalogItem is one thing a catalog offers: a dictionary's language.
type ExtraCatalogItem struct {
	ID      string             `json:"id"`
	Label   string             `json:"label,omitempty"`
	License string             `json:"license,omitempty"`
	Files   []ExtraCatalogFile `json:"files"`
}

type extraCatalogDoc struct {
	Format string             `json:"format"`
	Kind   string             `json:"kind"`
	Items  []ExtraCatalogItem `json:"items"`
}

// ExtraCatalogRow is one item as the administrator's screen lists it.
type ExtraCatalogRow struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	License string `json:"license,omitempty"`
	// Size is its files' together, as the catalog says them.
	Size  int64 `json:"size"`
	Files int   `json:"files"`
	// Installed: every file of it is among the app's added files, at the
	// catalog's size; Partial: some are.
	Installed bool `json:"installed"`
	Partial   bool `json:"partial,omitempty"`
}

// ExtraCatalogAnswer is a kind's catalog for one app (GET …/extras-catalog/{kind}).
type ExtraCatalogAnswer struct {
	Kind  string            `json:"kind"`
	Items []ExtraCatalogRow `json:"items"`
}

// checkUIExtras holds a manifest's `ui.extras` to the kinds filex takes, the
// package_fetch they are read through, and a pinned catalog.
func checkUIExtras(ui *wire.UISpec) error {
	if len(ui.Extras) == 0 {
		return nil
	}
	if !ui.PackageFetch {
		return fmt.Errorf("manifest: ui.extras needs ui.package_fetch: the interface reads the files an administrator adds from its own package address")
	}
	for name, x := range ui.Extras {
		if _, ok := extraKindNamed(name); !ok {
			return fmt.Errorf("manifest: ui.extras: %q is not a kind of file filex lets an administrator add (%s)", name, strings.Join(ExtraKinds(), ", "))
		}
		if x.Catalog == nil {
			continue
		}
		x.Catalog.URL = strings.TrimSpace(x.Catalog.URL)
		x.Catalog.SHA256 = strings.ToLower(strings.TrimSpace(x.Catalog.SHA256))
		if x.Catalog.URL == "" {
			return fmt.Errorf("manifest: ui.extras.%s.catalog.url is empty", name)
		}
		if !sha256Re.MatchString(x.Catalog.SHA256) {
			return fmt.Errorf("manifest: ui.extras.%s.catalog.sha256 must be 64 hex characters: a catalog is pinned like the bundle", name)
		}
		ui.Extras[name] = x
	}
	return nil
}

// extraKindNamed is the kind called name.
func extraKindNamed(name string) (extraKind, bool) {
	for _, k := range extraKinds {
		if k.Name == name {
			return k, true
		}
	}
	return extraKind{}, false
}

// declaredExtras are the kinds a manifest names in `ui.extras` that filex
// takes: what its admin page offers, what is accepted and served.
func declaredExtras(m *Manifest) map[string]bool {
	out := map[string]bool{}
	if m == nil || m.UI == nil {
		return out
	}
	for name := range m.UI.Extras {
		if _, ok := extraKindNamed(name); ok {
			out[name] = true
		}
	}
	return out
}

// declaredKinds is declaredExtras as a sorted list (the admin's answer).
func declaredKinds(m *Manifest) []string {
	out := []string{}
	for name := range declaredExtras(m) {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// extraCatalogOf is the catalog app p declares for kind.
func extraCatalogOf(p *Installed, kind string) (*wire.UIBundle, error) {
	if !declaredExtras(p.Manifest)[kind] {
		return nil, ErrExtraUndeclared
	}
	x := p.Manifest.UI.Extras[kind]
	if x.Catalog == nil || x.Catalog.URL == "" {
		return nil, ErrExtraNoCatalog
	}
	return x.Catalog, nil
}

// extrasAddress is where raw, an address a manifest names, is read for the
// installed app p: `{tag}` and a relative address against where it was
// installed from (repoFile for a repository install; the manifest's own
// address for a URL install).
func (r *Registry) extrasAddress(p *Installed, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if repo, tag, ok := parseGitHubSource(p.Row.SourceURL); ok {
		return r.repoFile(repo, tag, tag, raw), nil
	}
	if strings.Contains(raw, "{tag}") {
		return "", ErrExtraNoCatalogSource
	}
	if strings.Contains(raw, "://") {
		return raw, nil
	}
	if base, err := url.Parse(strings.TrimSpace(p.Row.ManifestURL)); err == nil && base.Scheme != "" {
		if ref, err := url.Parse(raw); err == nil {
			return base.ResolveReference(ref).String(), nil
		}
	}
	return "", ErrExtraNoCatalogSource
}

// readExtraCatalog fetches and checks app p's catalog of kind: its sha256 is
// the manifest's, its format and kind are this one, every item and file of
// the shape filex stores. at is the catalog's address (a relative file
// address is read against it).
func (r *Registry) readExtraCatalog(ctx context.Context, p *Installed, kind string) (doc *extraCatalogDoc, at *url.URL, err error) {
	pin, err := extraCatalogOf(p, kind)
	if err != nil {
		return nil, nil, err
	}
	addr, err := r.extrasAddress(p, pin.URL)
	if err != nil {
		return nil, nil, err
	}
	at, err = url.Parse(addr)
	if err != nil {
		return nil, nil, &ExtraCatalogError{Msg: "not an address"}
	}
	b, err := r.fetch(ctx, addr, maxExtraCatalogBytes)
	if err != nil {
		return nil, nil, &ExtraCatalogError{Msg: err.Error()}
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != pin.SHA256 {
		return nil, nil, &ExtraCatalogError{Msg: "its sha256 is not the one the app's manifest pins"}
	}
	doc = &extraCatalogDoc{}
	if err := json.Unmarshal(b, doc); err != nil {
		return nil, nil, &ExtraCatalogError{Msg: "not a catalog: " + err.Error()}
	}
	if doc.Format != ExtraCatalogFormat || doc.Kind != kind {
		return nil, nil, &ExtraCatalogError{Msg: fmt.Sprintf("format %q of kind %q, not %s of %s", doc.Format, doc.Kind, ExtraCatalogFormat, kind)}
	}
	if len(doc.Items) > maxExtraCatalogItems {
		return nil, nil, &ExtraCatalogError{Msg: fmt.Sprintf("more than %d items", maxExtraCatalogItems)}
	}
	k, _ := extraKindNamed(kind)
	seen := map[string]bool{}
	for i := range doc.Items {
		it := &doc.Items[i]
		if !extraItemRe.MatchString(it.ID) || seen[it.ID] {
			return nil, nil, &ExtraCatalogError{Msg: fmt.Sprintf("item %q: an id is letters and _, 2 to 16 of them, each once", it.ID)}
		}
		seen[it.ID] = true
		if len(it.Files) == 0 || len(it.Files) > maxExtraItemFiles {
			return nil, nil, &ExtraCatalogError{Msg: fmt.Sprintf("item %s: 1 to %d files", it.ID, maxExtraItemFiles)}
		}
		for _, f := range it.Files {
			rel := kind + "/" + f.Path
			if got, ok := extraKindOf(rel); !ok || got.Name != k.Name || !strings.HasPrefix(f.Path, it.ID+"/") {
				return nil, nil, &ExtraCatalogError{File: f.Path, Msg: "not a place a file of item " + it.ID + " may have"}
			}
			if f.Size <= 0 || f.Size > extraCaps.file || !sha256Re.MatchString(f.SHA256) || strings.TrimSpace(f.URL) == "" {
				return nil, nil, &ExtraCatalogError{File: f.Path, Msg: "a file has an address, a size up to the ceiling and a sha256"}
			}
		}
	}
	return doc, at, nil
}

// ExtraCatalog answers app id's catalog of kind, each item with whether its
// files are already among the app's added files.
func (r *Registry) ExtraCatalog(ctx context.Context, id int64, kind string) (*ExtraCatalogAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	doc, _, err := r.readExtraCatalog(ctx, p, kind)
	if err != nil {
		return nil, err
	}
	files, _, err := r.listExtras(p.Row.Name)
	if err != nil {
		return nil, err
	}
	have := map[string]int64{}
	for _, f := range files {
		have[f.Path] = f.Size
	}
	ans := &ExtraCatalogAnswer{Kind: kind, Items: []ExtraCatalogRow{}}
	for _, it := range doc.Items {
		row := ExtraCatalogRow{ID: it.ID, Label: it.Label, License: it.License, Files: len(it.Files)}
		got := 0
		for _, f := range it.Files {
			row.Size += f.Size
			if size, ok := have[kind+"/"+f.Path]; ok && size == f.Size {
				got++
			}
		}
		row.Installed = got == len(it.Files)
		row.Partial = got > 0 && !row.Installed
		ans.Items = append(ans.Items, row)
	}
	return ans, nil
}

// InstallExtraItem downloads every file of item from app id's catalog of
// kind, holds each to the catalog's size and sha256, and only then stores
// them (PutExtra): a file that does not match leaves nothing new behind.
func (r *Registry) InstallExtraItem(ctx context.Context, id int64, kind, item string) ([]ExtraFile, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	if !p.Grants.Has(PermUIPackageFetch) {
		return nil, ErrExtrasUnused
	}
	ctx, cancel := context.WithTimeout(ctx, extraInstallTimeout)
	defer cancel()
	doc, at, err := r.readExtraCatalog(ctx, p, kind)
	if err != nil {
		return nil, err
	}
	var it *ExtraCatalogItem
	for i := range doc.Items {
		if doc.Items[i].ID == item {
			it = &doc.Items[i]
		}
	}
	if it == nil {
		return nil, ErrExtraItemMissing
	}
	bodies := make([][]byte, len(it.Files))
	for i, f := range it.Files {
		ref, err := url.Parse(strings.TrimSpace(f.URL))
		if err != nil {
			return nil, &ExtraCatalogError{File: f.Path, Msg: "not an address"}
		}
		addr := at.ResolveReference(ref).String()
		b, err := r.fetch(ctx, addr, f.Size)
		if err != nil {
			return nil, &ExtraCatalogError{File: f.Path, Msg: err.Error()}
		}
		sum := sha256.Sum256(b)
		if int64(len(b)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
			return nil, &ExtraCatalogError{File: f.Path, Msg: "its size or sha256 is not the catalog's"}
		}
		bodies[i] = b
	}
	out := make([]ExtraFile, 0, len(it.Files))
	for i, f := range it.Files {
		rel := path.Clean(kind + "/" + f.Path)
		ef, err := r.PutExtra(id, rel, bytes.NewReader(bodies[i]))
		if err != nil {
			return out, err
		}
		out = append(out, *ef)
	}
	return out, nil
}
