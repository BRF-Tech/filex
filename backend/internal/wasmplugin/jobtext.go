package wasmplugin

import (
	"encoding/json"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ⚠⚠ A job's words are chosen when somebody READS the operations list, in
// the language on their screen — never frozen at submit.
//
// The row used to keep the action label and the app's final message
// flattened to ONE language when the job was created (`Label.Get(locale)`,
// `Message.Get(job.Locale)`): an embedded explorer drawing Turkish over an
// English account read "Convert…" and an English result in a Turkish tray
// (2026-09-26). The app hands the host every language it wrote; the row now
// keeps them all, in the same text column (no migration: a row written
// before this reads back exactly as it was), and DecorateOps picks the
// reader's (srvtext.Reader, from the request's `?lang=` / account /
// Accept-Language).

// jobTextPrefix marks a column that holds a {lang: text} map rather than one
// string. A plain message that happens to start with it and is not valid
// JSON after it is shown as it is.
const jobTextPrefix = "i18n:"

// EncodeJobText is t as a job column: one plain string when every language
// says the same (or only one was written), else every language, marked.
func EncodeJobText(t wire.Text) string {
	var first string
	same := true
	n := 0
	for _, v := range t {
		if v == "" {
			continue
		}
		if n == 0 {
			first = v
		} else if v != first {
			same = false
		}
		n++
	}
	if n == 0 {
		return ""
	}
	if same {
		return first
	}
	b, err := json.Marshal(t)
	if err != nil {
		return t.Get("en")
	}
	return jobTextPrefix + string(b)
}

// JobText reads a job column in lang (wire.Text.Get: the language, its base
// language, English, any). A plain column — every row written before
// EncodeJobText, a progress line, the host's own words — comes back as it
// is.
func JobText(stored, lang string) string {
	rest, ok := strings.CutPrefix(stored, jobTextPrefix)
	if !ok {
		return stored
	}
	var t wire.Text
	if err := json.Unmarshal([]byte(rest), &t); err != nil || len(t) == 0 {
		return stored
	}
	return t.Get(lang)
}
