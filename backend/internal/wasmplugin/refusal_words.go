package wasmplugin

import (
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Said is the refusal as a reader is told it: `message` is the server's
// sentence for its code in lang (`server.install.<code>`, a failed
// download's `server.install.fetch.<reason>`), filled from its own fields,
// and Go's English moves to `detail`, for a log. A copy: what an update
// check stored (UpdateInfo.Refusal) keeps its English and is said again,
// in the language of whoever reads it, every time it is read.
//
// ⚠⚠ Why (0.55, the 0.54 rule "the server writes the sentence"). The
// install wizard and the Apps list built this sentence in the browser
// (web lib/appPluginRefusal.ts with appPlugins.wizard.errors.*), from the
// code and the fields; the command line and an agent read Go's English.
// Said twice (a refusal already said) it is returned as it is.
func (ref *InstallRefusal) Said(lang string) *InstallRefusal {
	if ref == nil {
		return nil
	}
	if ref.Detail != "" {
		return ref
	}
	out := *ref
	out.Detail = ref.Message
	out.Message = installSentence(lang, ref)
	return &out
}

// installSentence is ref's sentence in lang.
func installSentence(lang string, ref *InstallRefusal) string {
	v := srvtext.Vars{
		"missing":  orDash(strings.Join(ref.Missing, ", ")),
		"detail":   orDash(ref.Message),
		"requires": orDash(ref.Requires),
		"filex":    orDash(ref.Filex),
	}
	key := "server.install." + ref.Code
	if ref.Code == ErrCodeFetch {
		reason := ref.Reason
		if !srvtext.Has("server.install.fetch." + reason) {
			// A reason this filex does not know: what a person can check
			// for any download.
			reason = FetchReasonUnreachable
		}
		key = "server.install.fetch." + reason
		v["where"] = orDash(ref.Where)
		v["refs"] = orDash(strings.Join(ref.Refs, ", "))
		v["status"] = "-"
		if ref.Status > 0 {
			v["status"] = strconv.Itoa(ref.Status)
		}
	}
	if !srvtext.Has(key) {
		key = "server.install.failed"
	}
	return srvtext.Text(srvtext.Pick(lang), key, v)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
