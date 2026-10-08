// Package handlers — audit_label.go
//
// What an audit row says, in the reader's language: `label` (the action) and
// `target_label` (the thing it is about) on every row of GET
// /api/admin/audit and of the dashboard's Recent activity, and `resources`,
// the Audit page's "What" filter.
//
// The server stores an action as `<resource>.<verb>` (`user.password_reset`,
// `storage.sync_trigger`, `ai.file.move`) and a target as a type plus an id.
// Those are wire names; until 0.54 the panel composed the sentence from them
// in the browser (web lib/auditLabel.ts), so the admin MCP tool
// admin_audit_list - and any other reader of the log - got only the codes.
//
// ⚠ The set of actions is OPEN. auth/audit_middleware.go falls back to
// `<admin segment>.<method verb>` for every mutating /api/admin/* route it has
// no case for, and `ai.file.<verb>` for every AI verb, so a lookup table of
// whole action names would go stale the day a route is added. The label is
// therefore COMPOSED: the resource and the verb are said separately
// (`server.audit.resource.*`, `server.audit.verb.*`) and joined by one phrase
// (`server.audit.phrase`). A resource or verb the catalogue does not know is
// still printed readably (`replication targets`), never dropped.
//
// web/tests/lib/auditLabel.test.ts reads the Go that writes the rows and fails
// when a fixed action name or target type has no words in either language.
package handlers

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// readerLang is the language a page's server-written words are said in: the
// screen's own (`?lang=`, which the admin panel sends with the reads that
// carry words - the screen may be switched without the account being
// changed), then requestLang's order.
func readerLang(r *http.Request) string {
	return requestLang(r, r.URL.Query().Get("lang"))
}

// auditKey folds a wire name to its catalogue key: `.` and `-` become `_`.
func auditKey(s string) string { return strings.NewReplacer(".", "_", "-", "_").Replace(s) }

var auditSeparators = regexp.MustCompile(`[._-]+`)

// auditReadable is a name the catalogue lacks, made readable:
// `replication-targets` → `replication targets`.
func auditReadable(s string) string {
	return strings.TrimSpace(auditSeparators.ReplaceAllString(s, " "))
}

// auditSplit splits `ai.file.move` into resource `ai.file` and verb `move`.
func auditSplit(action string) (resource, verb string) {
	i := strings.LastIndex(action, ".")
	if i <= 0 {
		return action, ""
	}
	return action[:i], action[i+1:]
}

// auditResource is a resource's name; `ai.<resource>` (a write through the AI
// admin surface, auth.AIAdminAction) is the same resource, marked as taken
// through AI.
func auditResource(lang, resource string) string {
	if key := "server.audit.resource." + auditKey(resource); srvtext.Has(key) {
		return srvtext.Text(lang, key, nil)
	}
	if strings.HasPrefix(resource, "ai.") && len(resource) > 3 {
		return srvtext.Text(lang, "server.audit.via_ai", srvtext.Vars{"resource": auditResource(lang, resource[3:])})
	}
	return auditReadable(resource)
}

// auditActionLabel is an action in words: "User: password reset".
func auditActionLabel(lang, action string) string {
	if action == "" {
		return "-"
	}
	resource, verb := auditSplit(action)
	r := auditResource(lang, resource)
	if verb == "" {
		return r
	}
	v := auditReadable(verb)
	if key := "server.audit.verb." + auditKey(verb); srvtext.Has(key) {
		v = srvtext.Text(lang, key, nil)
	}
	return srvtext.Text(lang, "server.audit.phrase", srvtext.Vars{"resource": r, "verb": v})
}

// auditTargetLabel is the thing a row is about: its kind, and WHICH one.
//
// name is the server's own answer (target_name: a user's e-mail, a storage's
// name, a file's path - audit_targets.go). An id is shown only when there is
// no name, and a numeric one as `#12` rather than as if it were a name. The
// middleware's generic fallback names its target by the route segment
// (`app-plugins`), which has no target word of its own: the resource's word
// stands in for it before the raw text. A public demo's masked address
// (demoMaskedIP) is said in words.
func auditTargetLabel(lang, typ, id, name string) string {
	if id == demoMaskedIP || name == demoMaskedIP {
		hidden := srvtext.Text(lang, "server.audit.hidden_address", nil)
		if id == demoMaskedIP {
			id = hidden
		}
		if name == demoMaskedIP {
			name = hidden
		}
	}
	kind := ""
	if typ != "" {
		switch tk, rk := "server.audit.target."+auditKey(typ), "server.audit.resource."+auditKey(typ); {
		case srvtext.Has(tk):
			kind = srvtext.Text(lang, tk, nil)
		case srvtext.Has(rk):
			kind = srvtext.Text(lang, rk, nil)
		default:
			kind = auditReadable(typ)
		}
	}
	switch {
	case name != "" && kind != "":
		return srvtext.Text(lang, "server.audit.target_named", srvtext.Vars{"kind": kind, "name": name})
	case name != "":
		return name
	case typ == "":
		return id
	case id == "":
		return kind
	case isAllDigits(id):
		return srvtext.Text(lang, "server.audit.target_numbered", srvtext.Vars{"kind": kind, "id": id})
	}
	return srvtext.Text(lang, "server.audit.target_named", srvtext.Vars{"kind": kind, "name": id})
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// auditResourceOption is one entry of the Audit page's "What" filter: the
// label in the reader's language, and the `<resource>.` prefix(es) the list
// filter understands (db.AuditActionPrefixes), comma-separated when two
// spellings share one name.
type auditResourceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// auditWireResource is the catalogue keys whose wire resource is not the key
// itself (auditKey folds both `.` and `-` to `_`, so the way back has to be
// spelled out). web/tests/lib/auditLabel.test.ts reads this map.
var auditWireResource = map[string][]string{
	// The fixed action is `login_security.update`, the admin route's own segment `login-security`.
	"login_security":      {"login_security", "login-security"},
	"ai_file":             {"ai.file"},
	"ai_share":            {"ai.share"},
	"ai_tokens":           {"ai-tokens"},
	"replication_targets": {"replication-targets"},
	"auth_providers":      {"auth-providers"},
	"app_plugins":         {"app-plugins"},
	"plugin_requests":     {"plugin-requests"},
	"webhook_config":      {"webhook-config"},
	"smtp_test":           {"smtp-test"},
	"file_types":          {"file-types"},
}

// auditResourceOptions is every resource the catalogue names, labelled in
// lang and sorted by its label in lang's order. ⚠ The filter was a free-text
// box matched EXACTLY against wire names ("user.create") - typing what the
// page shows ("Kullanıcı") found nothing.
func auditResourceOptions(lang string) []auditResourceOption {
	lang = srvtext.Pick(lang)
	byLabel := map[string][]string{}
	for key, label := range srvtext.Table(lang, "server.audit.resource.") {
		wires, ok := auditWireResource[key]
		if !ok {
			wires = []string{key}
		}
		list := byLabel[label]
		for _, w := range wires {
			p := w + "."
			if !containsString(list, p) {
				list = append(list, p)
			}
		}
		byLabel[label] = list
	}
	out := make([]auditResourceOption, 0, len(byLabel))
	for label, prefixes := range byLabel {
		sort.Strings(prefixes)
		out = append(out, auditResourceOption{Value: strings.Join(prefixes, ","), Label: label})
	}
	col := collate.New(language.Make(lang))
	sort.Slice(out, func(i, j int) bool {
		if c := col.CompareString(out[i].Label, out[j].Label); c != 0 {
			return c < 0
		}
		return out[i].Value < out[j].Value
	})
	return out
}
