// Package handlers — login_security.go
//
// The administrator's side of the sign-in attempt limit (internal/loginguard):
// its settings, the locks in force, and the recent wrong attempts. Frozen
// contract for the admin SPA and for /api/ai/admin (and its MCP tools):
//
//	GET   /api/admin/login-security
//	      → {settings, limits, trusted_proxies_effective, trusted_proxies_source,
//	         trusted_defaults:{auto, loopback, private, link_local},
//	         trusted_addresses, trusted_proxies_auto:{in_use, environment,
//	         runtime, networks, excluded_gateways, excluded_self, interfaces,
//	         warning, warning_detail, resolved_at}, untrusted_forwarders:
//	         [{address, first_seen, last_seen, count, public, relay}],
//	         untrusted_forwarders_total, your_ip, your_ip_allowlisted}
//	PATCH /api/admin/login-security   partial body, same keys as `settings`
//	      → the GET answer (400 {error:"invalid_setting", field, message} on a
//	        bad value; nothing is written unless every field is valid)
//	GET   /api/admin/login-security/locks?scope=&locked=1&limit=
//	      → {items:[{id, scope, subject, fails, limit, lock_level, locked,
//	         locked_until, retry_after, window_start, last_fail_at, last_ip,
//	         last_protocol}], now}
//	POST  /api/admin/login-security/unlock   {scope, subject} | {all:true}
//	      → {ok, unlocked}
//	GET   /api/admin/login-security/attempts?action=&from=&to=&limit=&offset=
//	      → {items:[{id, action, identifier, ip, protocol, reason, scope, via,
//	         changed_fields, at, metadata}], total}
//
// On a public demo (DemoMode) every address a visitor or the operator wrote is
// "hidden on the demo" in all of these answers (demo_redact.go); your_ip, the
// reader's own, is not.
//
// # Instance-wide
//
// Every value here is one global row, and a lock or an address is not a
// tenant's own: the whole surface is the platform operator's
// (requireSupertenant, asked INSIDE each method because /api/ai/admin and the
// MCP tools mount these same handler instances).
//
// # Memory first
//
// With a running limiter (Guard) every answer here is the limiter's memory —
// the settings and the counters it decides with — and every change goes
// through it: a setting is written to the table and put in force at once, an
// unlock lifts the lock in memory and deletes the row. Without one (a store
// with no limiter) the table is read and written directly.
//
// # What is never here
//
// No password, ever: the counters hold an identifier and an address, the audit
// rows hold the same plus the door and the reason.
package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
)

const loginSecurityInstanceWide = "sign-in security applies to the whole instance and is managed by the platform operator"

// LoginSecurity handles /api/admin/login-security.
type LoginSecurity struct {
	Store db.Store
	// Guard is the running limiter; its settings cache is dropped after a
	// write so the next attempt sees the new numbers.
	Guard *loginguard.Guard
	// EnvTrustedProxies is FILEX_TRUSTED_PROXIES, shown as the source of the
	// trusted-proxy list when the setting is empty.
	EnvTrustedProxies string
	// DemoMode marks a public playground, where these pages are readable by
	// whoever read the published credentials: the visitors' addresses (the
	// locks, the trail) and the operator's (the allow-list, the proxies) are
	// masked - demo_redact.go. On a normal install nothing is.
	DemoMode bool
}

// maxForwardersShown bounds the untrusted forwarders one answer carries (the
// busiest first); untrusted_forwarders_total says how many are remembered.
const maxForwardersShown = 50

// NewLoginSecurity constructs the handler.
func NewLoginSecurity(store db.Store, guard *loginguard.Guard, envTrustedProxies string) *LoginSecurity {
	return &LoginSecurity{Store: store, Guard: guard, EnvTrustedProxies: envTrustedProxies}
}

type loginSecuritySettings struct {
	Enabled         bool     `json:"enabled"`
	AccountMaxFails int      `json:"account_max_fails"`
	IPMaxFails      int      `json:"ip_max_fails"`
	WindowSeconds   int      `json:"window_seconds"`
	LockBaseSeconds int      `json:"lock_base_seconds"`
	LockMaxSeconds  int      `json:"lock_max_seconds"`
	IPAllowlist     []string `json:"ip_allowlist"`
	// TrustedProxies is the `login.trusted_proxies` setting as stored — the
	// entries an administrator typed (addresses, CIDR networks, the words
	// `auto`, `loopback`, `private`, `link-local`, `none`). Empty = unset; see
	// trusted_proxies_effective for what is in force.
	TrustedProxies []string `json:"trusted_proxies"`
}

// trustedAutoView is what `auto` resolves to (clientip.AutoResolution) and
// whether the list in force uses it.
type trustedAutoView struct {
	clientip.AutoResolution
	InUse bool `json:"in_use"`
}

// untrustedForwarder is a peer that sent a forwarded address without being a
// trusted proxy, with what the page needs to word its "trust it?" question:
// Public (not loopback, private or link-local), Relay (an address `auto`
// carves out because relayed connections arrive from it: a gateway, or
// filex's own address).
type untrustedForwarder struct {
	clientip.Forwarder
	Public bool `json:"public"`
	Relay  bool `json:"relay"`
}

type loginSecurityLimit struct {
	Min     int `json:"min"`
	Max     int `json:"max"`
	Default int `json:"default"`
}

type loginSecurityResponse struct {
	Settings loginSecuritySettings         `json:"settings"`
	Limits   map[string]loginSecurityLimit `json:"limits"`
	// TrustedProxiesEffective is the list in force with `auto` spelled out:
	// the words of the address classes it takes (loopback, private,
	// link-local) first, then the container networks `auto` resolved to, then
	// its own addresses and networks - never trusted inside them:
	// trusted_proxies_auto.excluded_gateways and excluded_self, when `auto`
	// is in use.
	// TrustedProxiesSource says where it came from: "setting", "env" or
	// "auto" (neither is set: the default).
	TrustedProxiesEffective []string `json:"trusted_proxies_effective"`
	TrustedProxiesSource    string   `json:"trusted_proxies_source"`
	// TrustedDefaults says, one switch each, which words the list in force
	// takes (auto and the three classes); TrustedAddresses is the rest of it -
	// the addresses and networks written out.
	TrustedDefaults  clientip.Classes `json:"trusted_defaults"`
	TrustedAddresses []string         `json:"trusted_addresses"`
	// TrustedProxiesAuto is what `auto` resolves to right now, and why -
	// answered whether or not the list in force uses it (in_use).
	TrustedProxiesAuto trustedAutoView `json:"trusted_proxies_auto"`
	// UntrustedForwarders are peers that sent X-Forwarded-For / X-Real-IP
	// without being trusted (the busiest first, at most maxForwardersShown);
	// UntrustedForwardersTotal is how many are remembered.
	UntrustedForwarders      []untrustedForwarder `json:"untrusted_forwarders"`
	UntrustedForwardersTotal int                  `json:"untrusted_forwarders_total"`
	// YourIP is the address filex sees THIS request coming from — what to put
	// on the allow-list to keep oneself able to sign in.
	YourIP            string `json:"your_ip"`
	YourIPAllowlisted bool   `json:"your_ip_allowlisted"`
}

func splitList(text string) []string {
	out := []string{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	}) {
		out = append(out, f)
	}
	return out
}

// settings answers the numbers in force and where the stored text is read
// from: the limiter's memory when one runs, else the table.
func (h *LoginSecurity) settings(r *http.Request) (loginguard.Config, dbsetting.Getter) {
	ctx := r.Context()
	if h.Guard != nil {
		return h.Guard.Config(ctx), h.Guard.Stored(ctx)
	}
	return loginguard.LoadConfig(ctx, h.Store), h.Store
}

func (h *LoginSecurity) view(r *http.Request) loginSecurityResponse {
	ctx := r.Context()
	cfg, src := h.settings(r)
	rawAllow, _ := loginguard.IPAllowlistSetting.ResolveStrict(ctx, src)
	rawProxy, _ := loginguard.TrustedProxiesSetting.ResolveStrict(ctx, src)

	resp := loginSecurityResponse{
		Settings: loginSecuritySettings{
			Enabled:         cfg.Enabled,
			AccountMaxFails: cfg.AccountMax,
			IPMaxFails:      cfg.IPMax,
			WindowSeconds:   int(cfg.Window / time.Second),
			LockBaseSeconds: int(cfg.LockBase / time.Second),
			LockMaxSeconds:  int(cfg.LockMax / time.Second),
			IPAllowlist:     splitList(rawAllow),
			TrustedProxies:  splitList(rawProxy),
		},
		Limits: map[string]loginSecurityLimit{},
	}
	for key, s := range map[string]dbsetting.IntSpec{
		"account_max_fails": loginguard.AccountMaxSetting, "ip_max_fails": loginguard.IPMaxSetting,
		"window_seconds": loginguard.WindowSetting, "lock_base_seconds": loginguard.LockBaseSetting,
		"lock_max_seconds": loginguard.LockMaxSetting,
	} {
		resp.Limits[key] = loginSecurityLimit{Min: s.Min, Max: s.Max, Default: s.Default}
	}
	effective, source := clientip.DefaultSet(), "auto"
	if set, _ := clientip.ParseList(rawProxy); set != nil {
		effective, source = set, "setting"
	} else if envSet, _ := clientip.ParseList(h.EnvTrustedProxies); envSet != nil {
		effective, source = envSet, "env"
	}
	resp.TrustedProxiesEffective, resp.TrustedProxiesSource = effective.Effective(), source
	resp.TrustedDefaults, resp.TrustedAddresses = effective.Classes(), effective.Addresses()
	if resp.TrustedProxiesEffective == nil {
		resp.TrustedProxiesEffective = []string{}
	}
	if resp.TrustedAddresses == nil {
		resp.TrustedAddresses = []string{}
	}
	resp.TrustedProxiesAuto = trustedAutoView{AutoResolution: clientip.Auto(), InUse: effective.Classes().Auto}
	resp.UntrustedForwarders, resp.UntrustedForwardersTotal = h.forwarders()
	resp.YourIP = clientIP(r)
	resp.YourIPAllowlisted = cfg.Allow.ContainsString(resp.YourIP)
	if h.DemoMode {
		h.maskViewForDemo(&resp)
	}
	return resp
}

// forwarders answers the untrusted forwarders remembered (clientip.Observe),
// the busiest first, and how many there are.
func (h *LoginSecurity) forwarders() ([]untrustedForwarder, int) {
	all := clientip.UntrustedForwarders()
	out := make([]untrustedForwarder, 0, min(len(all), maxForwardersShown))
	for _, f := range all {
		if len(out) == maxForwardersShown {
			break
		}
		v := untrustedForwarder{Forwarder: f}
		if a, err := netip.ParseAddr(f.Address); err == nil {
			a = a.Unmap()
			v.Public = !(a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast())
			v.Relay = clientip.IsAutoRelay(a)
		}
		out = append(out, v)
	}
	return out, len(all)
}

// maskProxyAutoForDemo hides, on a public demo, the addresses this answer's
// trusted-proxy detail carries: the untrusted forwarders are OTHER people's
// (every visitor whose own proxy adds X-Forwarded-For shows up there), and
// the networks, gateways and interface addresses `auto` found are the
// operator's own layout. What kind of address each forwarder is (public,
// relay), how often it was seen, and why `auto` decided what it did stay.
// The demo's GET view calls it (the one place that masks that answer).
func maskProxyAutoForDemo(resp *loginSecurityResponse) {
	for i := range resp.UntrustedForwarders {
		resp.UntrustedForwarders[i].Address = demoMaskedIP
	}
	a := &resp.TrustedProxiesAuto
	a.Networks = maskedAll(a.Networks)
	a.ExcludedGateways = maskedAll(a.ExcludedGateways)
	a.ExcludedSelf = maskedAll(a.ExcludedSelf)
	ifaces := make([]clientip.AutoInterface, len(a.Interfaces))
	for i, ifc := range a.Interfaces {
		ifc.Addresses = maskedAll(ifc.Addresses)
		ifaces[i] = ifc
	}
	a.Interfaces = ifaces
	// The detail is an error's own words (a netlink or /proc failure): no
	// address is expected in it, and none is let through if one is.
	a.WarningDetail = maskAddressesInText(a.WarningDetail)
}

// maskedAll is a new list as long as list, every entry demoMaskedIP (the
// clientip answer it came from is shared and is not written to).
func maskedAll(list []string) []string {
	out := make([]string, len(list))
	for i := range out {
		out[i] = demoMaskedIP
	}
	return out
}

// Get returns the settings, the bounds the form should enforce, and the
// trusted-proxy list in force.
func (h *LoginSecurity) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, loginSecurityInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.view(r))
}

func badSetting(w http.ResponseWriter, field string, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error": "invalid_setting", "field": field, "message": err.Error(),
	})
}

// listField reads a list that arrives as a JSON array of strings or as one
// text (comma / space / newline separated).
func listField(raw json.RawMessage) ([]string, error) {
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		out := []string{}
		for _, e := range arr {
			out = append(out, splitList(e)...)
		}
		return out, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, errors.New("expected a list of addresses or networks")
	}
	return splitList(text), nil
}

// Patch validates every field it was given and, only when all are valid,
// writes them. It answers what Get does.
func (h *LoginSecurity) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, loginSecurityInstanceWide) {
		return
	}
	var req struct {
		Enabled         *bool           `json:"enabled"`
		AccountMaxFails *int            `json:"account_max_fails"`
		IPMaxFails      *int            `json:"ip_max_fails"`
		WindowSeconds   *int            `json:"window_seconds"`
		LockBaseSeconds *int            `json:"lock_base_seconds"`
		LockMaxSeconds  *int            `json:"lock_max_seconds"`
		IPAllowlist     json.RawMessage `json:"ip_allowlist"`
		TrustedProxies  json.RawMessage `json:"trusted_proxies"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ctx := r.Context()

	type write struct{ key, value, field string }
	var writes []write
	changed := []string{}

	for _, f := range []struct {
		field string
		v     *int
		spec  dbsetting.IntSpec
	}{
		{"account_max_fails", req.AccountMaxFails, loginguard.AccountMaxSetting},
		{"ip_max_fails", req.IPMaxFails, loginguard.IPMaxSetting},
		{"window_seconds", req.WindowSeconds, loginguard.WindowSetting},
		{"lock_base_seconds", req.LockBaseSeconds, loginguard.LockBaseSetting},
		{"lock_max_seconds", req.LockMaxSeconds, loginguard.LockMaxSetting},
	} {
		if f.v == nil {
			continue
		}
		if err := f.spec.Validate(*f.v); err != nil {
			badSetting(w, f.field, err)
			return
		}
		writes = append(writes, write{f.spec.Key, strconv.Itoa(*f.v), f.field})
	}
	if req.Enabled != nil {
		writes = append(writes, write{loginguard.KeyEnabled, dbsetting.FormatBool(*req.Enabled), "enabled"})
	}

	// A ceiling below the first lock would make "escalating" mean "shrinking".
	cur, _ := h.settings(r)
	base, ceil := int(cur.LockBase/time.Second), int(cur.LockMax/time.Second)
	if req.LockBaseSeconds != nil {
		base = *req.LockBaseSeconds
	}
	if req.LockMaxSeconds != nil {
		ceil = *req.LockMaxSeconds
	}
	if ceil < base {
		badSetting(w, "lock_max_seconds", fmt.Errorf("the longest lock (%d s) cannot be shorter than the first lock (%d s)", ceil, base))
		return
	}

	if req.IPAllowlist != nil {
		entries, err := listField(req.IPAllowlist)
		if err != nil {
			badSetting(w, "ip_allowlist", err)
			return
		}
		set, err := loginguard.ParseAllowlist(strings.Join(entries, " "))
		if err != nil {
			badSetting(w, "ip_allowlist", err)
			return
		}
		writes = append(writes, write{loginguard.KeyIPAllowlist, strings.Join(set.Strings(), ", "), "ip_allowlist"})
	}
	if req.TrustedProxies != nil {
		entries, err := listField(req.TrustedProxies)
		if err != nil {
			badSetting(w, "trusted_proxies", err)
			return
		}
		joined := strings.Join(entries, ", ")
		if _, err := clientip.ParseList(joined); err != nil {
			badSetting(w, "trusted_proxies", err)
			return
		}
		writes = append(writes, write{loginguard.KeyTrustedProxy, joined, "trusted_proxies"})
	}

	// Through the limiter: each value is written to the table and put in force
	// at once. A write the table refuses stops here (500); the ones before it
	// are written AND in force, exactly as the table says.
	save := func(key, value string) error {
		if h.Guard != nil {
			return h.Guard.SaveSetting(ctx, key, value)
		}
		return h.Store.UpsertSetting(ctx, key, value)
	}
	for _, wr := range writes {
		if err := save(wr.key, wr.value); err != nil {
			if len(changed) > 0 {
				auth.AddAuditDetail(ctx, "changed_fields", changed)
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		changed = append(changed, wr.field)
		// Names and values only — nothing here is a secret. The allow-list and
		// the proxy list are addresses; the rest are numbers and a switch.
		auth.AddAuditDetail(ctx, wr.field, wr.value)
	}
	if len(changed) > 0 {
		auth.AddAuditDetail(ctx, "changed_fields", changed)
	}
	writeJSON(w, http.StatusOK, h.view(r))
}

type loginLockItem struct {
	// ID names the row for the page (its row key, its actions): stable while
	// the server runs, unique per counter, and telling nothing about the
	// subject - on a demo two masked addresses read alike and must still be
	// two rows (loginLockID).
	ID           string     `json:"id"`
	Scope        string     `json:"scope"`
	Subject      string     `json:"subject"`
	Fails        int        `json:"fails"`
	Limit        int        `json:"limit"`
	LockLevel    int        `json:"lock_level"`
	Locked       bool       `json:"locked"`
	LockedUntil  *time.Time `json:"locked_until,omitempty"`
	RetryAfter   int        `json:"retry_after"`
	WindowStart  time.Time  `json:"window_start"`
	LastFailAt   time.Time  `json:"last_fail_at"`
	LastIP       string     `json:"last_ip,omitempty"`
	LastProtocol string     `json:"last_protocol,omitempty"`
}

// Locks lists the counters: who is being counted, who is locked, until when.
// ?locked=1 keeps only the locks in force; ?scope=account|ip narrows.
func (h *LoginSecurity) Locks(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, loginSecurityInstanceWide) {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	scope := q.Get("scope")
	if scope != "" && scope != model.LoginThrottleAccount && scope != model.LoginThrottleIP {
		badSetting(w, "scope", errors.New("scope must be account or ip"))
		return
	}
	limit := 200
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 1000 {
		limit = n
	}
	// Listing is also when a lock that has run out is noticed and its release
	// audited, so what an administrator reads and what the trail says agree.
	if h.Guard != nil {
		h.Guard.Sweep(ctx)
	}
	now := time.Now().UTC()
	if h.Guard != nil && h.Guard.Now != nil {
		now = h.Guard.Now().UTC()
	}
	var lockedAt *time.Time
	if v := q.Get("locked"); v == "1" || strings.EqualFold(v, "true") {
		lockedAt = &now
	}
	// The limiter's memory is what decides; the table is its copy.
	var rows []*model.LoginThrottle
	if h.Guard != nil {
		rows = h.Guard.List(ctx, scope, lockedAt, limit)
	} else {
		var err error
		if rows, err = h.Store.ListLoginThrottles(ctx, scope, lockedAt, limit); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	cfg, _ := h.settings(r)
	var dm *demoMask
	if h.DemoMode {
		dm = newDemoMask(ctx, h.Store)
	}
	items := make([]loginLockItem, 0, len(rows))
	for _, t := range rows {
		it := loginLockItem{
			ID:    loginLockID(t.Scope, t.Subject),
			Scope: t.Scope, Subject: t.Subject, Fails: t.Fails, LockLevel: t.LockLevel,
			WindowStart: t.WindowStart, LastFailAt: t.LastFailAt, LastIP: t.LastIP, LastProtocol: t.LastProtocol,
			Limit: cfg.AccountMax,
		}
		if t.Scope == model.LoginThrottleIP {
			it.Limit = cfg.IPMax
		}
		if t.Locked(now) {
			it.Locked, it.LockedUntil = true, t.LockedUntil
			it.RetryAfter = loginguard.RetryAfterSeconds(t.LockedUntil.Sub(now))
		}
		dm.lock(&it)
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "now": now})
}

// Unlock lifts one lock — and clears its counter and its escalation — or, with
// {"all":true}, every lock in force. An address that is being blocked is
// unlocked by the address (scope "ip"), an account by the identifier as typed
// (scope "account"; it is normalized the way the limiter does).
func (h *LoginSecurity) Unlock(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, loginSecurityInstanceWide) {
		return
	}
	var req struct {
		Scope   string `json:"scope"`
		Subject string `json:"subject"`
		All     bool   `json:"all"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ctx := r.Context()
	if h.Guard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sign-in limit is not running"})
		return
	}
	if req.All {
		n := h.Guard.UnlockAll(ctx)
		auth.AddAuditDetail(ctx, "all", true)
		auth.AddAuditDetail(ctx, "reason", "admin")
		auth.AddAuditDetail(ctx, "unlocked", n)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unlocked": n})
		return
	}
	subject := ""
	switch req.Scope {
	case model.LoginThrottleAccount:
		if strings.TrimSpace(req.Subject) == "" {
			badSetting(w, "subject", errors.New("name the account identifier to unlock"))
			return
		}
		subject = loginguard.Subject(req.Subject)
	case model.LoginThrottleIP:
		a, err := parseAddrText(req.Subject)
		if err != nil {
			badSetting(w, "subject", err)
			return
		}
		subject = a
	default:
		badSetting(w, "scope", errors.New("scope must be account or ip"))
		return
	}
	ok, err := h.Guard.Unlock(ctx, req.Scope, subject)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, subject, subject)
	auth.AddAuditDetail(ctx, "scope", req.Scope)
	auth.AddAuditDetail(ctx, "subject", subject)
	auth.AddAuditDetail(ctx, "reason", "admin")
	n := 0
	if ok {
		n = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unlocked": n})
}

// lockIDKey keys loginLockID: random, made once per process, so an id cannot
// be worked back to the address it names (an IPv4 address is one of 2^32 - an
// unkeyed hash of it is no secret at all).
var lockIDKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("login_security: no randomness for the lock ids: " + err.Error())
	}
	return k
}()

// loginLockID is a counter's row id on the locks list.
func loginLockID(scope, subject string) string {
	m := hmac.New(sha256.New, lockIDKey)
	m.Write([]byte(scope))
	m.Write([]byte{0})
	m.Write([]byte(subject))
	return hex.EncodeToString(m.Sum(nil)[:12])
}

// parseAddrText canonicalises an address the way the limiter stores it.
func parseAddrText(s string) (string, error) {
	set, err := loginguard.ParseAllowlist(s)
	if err != nil || set == nil {
		return "", errors.New("expected an IP address")
	}
	list := set.Strings()
	if len(list) != 1 || strings.Contains(list[0], "/") {
		return "", errors.New("expected one IP address")
	}
	return list[0], nil
}

type loginAttemptItem struct {
	ID         int64  `json:"id"`
	Action     string `json:"action"`
	Identifier string `json:"identifier,omitempty"`
	IP         string `json:"ip,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Scope      string `json:"scope,omitempty"`
	// Via is the door an ADMINISTRATOR's row came through - "panel" (a
	// session), "api" (an admin API key), "mcp" (an MCP tool) - for an unlock
	// by an administrator and a settings change; "" for the limiter's own rows,
	// whose door is Protocol.
	Via string `json:"via,omitempty"`
	// ChangedFields names the settings a login_security.update changed.
	ChangedFields []string       `json:"changed_fields,omitempty"`
	At            time.Time      `json:"at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// loginTrailActions is every action the trail serves, and loginTrailFilter the
// audit filter that reads them all (db.AuditActionPrefixes): the limiter's
// `login.` family and the settings changes.
var (
	loginTrailActions = []string{
		loginguard.ActionFailed, loginguard.ActionLocked, loginguard.ActionUnlocked,
		loginguard.ActionAllowlistPass, loginguard.ActionSettingsUpdate,
	}
	loginTrailFilter = "login.,login_security."
)

// adminDoor answers the door an administrator's audit row came through: the
// `via` the token doors stamp (auth.ViaAPI, auth.ViaMCP), "api" for a token
// row that predates the stamp, "panel" for a session's.
func adminDoor(meta map[string]any) string {
	if v, _ := meta["via"].(string); v != "" {
		return v
	}
	if _, ok := meta["token_id"]; ok {
		return auth.ViaAPI
	}
	return "panel"
}

// Attempts lists the sign-in trail: every wrong attempt, lock, release,
// allow-list pass and settings change, newest first. It is the audit log
// filtered to `login.` and `login_security.` - one name for each event
// whichever door it came through (auth.DoorAction), the door in `via`;
// ?action= narrows to one of them.
func (h *LoginSecurity) Attempts(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, loginSecurityInstanceWide) {
		return
	}
	q := r.URL.Query()
	action := q.Get("action")
	if action == "" {
		action = loginTrailFilter
	} else if !slices.Contains(loginTrailActions, action) {
		badSetting(w, "action", errors.New("action must be one of "+strings.Join(loginTrailActions, ", ")))
		return
	}
	var from, to *time.Time
	if t, err := time.Parse(time.RFC3339, q.Get("from")); err == nil {
		from = &t
	}
	if t, err := time.Parse(time.RFC3339, q.Get("to")); err == nil {
		to = &t
	}
	limit := 50
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	offset := 0
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n >= 0 {
		offset = n
	}
	rows, total, err := h.Store.ListAuditFiltered(r.Context(), nil, action, from, to, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var dm *demoMask
	if h.DemoMode {
		dm = newDemoMask(r.Context(), h.Store)
	}
	items := make([]loginAttemptItem, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.Entry == nil {
			continue
		}
		dm.auditRow(row.Entry)
		items = append(items, loginAttemptOf(row.Entry))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// loginAttemptOf reads one audit row as a trail item.
//
// The limiter's rows name the attempt: who was typed, from where, through
// which door. An administrator's rows - an unlock, a settings change - name
// what was acted on instead: the account or the address that was unlocked is
// the item's Identifier or IP (the administrator's own address stays on the
// audit row, for the Audit page), and Via says which door they used.
func loginAttemptOf(e *model.AuditEntry) loginAttemptItem {
	str := func(k string) string { s, _ := e.Metadata[k].(string); return s }
	it := loginAttemptItem{
		ID: e.ID, Action: e.Action, Identifier: str("identifier"), IP: e.IP,
		Protocol: str("protocol"), Reason: str("reason"), Scope: str("scope"),
		At: e.CreatedAt, Metadata: e.Metadata,
	}
	switch {
	case e.Action == loginguard.ActionSettingsUpdate:
		it.Identifier, it.IP, it.Via = "", "", adminDoor(e.Metadata)
		it.ChangedFields = stringList(e.Metadata["changed_fields"])
	case e.Action == loginguard.ActionUnlocked && it.Reason == "admin":
		it.Identifier, it.IP, it.Via = "", "", adminDoor(e.Metadata)
		if it.Scope == model.LoginThrottleIP {
			it.IP = str("subject")
		} else {
			it.Identifier = str("subject")
		}
	}
	return it
}

// stringList reads a JSON list of strings as stored in audit metadata
// ([]any after a round trip through the database, []string before).
func stringList(v any) []string {
	switch l := v.(type) {
	case []string:
		return append([]string(nil), l...)
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
