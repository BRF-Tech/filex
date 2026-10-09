// Package handlers - app_store_words.go
//
// The sentence of a store refusal (internal/appstore), written by the server
// in the reader's language.
//
// ⚠⚠ Why (0.55). appstore.Error carried an English Go sentence ("lang-eo
// 1.1.0 is installed; the link is for 1.0.0 ..."), and the admin panel threw
// it away and built its own from the code (web lib/storeRefusal.ts and the
// locales' appStore.err.*): a second copy of every sentence, one that knew
// nothing a code did not carry, and that the command line and an agent never
// had. #215 wrote the storage plugin's refusals here (detail.kind "storage");
// this does the same for every other store refusal - an app's, a language
// pack's, the trust, the connection, a license key - in one place: storeFail
// says the code's `server.store.<code>` sentence in `message`, and keeps
// Go's English in `detail.reason` for a log.
package handlers

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// storeSaid is e as the reader is told it: the same code and detail, the
// server's sentence in lang as `message`, Go's English as `detail.reason`.
// A storage plugin's refusal (detail.kind "storage") already carries its
// sentence (app_store_storage.go) and is answered as it is.
func storeSaid(lang string, e *appstore.Error) *appstore.Error {
	if k, _ := e.Detail["kind"].(string); k == appstore.KindStorage && e.Message != "" {
		return e
	}
	said := storeSentence(lang, e)
	if said == "" {
		// A code the catalogue does not know: what was there stays.
		return e
	}
	out := &appstore.Error{Code: e.Code, Message: said, Detail: make(map[string]any, len(e.Detail)+1)}
	for k, v := range e.Detail {
		out.Detail[k] = v
	}
	if e.Message != "" && e.Message != said {
		out.Detail["reason"] = e.Message
	}
	return out
}

// storeSentence is the `server.store.<code>` sentence of e in lang, filled
// from what its detail carries; "" for a code without one.
func storeSentence(lang string, e *appstore.Error) string {
	key := "server.store." + e.Code
	v := srvtext.Vars{}
	switch e.Code {
	case appstore.CodePinMismatch:
		fields := storeMismatchFields(e.Detail["mismatches"])
		for _, f := range fields {
			if f == "commit" {
				// A moved tag: the repository moved it, not the store.
				key += "_commit"
				break
			}
		}
		v["fields"] = storeOrDash(strings.Join(fields, ", "))
	case appstore.CodeVersionRollback:
		v["installed"] = storeOrDash(storeDetailString(e.Detail["installed"]))
		v["link"] = storeOrDash(storeDetailString(e.Detail["link"]))
	case appstore.CodeSourceChanged:
		was, _ := storeSourceOfDetail(e.Detail["installed"])
		link, _ := storeSourceOfDetail(e.Detail["link"])
		// Installed straight from the same repository, no store: a store's
		// PAID link does not take it under its license - said apart, with
		// the way out (installedFor refuses only that case for one repo).
		if was.Store == "" && was.Repo != "" && strings.EqualFold(was.Repo, link.Repo) {
			key += "_direct"
			v["version"], v["repo"], v["store"] = storeOrDash(was.Version), was.Repo, storeOrDash(link.Store)
		} else {
			v["version"] = storeOrDash(was.Version)
			v["installed"] = storeSourceText(lang, was)
			v["link"] = storeSourceText(lang, link)
		}
	case appstore.CodeWrongInstance:
		if b, _ := e.Detail["public_url_invalid"].(bool); b {
			key += "_public_url"
		} else {
			v["link"] = storeOrDash(storeDetailString(e.Detail["filex_origin"]))
			v["here"] = storeOrDash(storeDetailString(e.Detail["this_filex"]))
		}
	case appstore.CodeStoreRefusal:
		// What the store said, as it said it.
		v["reason"] = storeOrDash(e.Message)
	}
	if !srvtext.Has(key) {
		return ""
	}
	return srvtext.Text(lang, key, v)
}

// storeSourceText is where an app came from, as a person reads it: "the
// store <origin> (<repo>)", or - no store - "GitHub directly, without a
// store (<repo>)" / "an upload or an address, without a store (<address>)".
func storeSourceText(lang string, src installedSource) string {
	repo := src.Repo
	if repo == "" {
		repo = src.SourceURL
	}
	repo = storeOrDash(repo)
	switch {
	case src.Store != "":
		return srvtext.Text(lang, "server.store.source_store", srvtext.Vars{"store": src.Store, "repo": repo})
	case src.Repo != "":
		return srvtext.Text(lang, "server.store.source_direct", srvtext.Vars{"repo": repo})
	default:
		return srvtext.Text(lang, "server.store.source_other", srvtext.Vars{"repo": repo})
	}
}

// sourceOfDetail reads an installedSource back out of a refusal's detail.
func storeSourceOfDetail(x any) (installedSource, bool) {
	switch s := x.(type) {
	case installedSource:
		return s, true
	case *installedSource:
		if s != nil {
			return *s, true
		}
	}
	return installedSource{}, false
}

// mismatchFields are the fields of a pin mismatch's detail, in order.
func storeMismatchFields(x any) []string {
	var out []string
	switch ms := x.(type) {
	case []appstore.Mismatch:
		for _, m := range ms {
			out = append(out, m.Field)
		}
	case []any:
		for _, m := range ms {
			if mm, ok := m.(map[string]any); ok {
				if f, ok := mm["field"].(string); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

func storeDetailString(x any) string {
	s, _ := x.(string)
	return s
}

func storeOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
