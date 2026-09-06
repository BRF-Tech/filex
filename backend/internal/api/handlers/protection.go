// Package handlers — protection.go ("Koru" v0.4)
//
// Admin protection-settings surface, frozen contract:
//
//	GET   /api/admin/protection → {"trash_retention_days":30,"versions_keep_n":0,
//	                               "share_max_ttl_days":7,"shares_over_max_ttl":0,
//	                               "antivirus":{ … see protectionAntivirusStatus }}
//	PATCH /api/admin/protection → {trash_retention_days?, versions_keep_n?,
//	                               share_max_ttl_days?, av_save_scan_window_minutes?,
//	                               av_max_scan_mb?, av_enabled?, av_mode?,
//	                               av_clamd_addr?}
//
// The values live in the settings table (`trash.retention_days`,
// `versions.keep_n`, `share.max_ttl_days`, `antivirus.enabled`,
// `antivirus.mode`, `antivirus.clamd_addr`,
// `antivirus.save_scan_window_minutes`, `antivirus.max_scan_mb`).
// `shares_over_max_ttl` is a read-only count of EXISTING live links that
// outlive the ceiling — they are reported, never shortened (see
// share.CountOverMaxTTL).
//
// # The antivirus block: STATUS fields and SETTING fields
//
// They are separated on purpose, because they can disagree and the
// disagreement is the interesting part. A SETTING is what the settings table
// holds — what an admin last saved. A STATUS field is what this process is
// actually doing. `scan_enabled` can be true while `enabled` is false
// (switched on, nothing to reach), and both can be true while `reachable` is
// false (clamd configured, clamd down).
//
// ⚠⚠ `av_enabled`, `av_mode` and `av_clamd_addr` take effect AT THE NEXT
// RESTART, in both directions, because the scan pipeline is wired once at boot
// (internal/server registers the queue handler and hands the upload surfaces
// an enqueue function). The response says so twice: the settings are echoed
// back immediately, and `restart_pending` is true for as long as the stored
// configuration differs from what this process booted with. The two live
// settings — the save-scan window and the size ceiling — are read at the point
// of use and are never pending.
//
// ⚠ The scanner BINARY is still not admin-writable, and for the original
// reason: it is a path this server EXECUTES, so a writable field for it would
// turn an admin account into arbitrary command execution. A clamd ADDRESS is
// a different thing — a dial target, never executed — and is writable, with
// its shape validated at save time (antivirus.AddrSetting.Check). It does let
// an admin point scanning at an arbitrary TCP endpoint, which is worth stating
// plainly, but it is the same authority an admin already has when configuring
// a storage backend's endpoint.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// Validation bounds for PATCH (frozen contract): retention 1-3650 days,
// keep_n 0-1000 (0 = unlimited / retention job off).
const (
	protRetentionMin = 1
	protRetentionMax = 3650
	protKeepNMin     = 0
	protKeepNMax     = 1000
)

// protectionIsInstanceWide is the refusal an admin of a tenant reads. Every
// value on this page is a single global row: switching antivirus off, or
// pointing clamd somewhere else, does it for every tenant on the instance —
// which is why the surface belongs to the platform operator and not to
// whoever happens to be an admin of one customer.
//
// ⚠ The READ is gated as well as the write, and not out of tidiness: the
// response carries the clamd address in force, the mode, and a live probe of
// whether that daemon answers. That is a map of an operator's internal
// infrastructure, handed to somebody who has no business acting on it.
const protectionIsInstanceWide = "protection settings apply to the whole instance and are managed by the platform operator"

// Protection handles GET/PATCH /api/admin/protection.
type Protection struct {
	Store db.Store
}

// NewProtection constructs the handler.
func NewProtection(store db.Store) *Protection { return &Protection{Store: store} }

// protectionResponse is the wire shape both verbs return.
type protectionResponse struct {
	TrashRetentionDays int `json:"trash_retention_days"`
	VersionsKeepN      int `json:"versions_keep_n"`
	// ShareMaxTTLDays is the longest life a NEW share link may be given
	// (0 = no ceiling). SharesOverMaxTTL counts existing live links that
	// outlive it — information for the operator, not something this API
	// changes.
	ShareMaxTTLDays  int                       `json:"share_max_ttl_days"`
	SharesOverMaxTTL int                       `json:"shares_over_max_ttl"`
	Antivirus        protectionAntivirusStatus `json:"antivirus"`
}

type protectionAntivirusStatus struct {
	// ---- STATUS: what this process is doing right now ----

	// Enabled is scanning being both switched on AND reachable-in-principle
	// (a binary resolved, or a clamd address is configured and parses). It is
	// the badge on the page, and it is NOT the toggle — see ScanEnabled.
	Enabled bool `json:"enabled"`
	// Binary names what answers a scan: "clamscan", "clamdscan", or "clamd"
	// in daemon mode. Empty when unavailable.
	Binary string `json:"binary"`
	// Mode is the transport in force ("binary" | "daemon"), empty when
	// unavailable. Address is the clamd address in force, empty in binary
	// mode.
	Mode    string `json:"mode"`
	Address string `json:"address,omitempty"`
	// Reachable is a live probe — clamd PING/PONG in daemon mode, an
	// existence check on the executable in binary mode. Health carries the
	// reason when it is false.
	//
	// ⚠⚠ This field is the point of the daemon mode's admin surface. A
	// scanner that cannot reach clamd does not report files clean (Scan
	// returns an error and the queue op fails), but nothing on a page would
	// otherwise SAY so, and an operator would read the green Enabled badge as
	// "files are being scanned". Reachable is what makes an unreachable
	// daemon visible instead of merely non-fatal.
	Reachable bool   `json:"reachable"`
	Health    string `json:"health,omitempty"`
	// Version is clamd's VERSION reply in daemon mode (decoration; empty when
	// it could not be asked).
	Version string `json:"version,omitempty"`
	// RestartPending is true while the stored antivirus configuration differs
	// from the one this process wired itself with at boot.
	RestartPending bool `json:"restart_pending"`

	// ---- SETTINGS: what the settings table holds (writable) ----

	// ScanEnabled is the `antivirus.enabled` row — the toggle. Deferred to
	// the next restart, in both directions.
	ScanEnabled bool `json:"scan_enabled"`
	// ScanMode is the `antivirus.mode` row, ClamdAddr the
	// `antivirus.clamd_addr` row. Reported even when scanning is off, because
	// the form has to render the choice an admin made regardless of whether
	// it is in force.
	ScanMode  string `json:"scan_mode"`
	ClamdAddr string `json:"clamd_addr"`
	// Modes is the closed set the API accepts, shipped with the value so the
	// form cannot offer an option the API refuses — the same reason the
	// numeric settings below ship their bounds.
	Modes []string `json:"modes"`
	// SaveScanWindowMinutes is how long a save from the browser's text
	// editor waits before its antivirus scan is queued; repeat saves inside
	// the window are absorbed into that one scan. Writable (unlike the two
	// fields above), and takes effect without a restart.
	SaveScanWindowMinutes int `json:"save_scan_window_minutes"`
	// The bounds are shipped with the value so the admin UI can render the
	// same limits the API enforces instead of hard-coding a second copy that
	// drifts.
	SaveScanWindowMin int `json:"save_scan_window_min"`
	SaveScanWindowMax int `json:"save_scan_window_max"`
	// MaxScanMB is the largest file that will be scanned; bigger files are
	// skipped, not failed. Writable, and live — the next file scanned uses
	// the new value.
	MaxScanMB    int `json:"max_scan_mb"`
	MaxScanMBMin int `json:"max_scan_mb_min"`
	MaxScanMBMax int `json:"max_scan_mb_max"`
}

// Get returns the current protection settings + antivirus status.
func (h *Protection) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, protectionIsInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.snapshot(r))
}

// protectionPatch is the PATCH body; pointer fields distinguish "absent"
// from zero values (keep_n 0 is a legal write meaning "unlimited").
type protectionPatch struct {
	TrashRetentionDays *int `json:"trash_retention_days"`
	VersionsKeepN      *int `json:"versions_keep_n"`
	ShareMaxTTLDays    *int `json:"share_max_ttl_days"`
	// AVSaveScanWindowMinutes is validated against antivirus.SaveWindowSetting
	// bounds and REFUSED when out of range, rather than being clamped: an
	// operator typing 5 must be told no while they are looking at the form.
	// (The read path clamps as a last resort — for rows written by hand, or
	// written before a bound changed — but a clamp nobody sees is how a
	// setting comes to mean something other than what the UI shows.)
	AVSaveScanWindowMinutes *int `json:"av_save_scan_window_minutes"`
	// AVMaxScanMB is validated the same way and for the same reason.
	AVMaxScanMB *int `json:"av_max_scan_mb"`
	// ⚠⚠ The three below are DEFERRED: they are stored immediately and take
	// effect at the next restart. The response's restart_pending stays true
	// for as long as that is so.
	AVEnabled   *bool   `json:"av_enabled"`
	AVMode      *string `json:"av_mode"`
	AVClamdAddr *string `json:"av_clamd_addr"`
}

// Patch updates one or both settings and echoes the fresh GET shape.
func (h *Protection) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, protectionIsInstanceWide) {
		return
	}
	var req protectionPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.TrashRetentionDays == nil && req.VersionsKeepN == nil &&
		req.ShareMaxTTLDays == nil && req.AVSaveScanWindowMinutes == nil &&
		req.AVMaxScanMB == nil && req.AVEnabled == nil && req.AVMode == nil &&
		req.AVClamdAddr == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no fields to update"})
		return
	}
	if v := req.ShareMaxTTLDays; v != nil && (*v < 0 || *v > share.MaxTTLDaysLimit) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "share_max_ttl_days must be between 0 and 3650"})
		return
	}
	if v := req.TrashRetentionDays; v != nil && (*v < protRetentionMin || *v > protRetentionMax) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trash_retention_days must be between 1 and 3650"})
		return
	}
	if v := req.VersionsKeepN; v != nil && (*v < protKeepNMin || *v > protKeepNMax) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "versions_keep_n must be between 0 and 1000"})
		return
	}
	if v := req.AVSaveScanWindowMinutes; v != nil {
		if err := antivirus.SaveWindowSetting.Validate(*v); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if v := req.AVMaxScanMB; v != nil {
		if err := antivirus.MaxScanSetting.Validate(*v); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	// ⚠ Both are canonicalised HERE and the canonical text is what gets
	// stored, so the row can never hold a value this API's own read path would
	// then reject and quietly replace with a default.
	var avMode, avAddr string
	if v := req.AVMode; v != nil {
		c, err := antivirus.ModeSetting.Canonical(*v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "av_mode " + err.Error()})
			return
		}
		avMode = c
	}
	if v := req.AVClamdAddr; v != nil {
		c, err := antivirus.AddrSetting.Canonical(*v)
		if err != nil {
			// ⚠ The package prefix belongs in a log line, not under a form
			// field: the person reading this typed the value two seconds ago
			// and needs the sentence, not the package that produced it.
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": strings.TrimPrefix(err.Error(), "antivirus: ")})
			return
		}
		avAddr = c
	}
	// ⚠ Daemon mode with no address is refused as a PAIR, not field by
	// field: each half is individually legal, and the combination is a
	// scanner that is switched on and can reach nothing. Refusing it here is
	// the only place an operator finds out while still looking at the form.
	if req.AVMode != nil && avMode == antivirus.ModeDaemon {
		addr := avAddr
		if req.AVClamdAddr == nil {
			addr = antivirus.AddrSetting.Resolve(r.Context(), h.Store)
		}
		if addr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "daemon mode needs a clamd address (host:port or a unix socket path)"})
			return
		}
	}
	if v := req.TrashRetentionDays; v != nil {
		if err := h.Store.UpsertSetting(r.Context(), trash.SettingKey, strconv.Itoa(*v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if v := req.VersionsKeepN; v != nil {
		if err := h.Store.UpsertSetting(r.Context(), versioning.SettingKeyKeepN, strconv.Itoa(*v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if v := req.ShareMaxTTLDays; v != nil {
		if err := h.Store.UpsertSetting(r.Context(), share.SettingKeyMaxTTLDays, strconv.Itoa(*v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if v := req.AVSaveScanWindowMinutes; v != nil {
		if err := h.Store.UpsertSetting(r.Context(), antivirus.SaveWindowSetting.Key, strconv.Itoa(*v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if v := req.AVMaxScanMB; v != nil {
		if err := h.Store.UpsertSetting(r.Context(), antivirus.MaxScanSetting.Key, strconv.Itoa(*v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if v := req.AVEnabled; v != nil {
		if err := h.Store.UpsertSetting(r.Context(), antivirus.EnabledSetting.Key, dbsetting.FormatBool(*v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.AVMode != nil {
		if err := h.Store.UpsertSetting(r.Context(), antivirus.ModeSetting.Key, avMode); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.AVClamdAddr != nil {
		if err := h.Store.UpsertSetting(r.Context(), antivirus.AddrSetting.Key, avAddr); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, h.snapshot(r))
}

// snapshot assembles the response from the settings table + the live
// antivirus binary probe.
func (h *Protection) snapshot(r *http.Request) protectionResponse {
	ctx := r.Context()
	retention := trash.DefaultRetentionDays
	if v, err := h.Store.GetSetting(ctx, trash.SettingKey); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			retention = n
		}
	}
	keepN := 0
	if v, err := h.Store.GetSetting(ctx, versioning.SettingKeyKeepN); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			keepN = n
		}
	}
	shareSvc := share.NewService(h.Store)
	maxTTL := shareSvc.MaxTTLDays(ctx)
	over, _ := shareSvc.CountOverMaxTTL(ctx, time.Now())
	// ⚠ ONE resolution, shared with internal/capability and the scan
	// pipeline: the badge on this page cannot drift from what actually scans,
	// because there is nothing else for it to be derived from.
	avRes := antivirus.Resolve(ctx, h.Store)
	// The SETTINGS as saved, which is a different question from the status
	// above: a disabled scanner still has a mode and an address an admin
	// chose, and the form has to render them.
	avCfg := antivirus.Configured(ctx, h.Store)
	sc := antivirus.NewWithResolution(avRes)
	// Live probe. Costs a dial + PING in daemon mode, bounded to a few
	// seconds — an admin is watching this page render, and "clamd did not
	// answer" is the answer they came for.
	health := ""
	if err := sc.Health(ctx); err != nil {
		health = err.Error()
	}
	version := ""
	if health == "" {
		if v, verr := sc.Version(ctx); verr == nil {
			version = v
		}
	}
	return protectionResponse{
		TrashRetentionDays: retention,
		VersionsKeepN:      keepN,
		ShareMaxTTLDays:    maxTTL,
		SharesOverMaxTTL:   over,
		Antivirus: protectionAntivirusStatus{
			Enabled:               sc.Supports(),
			Binary:                sc.BinName(),
			Mode:                  sc.Mode(),
			Address:               sc.Address(),
			Reachable:             sc.Supports() && health == "",
			Health:                health,
			Version:               version,
			RestartPending:        antivirus.RestartPending(avRes),
			ScanEnabled:           avCfg.Enabled,
			ScanMode:              avCfg.Mode,
			ClamdAddr:             avCfg.Addr,
			Modes:                 antivirus.Modes,
			SaveScanWindowMinutes: antivirus.SaveWindowSetting.Resolve(ctx, h.Store),
			SaveScanWindowMin:     antivirus.MinSaveWindowMinutes,
			SaveScanWindowMax:     antivirus.MaxSaveWindowMinutes,
			MaxScanMB:             antivirus.MaxScanSetting.Resolve(ctx, h.Store),
			MaxScanMBMin:          antivirus.MinMaxScanMB,
			MaxScanMBMax:          antivirus.MaxMaxScanMB,
		},
	}
}
