package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/comments"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/editkind"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tagname"
	"github.com/brf-tech/filex/backend/internal/version"
)

// capabilities_rules.go - the rules the clients used to keep copies of
// (filex #211, the audit's B2, B12, B16, B19, B20 and A11), published from
// the server that applies them.
//
// ⚠⚠ The server's work is done on the server: a rule, a number or a sentence
// is decided here, and the explorer, the admin panel and the desktop app only
// show it. Every copy a client kept had drifted or could drift the first time
// one side changed (`.graphql` offered an Edit save-text refused; a 64 that
// counted UTF-16 units against a server that counts characters).

// clientLimits are the numbers a client holds an input to before it is sent;
// the server enforces each of them again.
type clientLimits struct {
	// TagMaxRunes: the longest tag name, in characters (tagname.MaxRunes).
	TagMaxRunes int `json:"tag_max_runes"`
	// CommentMaxRunes: the longest comment, in characters (comments.MaxBodyLen).
	CommentMaxRunes int `json:"comment_max_runes"`
	// E2ERequestReasonMaxRunes: the longest reason an encryption request or a
	// decision note keeps, in characters (e2epolicy.MaxRequestChars; the rest
	// is cut).
	E2ERequestReasonMaxRunes int `json:"e2e_request_reason_max_runes"`
	// AppStateMaxBytes: what one app's interface may keep for a person, as
	// JSON (userprefs.go, PUT /api/me/prefs refuses more).
	AppStateMaxBytes int `json:"app_state_max_bytes"`
	// AppUISaveChunkBytes: one chunk of an app interface's save
	// (app_ui_chunks.go; a bigger chunk is refused).
	AppUISaveChunkBytes int `json:"app_ui_save_chunk_bytes"`
}

func currentClientLimits() clientLimits {
	return clientLimits{
		TagMaxRunes:              tagname.MaxRunes,
		CommentMaxRunes:          comments.MaxBodyLen,
		E2ERequestReasonMaxRunes: e2epolicy.MaxRequestChars,
		AppStateMaxBytes:         appStateMaxBytes,
		AppUISaveChunkBytes:      uiChunkMax,
	}
}

// eventOff is why a notification event cannot happen on this instance and
// whether the caller could change that (audit B16).
type eventOff struct {
	// Reason: what it waits for - "antivirus", "escrow", "app_plugins",
	// "e2e_approval".
	Reason string `json:"reason"`
	// Fixable: this caller could switch it on - the instance's administrator
	// for a service, the tenant's own administrator for the encryption policy.
	// The owner's rule for a missing service (lib/serviceGate): greyed with
	// the sentence for whoever can, not offered to anybody else.
	Fixable bool `json:"fixable"`
	// Text: the sentence that says so, in the reader's language.
	Text string `json:"text"`
}

// eventOffKeys are the sentences, by reason.
var eventOffKeys = map[string]string{
	"antivirus":    "server.event_off.antivirus",
	"escrow":       "server.event_off.escrow",
	"app_plugins":  "server.event_off.app_plugins",
	"e2e_approval": "server.event_off.e2e_approval",
}

// eventFacts are what decides whether an event can happen.
type eventFacts struct {
	antivirus    bool
	escrow       bool
	appPlugins   bool
	approval     bool // the caller's tenant asks for approval, with encryption available
	accountAdmin bool // the caller's account is an administrator's
	callerAdmin  bool // the caller may set the instance up (caller_admin)
}

// eventsOff answers, for each event that cannot happen, why and who could
// change it. An event absent from the answer can happen.
//
// ⚠ The two encryption request events wait for the TENANT's policy, which
// the tenant's own administrators set: `request_created` is sent to
// administrator accounts alone (notify/bell.go bellFor) and both exist only
// under the `approval` policy (e2epolicy requests.go announce). Which
// administrator can change it is the account's role, not caller_admin, which
// on a multi-tenant install is the supertenant's alone.
func eventsOff(f eventFacts) map[string]eventOff {
	out := map[string]eventOff{}
	off := func(event, reason string, fixable bool) {
		out[event] = eventOff{Reason: reason, Fixable: fixable}
	}
	if !f.antivirus {
		off("file.infected", "antivirus", f.callerAdmin)
	}
	if !f.escrow {
		off("e2e.escrow_used", "escrow", f.callerAdmin)
	}
	if !f.appPlugins {
		off("plugin.notice", "app_plugins", f.callerAdmin)
	}
	if !(f.accountAdmin && f.approval) {
		off("e2e.request_created", "e2e_approval", f.accountAdmin)
	}
	if !f.approval {
		off("e2e.request_decided", "e2e_approval", f.accountAdmin)
	}
	return out
}

// publishRules adds the rule blocks to a capabilities answer.
func (h *Capabilities) publishRules(r *http.Request, c *model.Capabilities, merged map[string]any) {
	// B2: how each kind of file is edited - the lists the explorer, the
	// preview and the desktop app look a file up in (internal/editkind).
	// A property of the build, so an anonymous caller gets it too.
	merged["edit_kinds"] = editkind.Published()
	// B12, B19, B20: the numbers an input is held to.
	merged["limits"] = currentClientLimits()
	// A11: the release, the commit and the build time, apart - `version`
	// keeps the one-line form for whoever printed it whole.
	merged["release"] = version.Version
	if version.Commit != "" && version.Commit != "unknown" {
		merged["commit"] = version.Commit
	}
	if version.Date != "" && version.Date != "unknown" {
		merged["built"] = version.Date
	}
	// B16: the notification events that cannot happen here, for a signed-in
	// person choosing what to be told about.
	u := auth.UserFrom(r.Context())
	if u == nil {
		return
	}
	f := eventFacts{
		antivirus:    c.Antivirus,
		escrow:       h.E2EEscrow != nil,
		appPlugins:   c.AppPlugins.Enabled,
		accountAdmin: u.IsAdmin(),
		callerAdmin:  h.callerCanConfigure(r),
	}
	if pol, ok := h.e2ePolicy(r); ok {
		avail, _ := pol["available"].(bool)
		policy, _ := pol["policy"].(string)
		f.approval = avail && policy == "approval"
	}
	lang := requestLang(r, r.URL.Query().Get("lang"))
	offs := eventsOff(f)
	for ev, o := range offs {
		o.Text = srvtext.Text(lang, eventOffKeys[o.Reason], nil)
		offs[ev] = o
	}
	merged["event_off"] = offs
}
