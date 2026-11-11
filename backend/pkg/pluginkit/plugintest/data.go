package plugintest

import (
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The app's own data, routes and the shortener role (filex 0.60.0) ───────
//
// The fake host keeps the app's data in memory with the real host's rules:
// the `data` grant, the scopes (the tenant scope is Host.Tenant, which the
// app cannot change), the size limits, conditional writes answered `busy`,
// and a route visit that may read and count but not write.

type dataRow struct {
	value   string
	rev     int64
	counter bool
}

// EnterRoute makes the following calls behave like a route visit: the app's
// data may be read and counted (DataGet, DataList, DataIncr), nothing else
// written.
func (h *Host) EnterRoute() {
	h.mu.Lock()
	h.writable, h.share, h.route = false, "", true
	h.mu.Unlock()
}

// SetLinkLive says whether one of filex's public links (by token) is still
// open, for LinkLive.
func (h *Host) SetLinkLive(link string, live bool) {
	h.mu.Lock()
	h.links[link] = live
	h.mu.Unlock()
}

// DataKeys lists the keys of a scope ("app", or "tenant" for Host.Tenant),
// for assertions.
func (h *Host) DataKeys(scope string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ns, err := h.dataScope(scope)
	if err != nil {
		return nil
	}
	var out []string
	for k := range h.data[ns] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResetData erases the app's data, as Reconfigure and removing the app do.
func (h *Host) ResetData() {
	h.mu.Lock()
	h.data = map[string]map[string]*dataRow{}
	h.mu.Unlock()
}

func (h *Host) dataScope(scope string) (string, error) {
	switch scope {
	case wire.DataApp:
		return "app", nil
	case wire.DataTenant, "":
		return "t:" + strconv.FormatInt(h.Tenant, 10), nil
	}
	return "", hostErr(wire.ErrInvalid, "scope is tenant or app")
}

func (h *Host) dataKey(key string) error {
	if key == "" || len(key) > wire.DataMaxKeyBytes {
		return hostErr(wire.ErrInvalid, "a key is 1 to 512 printable ASCII characters")
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return hostErr(wire.ErrInvalid, "a key is 1 to 512 printable ASCII characters")
		}
	}
	return nil
}

// DataGet reads one key (data).
func (h *Host) DataGet(scope, key string) (string, int64, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("data"); err != nil {
		return "", 0, false, err
	}
	ns, err := h.dataScope(scope)
	if err != nil {
		return "", 0, false, err
	}
	if err := h.dataKey(key); err != nil {
		return "", 0, false, err
	}
	r := h.data[ns][key]
	if r == nil {
		return "", 0, false, nil
	}
	return r.value, r.rev, true, nil
}

// DataPut writes one key (data; not on a route visit).
func (h *Host) DataPut(scope, key, value string, opt pluginkit.DataPutOptions) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("data"); err != nil {
		return 0, err
	}
	if h.route {
		return 0, hostErr(wire.ErrPermissionDenied, "a route visit may read and count its data, not write it")
	}
	ns, err := h.dataScope(scope)
	if err != nil {
		return 0, err
	}
	if err := h.dataKey(key); err != nil {
		return 0, err
	}
	if len(value) > wire.DataMaxValueBytes {
		return 0, hostErr(wire.ErrTooLarge, "a value is at most 64 KiB")
	}
	if h.data[ns] == nil {
		h.data[ns] = map[string]*dataRow{}
	}
	cur := h.data[ns][key]
	if opt.IfAbsent && cur != nil {
		return 0, hostErr(wire.ErrBusy, "the key exists")
	}
	if opt.IfRev != 0 && (cur == nil || cur.rev != opt.IfRev) {
		return 0, hostErr(wire.ErrBusy, "the key is not at that revision")
	}
	if cur != nil && cur.counter {
		return 0, hostErr(wire.ErrInvalid, "a counter key is written by data_incr only")
	}
	h.dataRev++
	h.data[ns][key] = &dataRow{value: value, rev: h.dataRev}
	return h.dataRev, nil
}

// DataDelete removes one key (data; not on a route visit).
func (h *Host) DataDelete(scope, key string, ifRev int64) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("data"); err != nil {
		return false, err
	}
	if h.route {
		return false, hostErr(wire.ErrPermissionDenied, "a route visit may read and count its data, not write it")
	}
	ns, err := h.dataScope(scope)
	if err != nil {
		return false, err
	}
	cur := h.data[ns][key]
	if cur == nil {
		return false, nil
	}
	if ifRev != 0 && cur.rev != ifRev {
		return false, hostErr(wire.ErrBusy, "the key is not at that revision")
	}
	delete(h.data[ns], key)
	return true, nil
}

// DataList lists keys by prefix, in order (data).
func (h *Host) DataList(scope, prefix, after string, limit int) ([]pluginkit.DataEntry, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("data"); err != nil {
		return nil, "", err
	}
	ns, err := h.dataScope(scope)
	if err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > wire.DataMaxList {
		limit = 100
	}
	var keys []string
	for k := range h.data[ns] {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	next := ""
	if len(keys) > limit {
		keys = keys[:limit]
		next = keys[limit-1]
	}
	out := make([]pluginkit.DataEntry, 0, len(keys))
	for _, k := range keys {
		r := h.data[ns][k]
		out = append(out, pluginkit.DataEntry{Key: k, Value: r.value, Rev: r.rev})
	}
	return out, next, nil
}

// DataIncr adds to a counter key (data; a route visit may).
func (h *Host) DataIncr(scope, key string, by int64) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("data"); err != nil {
		return 0, err
	}
	ns, err := h.dataScope(scope)
	if err != nil {
		return 0, err
	}
	if err := h.dataKey(key); err != nil {
		return 0, err
	}
	if h.data[ns] == nil {
		h.data[ns] = map[string]*dataRow{}
	}
	cur := h.data[ns][key]
	if cur != nil && !cur.counter {
		return 0, hostErr(wire.ErrInvalid, "the key holds a value, not a counter")
	}
	n := by
	if cur != nil {
		n, _ = strconv.ParseInt(cur.value, 10, 64)
		n += by
	}
	h.dataRev++
	h.data[ns][key] = &dataRow{value: strconv.FormatInt(n, 10), rev: h.dataRev, counter: true}
	return n, nil
}

// LinkLive answers whether a public link is open (link_shortener).
func (h *Host) LinkLive(link string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("link_shortener"); err != nil {
		return false, err
	}
	return h.links[link], nil
}

// Route plays one visit to one of the app's routes, after the checks filex
// makes of the manifest, and checks the answer the way filex does.
func (h *Harness) Route(in *wire.RouteInput) (*wire.RouteOutput, error) {
	m := h.Manifest()
	if !hasPermission(m, "public_routes") {
		return nil, errors.New("plugintest: the manifest does not ask for the public_routes permission, so filex would never call route")
	}
	if h.Plugin.Route == nil {
		return nil, errors.New("plugintest: no Route registered, but the manifest declares routes")
	}
	if in.Method == "" {
		in.Method = "GET"
	}
	if in.Locale == "" {
		in.Locale = h.Locale
	}
	if in.Settings == nil {
		in.Settings = h.Host.CallSettings()
	}
	h.Host.EnterRoute()
	out, err := h.Plugin.Route(in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.RouteOutput{Miss: true}
	}
	if err := CheckRouteOutput(out); err != nil {
		return nil, err
	}
	return out, nil
}

// CheckRouteOutput applies filex's rules to a route's answer: exactly one of
// a redirect, a page and a miss; a redirect absolute http(s), no user name
// or password, at most 2048 bytes; a page's link the same.
func CheckRouteOutput(out *wire.RouteOutput) error {
	n := 0
	if out.Redirect != "" {
		n++
		if err := checkRouteURL(out.Redirect); err != nil {
			return errors.New("plugintest: redirect " + err.Error())
		}
	}
	if out.Page != nil {
		n++
		if len(out.Page.Title) == 0 {
			return errors.New("plugintest: a page needs a title")
		}
		if out.Page.Link != "" {
			if err := checkRouteURL(out.Page.Link); err != nil {
				return errors.New("plugintest: page link " + err.Error())
			}
		}
	}
	if out.Miss {
		n++
	}
	if n != 1 {
		return errors.New("plugintest: a route answers exactly one of redirect, page and miss")
	}
	return nil
}

func checkRouteURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("is longer than 2048 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("is not an absolute http(s) address")
	}
	if u.User != nil {
		return errors.New("carries a user name or password")
	}
	return nil
}

// Shorten plays filex asking the app for a short address, as the harness's
// Actor, and checks the answer lies on one of the bases it was handed.
func (h *Harness) Shorten(in *wire.ShortenInput) (*wire.ShortenOutput, error) {
	m := h.Manifest()
	if m.LinkShortener == nil || !hasPermission(m, "link_shortener") {
		return nil, errors.New("plugintest: the manifest has no link_shortener block and permission, so filex would never call shorten")
	}
	if h.Plugin.Shorten == nil {
		return nil, errors.New("plugintest: no Shorten registered, but the manifest has a link_shortener block")
	}
	if in.Actor == nil {
		a := h.Actor
		in.Actor = &a
	}
	if in.Locale == "" {
		in.Locale = h.Locale
	}
	if in.Settings == nil {
		in.Settings = h.Host.CallSettings()
	}
	h.Host.EnterScreen()
	out, err := h.Plugin.Shorten(in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.ShortenOutput{}
	}
	if out.URL != "" {
		ok := false
		for _, b := range in.Bases {
			if strings.HasPrefix(out.URL, strings.TrimRight(b.URL, "/")+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return nil, errors.New("plugintest: shorten answered " + out.URL + ", which lies on none of the bases it was handed")
		}
		if err := checkRouteURL(out.URL); err != nil {
			return nil, errors.New("plugintest: short address " + err.Error())
		}
	}
	return out, nil
}

// Reset plays filex erasing the app's data: the app's Reset is told first,
// then the fake host's data goes, whatever Reset answered.
func (h *Harness) Reset(removing bool) error {
	var err error
	if h.Plugin.Reset != nil {
		h.Host.EnterScreen()
		err = h.Plugin.Reset(&wire.ResetInput{Removing: removing, Settings: h.Host.CallSettings()})
	}
	h.Host.ResetData()
	return err
}
