package wasmplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/netguard"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── asset_fetch: a file the app needs, downloaded once, by its hash ─────
//
// ⚠⚠ Why this exists (2026-09-21, the owner): a signing app has to draw a
// name typed in Japanese, a box filled in Arabic, an audit line in Hindi —
// and the fonts that can are Noto, 10–18 MB for one CJK face. Bundled, they
// would add tens of megabytes to the module and to the compile every
// install pays (~23 s for the 17 MB sign module). "yazı tipini online
// çekemez miyiz? online çekebilirsek 0 MB" — so the app names the file it
// needs by URL AND sha256, and the host downloads it once:
//
//   - through the SAME guarded outbound path http_request uses: the app's
//     own `http:<host>` grant (shown in the install review, so it is never
//     a hidden connection), the dialer that refuses private and loopback
//     addresses, redirects only to granted hosts;
//   - verified against the sha256 the app carries BEFORE anything reads it.
//     A font file is a parser's attack surface: a compromised CDN must not
//     be able to hand an app bytes it did not pin. A mismatch is refused
//     (`integrity`) and nothing is stored;
//   - kept in the app's own cache directory, named by its hash, so every
//     later call — any call, any screen — is served from disk without the
//     network. The app reads it with the ordinary file ABI (`file_open` on
//     the ref), which needs no `files:read`: an asset is the app's own
//     download, not somebody's file.
//
// ⚠ Why not http_request: that call answers in JSON with the body base64-
// encoded and caps it at 8 MiB, because it is for API replies an app holds
// in memory. A CJK face is 10.5 MB (Noto Sans SC Regular) and its variable
// release 17.8 MB. This streams to disk under its own ceiling and hands
// back a handle.
//
// ⚠⚠ The download does not belong to the call that asked for it. A screen
// has 30 seconds (call_timeout_s) and asks for a font while the person is
// still typing; on a slow line a CJK face takes longer than that. Tied to
// the call, the download would be cut at 30 s and started again from zero
// by the next keystroke — forever. So it runs detached, under its own
// budget (assetTimeout), shared by every call that wants the same file;
// a call waits for it only while its own deadline allows, then answers
// `timeout` ("still downloading") and the next call finds it finished.

const (
	// assetMaxBytes is the ceiling for ONE asset. The largest file the
	// signing app pins is Noto Sans SC Regular (10,540,644 bytes); its
	// variable release — the one a future pin may move to — is 17,773,132.
	// 32 MiB leaves room for either without letting an app pull arbitrarily
	// large files onto the server.
	assetMaxBytes = 32 << 20
	// assetCacheMax bounds one app's cache on disk. Past it, the least
	// recently used assets are removed (they are fetched again when needed).
	assetCacheMax = 256 << 20
	// assetTimeout bounds one download, independently of any call.
	assetTimeout = 120 * time.Second
	// assetAnswerMargin is the part of a call's own budget kept for the app
	// to answer after "still downloading": the call must end with the app's
	// screen, not with the host killing it.
	assetAnswerMargin = 3 * time.Second
	// assetRetryAfter: a download that failed is not tried again for this
	// long; the calls in between get the same answer at once.
	//
	// ⚠ Measured 2026-09-21 on an installation with no internet: a screen
	// that asks for a font at every pause in typing asked the network five
	// times per pause (the face, then the shared faces behind it). DNS
	// failing fast made that cheap there; on a network that DROPS packets
	// each attempt is a 10 s dial timeout, and the person would wait that
	// long for the line that tells them their text will not print.
	assetRetryAfter = time.Minute
)

// assetFail is the last failure of one app's asset, kept until the asset
// is fetched. While it is kept the outage is not logged again: it is said
// once, not once per keystroke (the same measurement: 15 identical log
// lines for three pauses in typing).
type assetFail struct {
	err error
	at  time.Time
}

// assetFlight is one download in progress, shared by every call that asks
// for the same asset of the same app until it ends.
type assetFlight struct {
	done chan struct{}
	size int64
	err  error
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// assetDir is one app's cache: <plugins dir>/assets/<app name>. Kept apart
// from the app's own directory so an upgrade — which replaces that one —
// does not throw the downloads away; removed when the app is uninstalled.
func (r *Registry) assetDir(name string) string {
	return filepath.Join(r.opts.Dir, "assets", name)
}

func hfAssetFetch(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		URL      string `json:"url"`
		SHA256   string `json:"sha256"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	sum := strings.ToLower(strings.TrimSpace(req.SHA256))
	if !sha256Re.MatchString(sum) {
		return nil, hostErr(wire.ErrInvalid, "sha256 must be 64 hex characters: an asset is always pinned")
	}
	if req.MaxBytes <= 0 || req.MaxBytes > assetMaxBytes {
		return nil, hostErr(wire.ErrInvalid, fmt.Sprintf("max_bytes must be 1..%d", assetMaxBytes))
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	// https only: the hash guards the bytes, TLS guards who learns what an
	// installation asked for and keeps a network in the middle from
	// answering with garbage that costs a download to refuse.
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, hostErr(wire.ErrInvalid, "url must be https://host/…")
	}
	if !s.plugin.Grants.HasHost(u.Hostname()) {
		return nil, hostErr(wire.ErrPermissionDenied, "plugin was not granted http:"+u.Hostname())
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && netguard.Refused(ip) {
		return nil, hostErr(wire.ErrPermissionDenied, "private and local addresses are refused")
	}

	dir := s.reg.assetDir(s.plugin.Row.Name)
	final := filepath.Join(dir, sum)
	if st, err := os.Stat(final); err == nil && st.Mode().IsRegular() {
		// Served from the cache: no network. The mtime is the LRU clock.
		now := time.Now()
		_ = os.Chtimes(final, now, now)
		return map[string]any{"ref": s.addAsset(sum, final, st.Size()), "size": st.Size(), "cached": true}, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, hostErr(wire.ErrUnavailable, "asset cache: "+err.Error())
	}
	if err := s.reg.recentAssetFailure(s.plugin.Row.Name + "/" + sum); err != nil {
		return nil, err
	}
	fl := s.reg.assetFlight(s.plugin, u, sum, req.MaxBytes, final)
	wait := assetTimeout
	if dl, ok := ctx.Deadline(); ok {
		wait = time.Until(dl) - assetAnswerMargin
	}
	if wait <= 0 {
		return nil, hostErr(wire.ErrTimeout, "still downloading; no time left in this call to wait for it")
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-fl.done:
	case <-timer.C:
		return nil, hostErr(wire.ErrTimeout, "still downloading; ask again in a moment")
	case <-ctx.Done():
		return nil, hostErr(wire.ErrTimeout, "still downloading; ask again in a moment")
	}
	if fl.err != nil {
		return nil, fl.err
	}
	return map[string]any{"ref": s.addAsset(sum, final, fl.size), "size": fl.size, "cached": false}, nil
}

// assetFlight joins the download of one app's asset, starting it when none
// is running. It runs on its own context (assetTimeout), not the caller's:
// see the note at the top of this file.
func (r *Registry) assetFlight(p *Installed, u *url.URL, sum string, max int64, final string) *assetFlight {
	key := p.Row.Name + "/" + sum
	r.assetMu.Lock()
	defer r.assetMu.Unlock()
	if fl, ok := r.assetFlights[key]; ok {
		return fl
	}
	if r.assetFlights == nil {
		r.assetFlights = map[string]*assetFlight{}
	}
	fl := &assetFlight{done: make(chan struct{})}
	r.assetFlights[key] = fl
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), assetTimeout)
		defer cancel()
		fl.size, fl.err = r.downloadAsset(ctx, p, u, sum, max, final)
		r.assetMu.Lock()
		_, wasDown := r.assetFails[key]
		if fl.err == nil {
			delete(r.assetFails, key)
		} else {
			if r.assetFails == nil {
				r.assetFails = map[string]*assetFail{}
			}
			r.assetFails[key] = &assetFail{err: fl.err, at: time.Now()}
		}
		r.assetMu.Unlock()
		switch {
		case fl.err == nil:
			// ⚠ Said in the app's log, where an administrator looks: this is
			// the one moment an installation talks to the network on an
			// app's behalf.
			p.log("info", "asset fetched: "+u.String()+" ("+strconv.FormatInt(fl.size, 10)+" bytes, sha256 verified)")
			r.trimAssets(filepath.Dir(final), final)
		case wasDown:
			// Still down: said when it started, not again.
		case asHostError(fl.err).Code == wire.ErrIntegrity:
			p.log("warn", "asset refused: "+u.String()+" does not match its pinned sha256")
		default:
			p.log("info", "asset could not be fetched: "+u.String()+" ("+clip(fl.err.Error(), 200)+"); asked again after "+assetRetryAfter.String()+", and not logged again until it arrives")
		}
		// A finished flight is forgotten: the file is on disk now, or the
		// failure above answers the calls of the next minute.
		r.assetMu.Lock()
		delete(r.assetFlights, key)
		r.assetMu.Unlock()
		close(fl.done)
	}()
	return fl
}

// recentAssetFailure is the failure of the last download of key, while it
// is younger than assetRetryAfter.
func (r *Registry) recentAssetFailure(key string) error {
	r.assetMu.Lock()
	defer r.assetMu.Unlock()
	if f, ok := r.assetFails[key]; ok && time.Since(f.at) < assetRetryAfter {
		return f.err
	}
	return nil
}

// downloadAsset streams url into a temporary file beside `final`, checks the
// size and the hash, and renames it into place only when both hold.
func (r *Registry) downloadAsset(ctx context.Context, p *Installed, u *url.URL, sum string, max int64, final string) (int64, error) {
	dctx, cancel := context.WithTimeout(ctx, assetTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(dctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, hostErr(wire.ErrInvalid, err.Error())
	}
	hr.Header.Set("User-Agent", "filex-app/"+p.Row.Name+" ("+HostVersion+")")
	client := &http.Client{
		Transport: r.outbound,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= httpMaxRedirect {
				return errors.New("too many redirects")
			}
			if next.URL.Scheme != "https" || !p.Grants.HasHost(next.URL.Hostname()) {
				return errors.New("redirect to " + next.URL.Hostname() + " is not in the plugin's allowed hosts")
			}
			return nil
		},
	}
	resp, err := client.Do(hr)
	if err != nil {
		if dctx.Err() != nil {
			return 0, hostErr(wire.ErrTimeout, "the download exceeded its time budget")
		}
		return 0, hostErr(wire.ErrUnavailable, clip(err.Error(), 300))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, hostErr(wire.ErrUnavailable, fmt.Sprintf("the server answered %d", resp.StatusCode))
	}
	if resp.ContentLength > max {
		return 0, hostErr(wire.ErrTooLarge, "the asset is larger than max_bytes")
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), "."+sum[:12]+".part-*")
	if err != nil {
		return 0, hostErr(wire.ErrUnavailable, "asset cache: "+err.Error())
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, max+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if dctx.Err() != nil {
			return 0, hostErr(wire.ErrTimeout, "the download exceeded its time budget")
		}
		return 0, hostErr(wire.ErrUnavailable, "download: "+clip(err.Error(), 300))
	}
	if n > max {
		return 0, hostErr(wire.ErrTooLarge, "the asset is larger than max_bytes")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return 0, hostErr(wire.ErrIntegrity, "the downloaded file does not match its pinned sha256 (got "+got[:16]+"…)")
	}
	// Atomic: a concurrent call for the same asset either finds nothing or
	// finds the whole, verified file — never half of it.
	if err := os.Rename(tmpName, final); err != nil {
		return 0, hostErr(wire.ErrUnavailable, "asset cache: "+err.Error())
	}
	keep = true
	return n, nil
}

// trimAssets keeps one app's cache under assetCacheMax, removing the least
// recently used assets first and never the one just stored.
func (r *Registry) trimAssets(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		size int64
		at   time.Time
	}
	var items []item
	var total int64
	for _, e := range entries {
		if !sha256Re.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		items = append(items, item{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for _, it := range items {
		if total <= assetCacheMax {
			return
		}
		if it.path == keep {
			continue
		}
		if os.Remove(it.path) == nil {
			total -= it.size
		}
	}
}

// addAsset registers a cached asset in the call's scope so the app can read
// it with file_open. It is neither an input (Inputs() leaves it out) nor an
// output (it can never be committed to a storage).
func (s *Scope) addAsset(sum, p string, size int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := "asset:" + strconv.Itoa(len(s.order))
	s.files[ref] = &scopeFile{Ref: ref, Name: sum, Size: size, Path: p, Asset: true}
	s.order = append(s.order, ref)
	return ref
}
