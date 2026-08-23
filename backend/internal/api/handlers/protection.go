// Package handlers — protection.go ("Koru" v0.4)
//
// Admin protection-settings surface, frozen contract:
//
//	GET   /api/admin/protection → {"trash_retention_days":30,"versions_keep_n":0,
//	                               "share_max_ttl_days":7,"shares_over_max_ttl":0,
//	                               "antivirus":{"enabled":bool,"binary":"clamscan|clamdscan|"}}
//	PATCH /api/admin/protection → {trash_retention_days?, versions_keep_n?, share_max_ttl_days?}
//
// The values live in the settings table (`trash.retention_days`,
// `versions.keep_n`, `share.max_ttl_days`); antivirus state is probed live
// from the shared resolver in internal/antivirus (read-only — the binary is
// an operator/env concern, not a DB setting). `shares_over_max_ttl` is a
// read-only count of EXISTING live links that outlive the ceiling — they are
// reported, never shortened (see share.CountOverMaxTTL).
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/db"
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
	Enabled bool   `json:"enabled"`
	Binary  string `json:"binary"`
}

// Get returns the current protection settings + antivirus status.
func (h *Protection) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.snapshot(r))
}

// protectionPatch is the PATCH body; pointer fields distinguish "absent"
// from zero values (keep_n 0 is a legal write meaning "unlimited").
type protectionPatch struct {
	TrashRetentionDays *int `json:"trash_retention_days"`
	VersionsKeepN      *int `json:"versions_keep_n"`
	ShareMaxTTLDays    *int `json:"share_max_ttl_days"`
}

// Patch updates one or both settings and echoes the fresh GET shape.
func (h *Protection) Patch(w http.ResponseWriter, r *http.Request) {
	var req protectionPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.TrashRetentionDays == nil && req.VersionsKeepN == nil && req.ShareMaxTTLDays == nil {
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
	sc := antivirus.New()
	return protectionResponse{
		TrashRetentionDays: retention,
		VersionsKeepN:      keepN,
		ShareMaxTTLDays:    maxTTL,
		SharesOverMaxTTL:   over,
		Antivirus: protectionAntivirusStatus{
			Enabled: sc.Supports(),
			Binary:  sc.BinName(),
		},
	}
}
