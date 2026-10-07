package appstore

// The embedded store's catalog (#162): filex reads a trusted store's SIGNED
// index (`<store>/v1/index.json` + `.sig`, the store's client contract) on
// the SERVER, verifies it with the store's trusted index key, and hands the
// browser a small projection of it. The browser never talks to the store:
// filex's Content-Security-Policy does not change, and icons come through
// filex too (Media), each one held to the sha256 its name says.
//
// A catalog is kept 10 minutes (catalogTTL). When the store cannot be
// reached, the last catalog that verified is served marked stale; one that
// does not verify, has expired or is another schema is refused, never served.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// Catalog limits and times.
const (
	catalogTTL    = 10 * time.Minute
	maxIndexBytes = 16 << 20
	maxSigBytes   = 4 << 10
	maxMediaBytes = 5 << 20
	mediaTTL      = time.Hour
)

// CatalogApp is one app of a store's catalog as filex's own screen draws it:
// the newest version that is not yanked, what it asks for, its icon (a file
// name Media serves). Revoked apps and apps with no version left are not in
// a catalog.
type CatalogApp struct {
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	Label             map[string]string `json:"label"`
	Summary           map[string]string `json:"summary,omitempty"`
	Publisher         string            `json:"publisher"`
	PublisherVerified bool              `json:"publisher_verified,omitempty"`
	PublisherOfficial bool              `json:"publisher_official,omitempty"`
	Categories        []string          `json:"categories"`
	Repo              string            `json:"repo"`
	Version           string            `json:"version"`
	PublishedAt       string            `json:"published_at,omitempty"`
	FilexRange        string            `json:"filex_range"`
	Permissions       []string          `json:"permissions"`
	Icon              string            `json:"icon,omitempty"`
	// The pins of that version, frozen with a request made from it (never
	// sent to the browser).
	ManifestSHA256 string `json:"-"`
	WasmSHA256     string `json:"-"`
	UISHA256       string `json:"-"`
}

// Catalog is a store's catalog at one serial.
type Catalog struct {
	Store     string       `json:"store"`
	Serial    int64        `json:"serial"`
	FetchedAt time.Time    `json:"fetched_at"`
	Stale     bool         `json:"stale"`
	Apps      []CatalogApp `json:"apps"`
	icons     map[string]bool
}

// App answers the catalog's entry for name.
func (c *Catalog) App(name string) (CatalogApp, bool) {
	if c == nil {
		return CatalogApp{}, false
	}
	for _, a := range c.Apps {
		if a.Name == name {
			return a, true
		}
	}
	return CatalogApp{}, false
}

// the index as filex reads it: the store's schema 1, fields it does not need
// left out (a field the store adds later is ignored).
type indexDoc struct {
	Schema     int    `json:"schema"`
	Serial     int64  `json:"serial"`
	ExpiresAt  string `json:"expires_at"`
	Publishers []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Verified bool   `json:"verified"`
		Official bool   `json:"official"`
	} `json:"publishers"`
	Apps []struct {
		Name       string            `json:"name"`
		Kind       string            `json:"kind"`
		Publisher  string            `json:"publisher"`
		Repo       string            `json:"repo"`
		Categories []string          `json:"categories"`
		Label      map[string]string `json:"label"`
		Summary    map[string]string `json:"summary"`
		Icon       *struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"icon"`
		Latest   string          `json:"latest"`
		Revoked  json.RawMessage `json:"revoked"`
		Versions []struct {
			Version     string `json:"version"`
			Filex       string `json:"filex"`
			PublishedAt string `json:"published_at"`
			Manifest    struct {
				SHA256 string `json:"sha256"`
			} `json:"manifest"`
			Wasm *struct {
				SHA256 string `json:"sha256"`
			} `json:"wasm"`
			UI *struct {
				SHA256 string `json:"sha256"`
			} `json:"ui"`
			Permissions []string        `json:"permissions"`
			Yanked      json.RawMessage `json:"yanked"`
		} `json:"versions"`
	} `json:"apps"`
}

// mediaNameRe is an icon's file name at a store: its sha256 and a picture's
// extension.
var mediaNameRe = regexp.MustCompile(`^([0-9a-f]{64})\.(png|jpg|jpeg|webp)$`)

func withdrawn(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	return t != "" && t != "null"
}

// verifyIndex checks the index's bytes against the store's trusted index
// keys: the signature is over the lower-hex sha256 of the bytes as served.
func verifyIndex(body []byte, sig string, keys []Key) error {
	var pubs []ed25519.PublicKey
	for _, k := range keys {
		if k.Use != UseIndex || !k.signs() {
			continue
		}
		pub, err := k.PublicKey()
		if err != nil {
			continue
		}
		pubs = append(pubs, pub)
	}
	// ⚠ Never an empty list: VerifyDetached accepts anything when it is
	// handed no key at all.
	if len(pubs) == 0 {
		return errf(CodeIndexInvalid, "the store is trusted with no index key that signs")
	}
	if err := plugin.VerifyDetached(pubs, sha256Hex(body), strings.TrimSpace(sig)); err != nil {
		return errf(CodeIndexInvalid, "the store's index does not verify with the key it is trusted with: %v", err)
	}
	return nil
}

// project turns a verified index into a catalog.
func project(origin string, doc *indexDoc, now time.Time) *Catalog {
	pubs := map[string]int{}
	for i, p := range doc.Publishers {
		pubs[p.ID] = i
	}
	c := &Catalog{Store: origin, Serial: doc.Serial, FetchedAt: now.UTC(), Apps: []CatalogApp{}, icons: map[string]bool{}}
	for _, a := range doc.Apps {
		if withdrawn(a.Revoked) || (a.Kind != KindApp && a.Kind != KindLanguagePack) {
			continue
		}
		pick := -1
		for i, v := range a.Versions {
			if withdrawn(v.Yanked) {
				continue
			}
			if v.Version == a.Latest {
				pick = i
				break
			}
			if pick < 0 {
				pick = i // newest first: the first one not yanked
			}
		}
		if pick < 0 {
			continue
		}
		v := a.Versions[pick]
		app := CatalogApp{Name: a.Name, Kind: a.Kind, Label: a.Label, Summary: a.Summary, Publisher: a.Publisher,
			Categories: append([]string{}, a.Categories...), Repo: a.Repo, Version: v.Version, PublishedAt: v.PublishedAt,
			FilexRange: v.Filex, Permissions: append([]string{}, v.Permissions...), ManifestSHA256: strings.ToLower(v.Manifest.SHA256)}
		if app.Label == nil {
			app.Label = map[string]string{"en": a.Name}
		}
		if i, ok := pubs[a.Publisher]; ok {
			p := doc.Publishers[i]
			app.Publisher, app.PublisherVerified, app.PublisherOfficial = p.Name, p.Verified, p.Official
		}
		if v.Wasm != nil {
			app.WasmSHA256 = strings.ToLower(v.Wasm.SHA256)
		}
		if v.UI != nil {
			app.UISHA256 = strings.ToLower(v.UI.SHA256)
		}
		if a.Icon != nil {
			if name := iconName(origin, a.Icon.URL, a.Icon.SHA256); name != "" {
				app.Icon = name
				c.icons[name] = true
			}
		}
		c.Apps = append(c.Apps, app)
	}
	sort.SliceStable(c.Apps, func(i, j int) bool { return c.Apps[i].Name < c.Apps[j].Name })
	return c
}

// iconName is an icon's file name when the index points it at the store's
// own media path (`<store>/v1/media/<sha256>.<ext>`), "" otherwise: filex
// fetches media from the store's own address only.
func iconName(origin, rawURL, sha string) string {
	name, ok := strings.CutPrefix(rawURL, origin+"/v1/media/")
	if !ok {
		return ""
	}
	m := mediaNameRe.FindStringSubmatch(name)
	if m == nil || m[1] != strings.ToLower(sha) {
		return ""
	}
	return name
}

// Catalog answers origin's catalog: from the cache while it is fresh,
// otherwise read again and verified. A store that cannot be reached answers
// the last catalog that verified, Stale; without one, the error.
func (s *Service) Catalog(ctx context.Context, origin string) (*Catalog, error) {
	if c, ok := s.catalogs.Get(origin); ok {
		return c, nil
	}
	c, err := s.fetchCatalog(ctx, origin)
	if err == nil {
		s.catalogs.Put(origin, c)
		s.lastGood.Put(origin, c)
		return c, nil
	}
	if e, ok := AsError(err); ok && e.Code == CodeUnreachable {
		if prev, ok := s.lastGood.Get(origin); ok {
			stale := *prev
			stale.Stale = true
			return &stale, nil
		}
	}
	return nil, err
}

// DropCatalog forgets origin's catalog (a store no longer trusted).
func (s *Service) DropCatalog(origin string) {
	s.catalogs.Delete(origin)
	s.lastGood.Delete(origin)
	s.media.DeleteFunc(func(k string, _ []byte) bool { return strings.HasPrefix(k, origin+"/") })
}

func (s *Service) fetchCatalog(ctx context.Context, origin string) (*Catalog, error) {
	t, err := s.trustFor(ctx, origin, true)
	if err != nil {
		return nil, err
	}
	status, body, err := s.opts.Client.doRaw(ctx, http.MethodGet, origin, "/v1/index.json", nil, nil, maxIndexBytes)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &Error{Code: CodeUnreachable, Message: "the store answered " + http.StatusText(status) + " for its index"}
	}
	sstatus, sig, err := s.opts.Client.doRaw(ctx, http.MethodGet, origin, "/v1/index.json.sig", nil, nil, maxSigBytes)
	if err != nil {
		return nil, err
	}
	if sstatus != http.StatusOK {
		return nil, &Error{Code: CodeUnreachable, Message: "the store answered " + http.StatusText(sstatus) + " for its index signature"}
	}
	if err := verifyIndex(body, string(sig), t.Keys); err != nil {
		return nil, err
	}
	var doc indexDoc
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&doc); err != nil {
		return nil, errf(CodeIndexInvalid, "the store's index does not read: %v", err)
	}
	if doc.Schema != 1 {
		return nil, errf(CodeIndexInvalid, "the store's index is schema %d; this filex reads schema 1", doc.Schema)
	}
	now := s.opts.Now().UTC()
	exp, err := time.Parse(time.RFC3339, doc.ExpiresAt)
	if err != nil || !now.Before(exp) {
		return nil, errf(CodeIndexInvalid, "the store's index expired at %s; the store has not signed a new one", doc.ExpiresAt)
	}
	return project(origin, &doc, now), nil
}

// Media answers an icon of origin's catalog: only a file the catalog names,
// fetched from the store's own media path, and only bytes whose sha256 is
// the file's name and that are the picture its extension says.
func (s *Service) Media(ctx context.Context, origin, file string) ([]byte, string, error) {
	m := mediaNameRe.FindStringSubmatch(file)
	if m == nil {
		return nil, "", errf(CodeMediaInvalid, "not an icon's name")
	}
	c, err := s.Catalog(ctx, origin)
	if err != nil {
		return nil, "", err
	}
	if !c.icons[file] {
		return nil, "", errf(CodeMediaInvalid, "the catalog names no such icon")
	}
	ctype := map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "webp": "image/webp"}[m[2]]
	key := origin + "/" + file
	if b, ok := s.media.Get(key); ok {
		return b, ctype, nil
	}
	status, b, err := s.opts.Client.doRaw(ctx, http.MethodGet, origin, "/v1/media/"+file, nil, nil, maxMediaBytes)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", errf(CodeMediaInvalid, "the store answered %d for the icon", status)
	}
	if sha256Hex(b) != m[1] || !isPicture(b, m[2]) {
		return nil, "", errf(CodeMediaInvalid, "the icon's bytes are not the ones its name pins")
	}
	s.media.Put(key, b)
	return b, ctype, nil
}

// isPicture: the bytes start the way a picture of that extension does.
func isPicture(b []byte, ext string) bool {
	switch ext {
	case "png":
		return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "jpg", "jpeg":
		return bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff})
	case "webp":
		return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
	}
	return false
}
