package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/instancesettings"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// InstanceSettings serves the settings that lived in the environment alone
// until 0.57 (task #258, internal/instancesettings): the default language, the
// sync interval, the apps' limits, the notifications switch and the rest. One
// handler for every card that draws them - Admin → Settings → General,
// Protection, Plugins, Notifications, Webhooks - so the cards cannot answer
// differently about the same row.
//
//	GET   /api/admin/instance-settings[?group=general]  → {"items": [...]}
//	PATCH /api/admin/instance-settings  {"<key>": <value>, ...}  → the GET
//
// The instance's: a tenant's administrator is refused both
// (requireSupertenant), and the menu does not offer them those pages.
type InstanceSettings struct {
	Store db.Store
}

// NewInstanceSettings builds the handler.
func NewInstanceSettings(store db.Store) *InstanceSettings {
	return &InstanceSettings{Store: store}
}

const instanceSettingsAreInstanceWide = "instance settings"

// Get answers the settings of one group, or of every group.
func (h *InstanceSettings) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, instanceSettingsAreInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": instancesettings.Items(r.Context(), h.Store, r.URL.Query().Get("group")),
	})
}

// Patch checks every value sent against its setting and stores them only
// when all are good - one refusal, under the field it is about, and nothing
// written - then answers the GET of the same group.
func (h *InstanceSettings) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, instanceSettingsAreInstanceWide) {
		return
	}
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || len(patch) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no settings to update"})
		return
	}
	if _, err := instancesettings.Write(r.Context(), h.Store, patch); err != nil {
		var fe *instancesettings.FieldError
		if errors.As(err, &fe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":   fe.English,
				"field":   fe.Key,
				"message": instanceSettingRefusal(r, fe),
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": instancesettings.Items(r.Context(), h.Store, r.URL.Query().Get("group")),
	})
}

// instanceSettingRefusal is the sentence the page shows under the field, in
// the reader's language: the Protection page's own words for a number out of
// range or not a number, and a language tag's.
func instanceSettingRefusal(r *http.Request, fe *instancesettings.FieldError) string {
	lang := requestLang(r)
	switch fe.Code {
	case "range":
		return srvtext.Text(lang, "server.protection.out_of_range",
			srvtext.Vars{"min": strconv.Itoa(fe.Min), "max": strconv.Itoa(fe.Max)})
	case "type":
		return srvtext.Text(lang, "server.protection.whole_number", nil)
	case "language":
		return srvtext.Text(lang, "server.settings.not_a_language", nil)
	}
	return srvtext.Text(lang, "server.settings.unknown_setting", nil)
}
