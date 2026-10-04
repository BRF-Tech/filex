package assoc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Store is the slice of db.Store the service reads and writes.
type Store interface {
	ListFileAssociations(ctx context.Context) ([]*model.FileAssociation, error)
	PutFileAssociation(ctx context.Context, a *model.FileAssociation) error
	DeleteFileAssociation(ctx context.Context, capability, ext string) (bool, error)
}

// Source lists the app handlers that exist now: the running apps' interfaces
// that open files, and the running apps that draw thumbnails. The app
// registry implements it (internal/wasmplugin). Each list is in the order the
// default puts it in: by app name, then the app's own order.
type Source interface {
	OpenHandlers() []AppHandler
	ThumbnailHandlers() []AppHandler
}

// rulesTTL is how long the rules read from the database are trusted before
// they are read again. A change made here drops them at once (Put, Reset);
// the interval only matters to a second server on the same database.
const rulesTTL = 10 * time.Second

// Service answers the chains and keeps the rules.
type Service struct {
	store Store

	mu  sync.RWMutex
	src Source
	// builtinThumb reports whether filex draws a file of this name and type
	// itself (thumb.BuiltinDraws). Nil: it draws nothing.
	builtinThumb func(name, mime string) bool
	// ooThumb reports whether the document server draws a file of this name
	// and type now: OnlyOffice is configured and it is a kind it draws. Nil:
	// it draws nothing.
	ooThumb func(name, mime string) bool
	// ooOpen reports whether the document server opens a file of this name
	// and type now, as one choice among others: OnlyOffice is configured and
	// it is a kind of OnlyOfficeOpens (filex 0.51). Nil: it opens nothing here.
	ooOpen func(name, mime string) bool

	rulesMu sync.Mutex
	rules   map[string]map[string]*Rule
	loaded  time.Time
	now     func() time.Time
}

// New builds the service over store. The source and the built-in drawer are
// attached once they exist (SetSource, SetBuiltinThumb).
func New(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// SetSource attaches (or replaces) the app handler source.
func (s *Service) SetSource(src Source) {
	s.mu.Lock()
	s.src = src
	s.mu.Unlock()
}

// SetBuiltinThumb attaches the answer to "does filex draw this file itself".
func (s *Service) SetBuiltinThumb(f func(name, mime string) bool) {
	s.mu.Lock()
	s.builtinThumb = f
	s.mu.Unlock()
}

// SetOnlyOfficeThumb attaches the answer to "does the document server draw
// this file now" (OnlyOffice configured, and a kind it draws). Read on every
// chain, so it must answer from memory.
func (s *Service) SetOnlyOfficeThumb(f func(name, mime string) bool) {
	s.mu.Lock()
	s.ooThumb = f
	s.mu.Unlock()
}

// SetOnlyOfficeOpen attaches the answer to "does the document server open
// this file now, as one choice among others" (OnlyOffice configured, and a
// kind of OnlyOfficeOpens). Read on every open chain, so it must answer from
// memory.
func (s *Service) SetOnlyOfficeOpen(f func(name, mime string) bool) {
	s.mu.Lock()
	s.ooOpen = f
	s.mu.Unlock()
}

// OnlyOfficeOpen reports whether the document server opens a file of this
// name and type now, as one choice among others.
func (s *Service) OnlyOfficeOpen(name, mime string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	f := s.ooOpen
	s.mu.RUnlock()
	return f != nil && OnlyOfficeOpens(ExtOf(name)) && f(name, mime)
}

// OnlyOfficeThumb reports whether the document server draws a file of this
// name and type now.
func (s *Service) OnlyOfficeThumb(name, mime string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	f := s.ooThumb
	s.mu.RUnlock()
	return f != nil && f(name, mime)
}

// BuiltinThumb reports whether filex draws a file of this name and type.
func (s *Service) BuiltinThumb(name, mime string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	f := s.builtinThumb
	s.mu.RUnlock()
	return f != nil && f(name, mime)
}

// appHandlers is the source's list for a capability, sorted by app name.
func (s *Service) appHandlers(capability string) []AppHandler {
	s.mu.RLock()
	src := s.src
	s.mu.RUnlock()
	if src == nil {
		return nil
	}
	var hs []AppHandler
	if capability == CapOpen {
		hs = src.OpenHandlers()
	} else {
		hs = src.ThumbnailHandlers()
	}
	out := append([]AppHandler(nil), hs...)
	sortHandlers(out)
	return out
}

// Forget drops the rules read from the database; the next question reads
// them again.
func (s *Service) Forget() {
	s.rulesMu.Lock()
	s.rules = nil
	s.rulesMu.Unlock()
}

// load returns the rules, reading them when they are older than rulesTTL. A
// read that fails keeps what was there (or none): a database hiccup must not
// make every kind fall back to the default order and every thumbnail stale.
func (s *Service) load(ctx context.Context) map[string]map[string]*Rule {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	if s.rules != nil && s.now().Sub(s.loaded) < rulesTTL {
		return s.rules
	}
	if s.store == nil {
		s.rules = map[string]map[string]*Rule{}
		s.loaded = s.now()
		return s.rules
	}
	rows, err := s.store.ListFileAssociations(ctx)
	if err != nil {
		if s.rules == nil {
			return map[string]map[string]*Rule{}
		}
		return s.rules
	}
	next := map[string]map[string]*Rule{CapOpen: {}, CapThumbnail: {}}
	for _, row := range rows {
		if !ValidCapability(row.Capability) {
			continue
		}
		r := Clean(Rule{Order: row.Handlers, Off: row.Off})
		next[row.Capability][row.Ext] = &r
	}
	s.rules, s.loaded = next, s.now()
	return s.rules
}

// Rule is the administrator's rule for a kind and a capability, nil for none.
func (s *Service) Rule(ctx context.Context, capability, ext string) *Rule {
	if s == nil || ext == "" {
		return nil
	}
	if r := s.load(ctx)[capability][ext]; r != nil {
		cp := *r
		return &cp
	}
	return nil
}

// Rules is every rule of one capability, by kind.
func (s *Service) Rules(ctx context.Context, capability string) map[string]Rule {
	out := map[string]Rule{}
	if s == nil {
		return out
	}
	for ext, r := range s.load(ctx)[capability] {
		out[ext] = *r
	}
	return out
}

// Available is every handler that could do the capability for a file of this
// name and type, in the default order - before any rule.
func (s *Service) Available(capability, name, mime string) []Handler {
	ext := ExtOf(name)
	var apps []Handler
	for _, a := range s.appHandlers(capability) {
		if a.Matches(ext, mime) {
			apps = append(apps, a.Handler)
		}
	}
	if capability == CapThumbnail {
		return ThumbDefault(s.OnlyOfficeThumb(name, mime), s.BuiltinThumb(name, mime), apps)
	}
	return OpenDefault(s.OnlyOfficeOpen(name, mime), apps)
}

// Chain is the handlers of the capability for a file of this name and type:
// the available ones (Available), ordered and switched off by the rule for
// the file's kind.
//
// ⚠ The thumbnail pipeline asks this for every file it draws and every row a
// listing assesses, so it reads only memory: the source's list (a handful of
// apps) and the cached rules.
func (s *Service) Chain(ctx context.Context, capability, name, mime string) Chain {
	if s == nil {
		return Chain{}
	}
	avail := s.Available(capability, name, mime)
	r := s.Rule(ctx, capability, ExtOf(name))
	on, off := Apply(avail, r)
	return Chain{On: on, Off: off, Custom: r != nil}
}

// OpenAllowed reports whether the open handler id may open a file of this
// name: false only when the administrator switched it off for that kind. It
// does not judge whether the handler opens the kind at all - the view's own
// rule does that.
func (s *Service) OpenAllowed(ctx context.Context, name, id string) bool {
	r := s.Rule(ctx, CapOpen, ExtOf(name))
	if r == nil {
		return true
	}
	for _, off := range r.Off {
		if off == id {
			return false
		}
	}
	return true
}

// ErrInvalid is a rule a caller may not store; the message says why.
type ErrInvalid struct{ Message string }

func (e *ErrInvalid) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ErrInvalid{Message: fmt.Sprintf(format, args...)}
}

// Put stores the administrator's rule for one kind and capability, after
// checking that it names only handlers the kind has: an id that is not one
// of them is refused rather than kept for later, so what the screen shows is
// what is stored.
func (s *Service) Put(ctx context.Context, capability, ext string, r Rule, by *int64) (*Rule, error) {
	if err := s.Check(capability, ext, r); err != nil {
		return nil, err
	}
	clean := Clean(r)
	if err := s.store.PutFileAssociation(ctx, &model.FileAssociation{
		Capability: capability, Ext: ext, Handlers: clean.Order, Off: clean.Off, UpdatedBy: by,
	}); err != nil {
		return nil, err
	}
	s.Forget()
	return &clean, nil
}

// Check refuses a rule Put would refuse, without storing anything: an
// unknown capability or kind, an id of the wrong shape, or a handler the kind
// does not have here.
func (s *Service) Check(capability, ext string, r Rule) error {
	if !ValidCapability(capability) {
		return invalid("capability %q must be open or thumbnail", capability)
	}
	if !ValidExt(ext) {
		return invalid("%q is not a kind of file (an extension, lower-case, no dot)", ext)
	}
	clean := Clean(r)
	avail := map[string]bool{}
	for _, h := range s.Available(capability, "file."+ext, MimeOf(ext)) {
		avail[h.ID] = true
	}
	for _, id := range append(append([]string{}, clean.Order...), clean.Off...) {
		if !ValidID(capability, id) {
			return invalid("%q is not a %s handler", id, capability)
		}
		if !avail[id] {
			return invalid("%q does not %s .%s files here", id, verbOf(capability), ext)
		}
	}
	return nil
}

// PruneApp drops an app's handlers from every rule (the app was removed): the
// others keep their order, and a rule left with nothing in it goes.
func (s *Service) PruneApp(ctx context.Context, app string) error {
	if s == nil || s.store == nil || app == "" {
		return nil
	}
	rows, err := s.store.ListFileAssociations(ctx)
	if err != nil {
		return err
	}
	keep := func(ids []string) ([]string, bool) {
		out := []string{}
		changed := false
		for _, id := range ids {
			if AppOf(id) == app {
				changed = true
				continue
			}
			out = append(out, id)
		}
		return out, changed
	}
	for _, row := range rows {
		order, c1 := keep(row.Handlers)
		off, c2 := keep(row.Off)
		if !c1 && !c2 {
			continue
		}
		if len(order) == 0 && len(off) == 0 {
			if _, err := s.store.DeleteFileAssociation(ctx, row.Capability, row.Ext); err != nil {
				return err
			}
			continue
		}
		row.Handlers, row.Off = order, off
		if err := s.store.PutFileAssociation(ctx, row); err != nil {
			return err
		}
	}
	s.Forget()
	return nil
}

// Reset removes the rule: the kind keeps the default order again. ok = there
// was one.
func (s *Service) Reset(ctx context.Context, capability, ext string) (bool, error) {
	if !ValidCapability(capability) || !ValidExt(ext) {
		return false, invalid("no such kind or capability")
	}
	ok, err := s.store.DeleteFileAssociation(ctx, capability, ext)
	s.Forget()
	return ok, err
}

func verbOf(capability string) string {
	if capability == CapOpen {
		return "open"
	}
	return "draw thumbnails of"
}

// Place is where an install puts a new app's handler for one kind.
type Place string

// Places an install may choose.
const (
	PlaceFirst Place = "first"
	PlaceLast  Place = "last"
	PlaceOff   Place = "off"
)

// Placement is one choice an administrator made at install.
type Placement struct {
	Capability string `json:"capability"`
	Ext        string `json:"ext"`
	// Handler is the new app's handler for that capability.
	Handler string `json:"handler"`
	Place   Place  `json:"place"`
}

// Place writes the install-time choice for one kind: the new handler first,
// after the ones already there, or off. The order the kind has now (its
// rule, or the default) is written out whole, so the choice does not move
// anybody else.
func (s *Service) Place(ctx context.Context, p Placement, by *int64) error {
	if !ValidCapability(p.Capability) || !ValidExt(p.Ext) || !ValidID(p.Capability, p.Handler) {
		return invalid("placement %s .%s %s is not well formed", p.Capability, p.Ext, p.Handler)
	}
	chain := s.Chain(ctx, p.Capability, "file."+p.Ext, MimeOf(p.Ext))
	others := []string{}
	for _, h := range chain.On {
		if h.ID != p.Handler {
			others = append(others, h.ID)
		}
	}
	off := []string{}
	for _, h := range chain.Off {
		if h.ID != p.Handler {
			off = append(off, h.ID)
		}
	}
	var r Rule
	switch p.Place {
	case PlaceFirst:
		r = Rule{Order: append([]string{p.Handler}, others...), Off: off}
	case PlaceLast:
		r = Rule{Order: append(others, p.Handler), Off: off}
	case PlaceOff:
		r = Rule{Order: others, Off: append(off, p.Handler)}
	default:
		return invalid("place %q must be first, last or off", p.Place)
	}
	_, err := s.Put(ctx, p.Capability, p.Ext, r, by)
	return err
}

// ── the kinds the administrator's screen lists ───────────────────────────

// KindCap is one capability of one kind, as the screen draws it.
type KindCap struct {
	On     []Handler `json:"on"`
	Off    []Handler `json:"off"`
	Custom bool      `json:"custom"`
	Rule   *Rule     `json:"rule,omitempty"`
}

// Kind is one row of Admin → Plugins → Default apps.
type Kind struct {
	Ext       string  `json:"ext"`
	Mime      string  `json:"mime,omitempty"`
	Open      KindCap `json:"open"`
	Thumbnail KindCap `json:"thumbnail"`
}

// Kinds lists every kind an app handles (its extensions, and the known
// extensions of its media types), every kind the document server opens as a
// choice while OnlyOffice is configured (`.csv`, filex 0.51) and every kind
// with a rule, sorted.
//
// ⚠ A kind only filex handles is not listed: there is nothing to choose
// between. Its rule, once written, keeps it on the list.
func (s *Service) Kinds(ctx context.Context) []Kind {
	exts := map[string]bool{}
	for _, capability := range []string{CapOpen, CapThumbnail} {
		for _, a := range s.appHandlers(capability) {
			for _, e := range a.Ext {
				if ValidExt(e) {
					exts[e] = true
				}
			}
			for _, m := range a.Mime {
				for _, e := range ExtsOfMime(m) {
					exts[e] = true
				}
			}
		}
		for e := range s.load(ctx)[capability] {
			exts[e] = true
		}
	}
	for _, e := range onlyOfficeOpenKinds {
		if s.OnlyOfficeOpen("file."+e, MimeOf(e)) {
			exts[e] = true
		}
	}
	list := make([]string, 0, len(exts))
	for e := range exts {
		list = append(list, e)
	}
	sort.Strings(list)
	out := make([]Kind, 0, len(list))
	for _, e := range list {
		name, mime := "file."+e, MimeOf(e)
		k := Kind{Ext: e, Mime: mime}
		for _, capability := range []string{CapOpen, CapThumbnail} {
			ch := s.Chain(ctx, capability, name, mime)
			kc := KindCap{On: nonNil(ch.On), Off: nonNil(ch.Off), Custom: ch.Custom, Rule: s.Rule(ctx, capability, e)}
			if capability == CapOpen {
				k.Open = kc
			} else {
				k.Thumbnail = kc
			}
		}
		out = append(out, k)
	}
	return out
}

func nonNil(hs []Handler) []Handler {
	if hs == nil {
		return []Handler{}
	}
	return hs
}

// ── an app drawing a thumbnail ───────────────────────────────────────────

// DrawRequest is one file an app is asked to draw. The app is handed Body,
// Name, Mime and Size; NodeID, StorageID and Path are for the host's own
// record (the audit row of a call that sent the file out) and never reach
// the app.
type DrawRequest struct {
	NodeID    int64
	StorageID int64
	Path      string
	Name      string
	Mime      string
	Size      int64
	Body      io.Reader
}

// Draw error codes (DrawError.Code).
const (
	DrawFailed   = "failed"
	DrawTimeout  = "timeout"
	DrawTooLarge = "too_large"
)

// DrawError is why an app did not draw a file. Limit is the limit that was
// hit: bytes for too_large, milliseconds for timeout.
type DrawError struct {
	App   string
	Code  string
	Limit int64
	Err   error
}

func (e *DrawError) Error() string {
	if e.Err != nil {
		return "app " + e.App + ": " + e.Code + ": " + e.Err.Error()
	}
	return "app " + e.App + ": " + e.Code
}

func (e *DrawError) Unwrap() error { return e.Err }

// AsDrawError returns err as a *DrawError, or a failed one for any other
// error.
func AsDrawError(app string, err error) *DrawError {
	var de *DrawError
	if errors.As(err, &de) {
		return de
	}
	return &DrawError{App: app, Code: DrawFailed, Err: err}
}

// ── what an install review shows ─────────────────────────────────────────

// InstallKind is one row of the install review's File types group: a kind
// the new app would open or draw thumbnails of, who handles it now, and where
// the app lands when the administrator chooses nothing.
type InstallKind struct {
	Capability string `json:"capability"`
	Ext        string `json:"ext"`
	Mime       string `json:"mime,omitempty"`
	// Handler is the new app's handler for the kind.
	Handler Handler `json:"handler"`
	// Current are the handlers on for the kind now, in order.
	Current []Handler `json:"current"`
	// Default is where the app lands with no choice: first, or after the
	// others (a kind with a rule keeps its named handlers in front).
	Default Place `json:"default"`
}

// InstallKinds lists the rows of the File types group for an app that is not
// installed yet: its open handlers (one per view) and its thumbnail handler,
// each kind they name - their extensions, and the known extensions of their
// media types.
func (s *Service) InstallKinds(ctx context.Context, open, thumb []AppHandler) []InstallKind {
	out := []InstallKind{}
	for _, c := range []struct {
		capability string
		hs         []AppHandler
	}{{CapOpen, open}, {CapThumbnail, thumb}} {
		for _, h := range c.hs {
			for _, e := range kindsOf(h) {
				name, mime := "file."+e, MimeOf(e)
				current := s.Chain(ctx, c.capability, name, mime).On
				out = append(out, InstallKind{
					Capability: c.capability, Ext: e, Mime: mime, Handler: h.Handler,
					Current: nonNil(current), Default: s.defaultPlace(ctx, c.capability, e, h),
				})
			}
		}
	}
	return out
}

// defaultPlace is where h would land for the kind with no choice made: the
// chain as it would be with h among the available handlers.
func (s *Service) defaultPlace(ctx context.Context, capability, ext string, h AppHandler) Place {
	name, mime := "file."+ext, MimeOf(ext)
	apps := []AppHandler{h}
	for _, a := range s.appHandlers(capability) {
		if a.ID != h.ID && a.Matches(ext, mime) {
			apps = append(apps, a)
		}
	}
	sortHandlers(apps)
	hs := make([]Handler, len(apps))
	for i, a := range apps {
		hs[i] = a.Handler
	}
	order := OpenDefault(s.OnlyOfficeOpen(name, mime), hs)
	if capability == CapThumbnail {
		order = ThumbDefault(s.OnlyOfficeThumb(name, mime), s.BuiltinThumb(name, mime), hs)
	}
	on, _ := Apply(order, s.Rule(ctx, capability, ext))
	if len(on) > 0 && on[0].ID == h.ID {
		return PlaceFirst
	}
	return PlaceLast
}
