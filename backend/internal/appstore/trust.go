package appstore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

// Trust sources.
const (
	SourceAdmin  = "admin"  // an administrator trusted it on first use
	SourceConfig = "config" // FILEX_APP_STORE_URLS + FILEX_APP_STORE_KEYS
)

// Trust is a store filex accepts install links and license answers from, and
// the keys it accepts them signed with - the keys the administrator SAW when
// they trusted it (or the operator configured). A key the store publishes
// later is not among them until somebody approves it again.
type Trust struct {
	Origin         string    `json:"origin"`
	Keys           []Key     `json:"keys"`
	Source         string    `json:"source"`
	ApprovedBy     *int64    `json:"approved_by,omitempty"`
	ApprovedByName string    `json:"approved_by_name,omitempty"`
	ApprovedAt     time.Time `json:"approved_at"`
	// UpdatedAt: when the key list last narrowed by itself (a key retired or
	// gone at the store) or a status moved (next → active).
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// KeyView is a key as the panel shows it.
type KeyView struct {
	ID          string `json:"id"`
	Use         string `json:"use"`
	Status      string `json:"status,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

func viewsOf(keys []Key) []KeyView {
	out := make([]KeyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, KeyView{ID: k.ID, Use: k.Use, Status: k.Status, Fingerprint: k.Fingerprint()})
	}
	return out
}

// TrustView is a trusted store as the panel lists it.
type TrustView struct {
	Origin         string    `json:"origin"`
	Source         string    `json:"source"`
	Keys           []KeyView `json:"keys"`
	ApprovedByName string    `json:"approved_by_name,omitempty"`
	ApprovedAt     time.Time `json:"approved_at,omitempty"`
}

// trustRequired is the refusal that asks an administrator to trust a store:
// its address and the keys it publishes, with fingerprints - what the panel
// shows and what the approval must name back.
func trustRequired(code, origin string, keys []Key, previous []Key) *Error {
	msg := "this store is not trusted yet: an administrator compares its key fingerprints with what the store publishes and trusts it"
	if code == CodeKeyChanged {
		msg = "this store's keys changed since it was trusted: compare the new fingerprints with what the store publishes and trust it again"
	}
	d := map[string]any{"store": origin, "keys": viewsOf(keys), "fingerprints": Fingerprints(keys)}
	if previous != nil {
		d["previous_keys"] = viewsOf(previous)
	}
	return &Error{Code: code, Message: msg, Detail: d}
}

// adminTrust reads the stored trust of origin; nil when there is none.
func (s *Service) adminTrust(ctx context.Context, origin string) (*Trust, error) {
	var t Trust
	ok, err := s.getJSON(ctx, keyTrustPrefix+origin, &t)
	if err != nil || !ok {
		return nil, err
	}
	return &t, nil
}

// ListTrust is every trusted store: configured ones first, then the ones an
// administrator trusted.
func (s *Service) ListTrust(ctx context.Context) ([]TrustView, error) {
	rows, err := s.opts.Store.ListAppStoreState(ctx, keyTrustPrefix)
	if err != nil {
		return nil, err
	}
	out := []TrustView{}
	seen := map[string]bool{}
	cfg := make([]string, 0, len(s.cfgStores))
	for o := range s.cfgStores {
		cfg = append(cfg, o)
	}
	sort.Strings(cfg)
	for _, o := range cfg {
		v := TrustView{Origin: o, Source: SourceConfig, Keys: []KeyView{}}
		var t Trust
		if raw, ok := rows[keyTrustPrefix+o]; ok && unmarshal(raw, &t) == nil && t.Source == SourceConfig {
			v.Keys = viewsOf(t.Keys)
		}
		out = append(out, v)
		seen[o] = true
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var t Trust
		if unmarshal(rows[k], &t) != nil || seen[t.Origin] || t.Source != SourceAdmin || s.refusedByList(t.Origin) != nil {
			continue
		}
		out = append(out, TrustView{Origin: t.Origin, Source: t.Source, Keys: viewsOf(t.Keys), ApprovedByName: t.ApprovedByName, ApprovedAt: t.ApprovedAt})
	}
	return out, nil
}

// Approve trusts origin with the keys it publishes NOW, provided they are
// the keys the administrator was shown (fingerprints, as Fingerprints
// spells them): a store whose keys changed while the page was open is asked
// about again, never trusted with keys nobody saw.
//
// A configured store is not the administrator's to trust or distrust.
func (s *Service) Approve(ctx context.Context, origin string, fingerprints []string, actorID *int64, actorName string) (*TrustView, error) {
	if s.cfgStores[origin] {
		return nil, errf(CodeBadStore, "%s is trusted by configuration (FILEX_APP_STORE_URLS); its keys are FILEX_APP_STORE_KEYS", origin)
	}
	if err := s.refusedByList(origin); err != nil {
		return nil, err
	}
	ks, err := s.opts.Client.Keys(ctx, origin)
	if err != nil {
		return nil, err
	}
	keys := ks.TrustedKeys()
	if len(keys) == 0 {
		return nil, errf(CodeBadAnswer, "the store publishes no key that signs install links or licenses")
	}
	want := append([]string(nil), fingerprints...)
	sort.Strings(want)
	if !slices.Equal(want, Fingerprints(keys)) {
		prev, _ := s.adminTrust(ctx, origin)
		var before []Key
		if prev != nil {
			before = prev.Keys
		}
		e := trustRequired(CodeKeyChanged, origin, keys, before)
		e.Message = "the store's keys are not the ones you were shown; compare these and approve again"
		return nil, e
	}
	prev, _ := s.adminTrust(ctx, origin)
	t := &Trust{Origin: origin, Keys: keys, Source: SourceAdmin, ApprovedBy: actorID, ApprovedByName: actorName, ApprovedAt: time.Now().UTC()}
	if err := s.putJSON(ctx, keyTrustPrefix+origin, t); err != nil {
		return nil, err
	}
	s.noteBuildKeys(ctx, origin, ks)
	// A catalog read under the keys before is read again under these.
	s.DropCatalog(origin)
	meta := map[string]any{"store": origin, "fingerprints": Fingerprints(keys)}
	if prev != nil {
		meta["previous_fingerprints"] = Fingerprints(prev.Keys)
	}
	s.audit(ctx, actorID, "app_store.trust", "app_store", origin, meta)
	return &TrustView{Origin: origin, Source: SourceAdmin, Keys: viewsOf(keys), ApprovedByName: actorName, ApprovedAt: t.ApprovedAt}, nil
}

// Remove stops trusting a store an administrator trusted. Its install links
// are refused from then on, and the licenses it issued are not checked
// against it any more (they hold until their grace ends).
func (s *Service) Remove(ctx context.Context, origin string, actorID *int64) (bool, error) {
	if s.cfgStores[origin] {
		return false, errf(CodeBadStore, "%s is trusted by configuration (FILEX_APP_STORE_URLS)", origin)
	}
	ok, err := s.opts.Store.DeleteAppStoreState(ctx, keyTrustPrefix+origin)
	if err != nil {
		return false, err
	}
	if ok {
		s.DropCatalog(origin)
		s.audit(ctx, actorID, "app_store.untrust", "app_store", origin, map[string]any{"store": origin})
	}
	return ok, nil
}

// trustFor answers the keys origin's answers are checked against, after
// reading what the store publishes now:
//
//   - not trusted (and not configured): CodeTrustRequired, with the keys to
//     show the administrator;
//   - trusted, and the store now publishes a key that is not pinned (a new
//     id, or new material under an old id): CodeKeyChanged - widening the
//     trust is an administrator's decision;
//   - a pinned key the store retired or no longer lists is dropped, and a
//     status that moved (next → active) is taken: narrowing needs nobody;
//   - a configured store: its keys are the published ones whose material
//     FILEX_APP_STORE_KEYS lists, and nothing else, ever.
//
// When the store cannot be reached, the pinned keys stand (refresh=false
// answers them without asking).
func (s *Service) trustFor(ctx context.Context, origin string, refresh bool) (*Trust, error) {
	if s.cfgStores[origin] {
		return s.configTrust(ctx, origin, refresh)
	}
	// ⚠ Before anything is read from the store: with FILEX_APP_STORE_URLS set,
	// another store is not asked for its keys, its links or its licenses - not
	// even one an administrator trusted before the list was set.
	if err := s.refusedByList(origin); err != nil {
		return nil, err
	}
	t, err := s.adminTrust(ctx, origin)
	if err != nil {
		return nil, err
	}
	if t == nil {
		if !refresh {
			return nil, errf(CodeTrustRequired, "the store %s is not trusted", origin)
		}
		ks, kerr := s.opts.Client.Keys(ctx, origin)
		if kerr != nil {
			return nil, kerr
		}
		return nil, trustRequired(CodeTrustRequired, origin, ks.TrustedKeys(), nil)
	}
	if !refresh {
		return t, nil
	}
	ks, kerr := s.opts.Client.Keys(ctx, origin)
	if kerr != nil {
		if e, ok := AsError(kerr); ok && e.Code == CodeUnreachable {
			return t, nil
		}
		return nil, kerr
	}
	fresh := ks.TrustedKeys()
	pinned := map[string]Key{}
	for _, k := range t.Keys {
		pinned[k.ID] = k
	}
	for _, k := range fresh {
		p, ok := pinned[k.ID]
		if !ok || p.Use != k.Use || !sameMaterial(p, k) {
			return nil, trustRequired(CodeKeyChanged, origin, fresh, t.Keys)
		}
	}
	s.noteBuildKeys(ctx, origin, ks)
	// Narrow: keep the pinned keys the store still publishes (not retired),
	// with the status it gives them now.
	kept := []Key{}
	byID := map[string]Key{}
	for _, k := range fresh {
		byID[k.ID] = k
	}
	changed := false
	for _, p := range t.Keys {
		k, ok := byID[p.ID]
		if !ok {
			changed = true
			continue
		}
		if k.Status != p.Status {
			p.Status = k.Status
			changed = true
		}
		kept = append(kept, p)
	}
	if changed {
		t.Keys = kept
		t.UpdatedAt = time.Now().UTC()
		if err := s.putJSON(ctx, keyTrustPrefix+origin, t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// configTrust is a configured store's trust: the published keys whose
// material FILEX_APP_STORE_KEYS lists (for the use it lists), kept so that a
// license check made while the store's keys.json cannot be read still knows
// them.
func (s *Service) configTrust(ctx context.Context, origin string, refresh bool) (*Trust, error) {
	var stored Trust
	have, err := s.getJSON(ctx, keyTrustPrefix+origin, &stored)
	if err != nil {
		return nil, err
	}
	if have && stored.Source != SourceConfig {
		have = false
	}
	if !refresh && have {
		return &stored, nil
	}
	ks, kerr := s.opts.Client.Keys(ctx, origin)
	if kerr != nil {
		if e, ok := AsError(kerr); ok && e.Code == CodeUnreachable && have {
			return &stored, nil
		}
		return nil, kerr
	}
	keys := []Key{}
	for _, k := range ks.TrustedKeys() {
		if s.configAllows(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		e := &Error{Code: CodeKeyNotConfigured,
			Message: "the store " + origin + " is configured (FILEX_APP_STORE_URLS) but none of the keys it publishes is in FILEX_APP_STORE_KEYS",
			Detail:  map[string]any{"store": origin, "keys": viewsOf(ks.TrustedKeys())}}
		return nil, e
	}
	s.noteBuildKeys(ctx, origin, ks)
	t := &Trust{Origin: origin, Keys: keys, Source: SourceConfig, ApprovedAt: time.Now().UTC()}
	if !have || !slices.Equal(Fingerprints(stored.Keys), Fingerprints(keys)) || !sameStatuses(stored.Keys, keys) {
		if err := s.putJSON(ctx, keyTrustPrefix+origin, t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (s *Service) configAllows(k Key) bool {
	f := k.Fingerprint()
	for _, ck := range s.cfgKeys {
		if ck.fpr == f && (ck.use == "" || ck.use == k.Use) {
			return true
		}
	}
	return false
}

func sameMaterial(a, b Key) bool {
	fa, fb := a.Fingerprint(), b.Fingerprint()
	return fa != "" && fa == fb
}

func sameStatuses(a, b []Key) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]string{}
	for _, k := range a {
		m[k.ID] = k.Status
	}
	for _, k := range b {
		if st, ok := m[k.ID]; !ok || st != k.Status {
			return false
		}
	}
	return true
}

// refusedByList refuses origin when FILEX_APP_STORE_URLS is set and does not
// name it (nil otherwise).
func (s *Service) refusedByList(origin string) error {
	if !s.allowList || s.cfgStores[origin] {
		return nil
	}
	return errf(CodeStoreRefused, "this filex takes install links and licenses only from the stores FILEX_APP_STORE_URLS lists, and %s is not one of them", origin)
}

// TrustStatus answers whether origin is trusted, for the panel, without
// asking the store: "config", "admin" or "".
func (s *Service) TrustStatus(ctx context.Context, origin string) string {
	if s.cfgStores[origin] {
		return SourceConfig
	}
	if s.refusedByList(origin) != nil {
		return ""
	}
	if t, err := s.adminTrust(ctx, origin); err == nil && t != nil {
		return SourceAdmin
	}
	return ""
}

// verifyWith checks env against origin's trust for use, refreshing the trust
// first. A key the trust does not know is answered as the trust's own
// verdict (CodeKeyChanged when the store published a new one).
func (s *Service) verifyWith(ctx context.Context, origin string, env *Envelope, use string, refresh bool) (*Trust, error) {
	t, err := s.trustFor(ctx, origin, refresh)
	if err != nil {
		return nil, err
	}
	if err := Verify(env, t.Keys, use); err != nil {
		switch {
		case errors.Is(err, ErrUnknownKey) && t.Source == SourceConfig:
			return nil, errf(CodeKeyNotConfigured, "the store signed with key %q, which FILEX_APP_STORE_KEYS does not list", env.KeyID)
		default:
			return nil, &Error{Code: CodeSignature, Message: err.Error()}
		}
	}
	return t, nil
}

func unmarshal(raw string, v any) error {
	return json.Unmarshal([]byte(strings.TrimSpace(raw)), v)
}
