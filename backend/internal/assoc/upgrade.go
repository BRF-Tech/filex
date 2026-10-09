package assoc

import (
	"context"
	"errors"
	"sort"
)

// ── an upgrade, and a request an administrator approves ──────────────────
//
// An upgrade that names kinds the app did not handle before asks the same
// question the first install asked - where does the app go for each of them -
// about those kinds and NOTHING else (the maintainer, 2026-10-01): the order the
// administrator already has for the kinds the app handled is not reopened by
// a new version. An install request an administrator approves asks it too,
// with the same rows.

// KindKey names one capability of one kind ("open .drawio").
func KindKey(capability, ext string) string { return capability + " ." + ext }

// kindsOf are the kinds one handler names: its extensions and the known
// extensions of its media types, sorted.
func kindsOf(h AppHandler) []string {
	exts := map[string]bool{}
	for _, e := range h.Ext {
		if ValidExt(e) {
			exts[e] = true
		}
	}
	for _, m := range h.Mime {
		for _, e := range ExtsOfMime(m) {
			exts[e] = true
		}
	}
	list := make([]string, 0, len(exts))
	for e := range exts {
		list = append(list, e)
	}
	sort.Strings(list)
	return list
}

// HandledKinds is every capability and kind an app's handlers cover, by
// KindKey.
func HandledKinds(open, thumb []AppHandler) map[string]bool {
	out := map[string]bool{}
	for _, h := range open {
		for _, e := range kindsOf(h) {
			out[KindKey(CapOpen, e)] = true
		}
	}
	for _, h := range thumb {
		for _, e := range kindsOf(h) {
			out[KindKey(CapThumbnail, e)] = true
		}
	}
	return out
}

// NewKinds are the kinds in after that are not in before: what an upgrade
// adds.
func NewKinds(before, after map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range after {
		if !before[k] {
			out[k] = true
		}
	}
	return out
}

// OnlyNewInstallKinds keeps the File types rows whose kind the app did not
// handle before (before, by KindKey): an upgrade's review asks about those.
func OnlyNewInstallKinds(rows []InstallKind, before map[string]bool) []InstallKind {
	out := []InstallKind{}
	for _, r := range rows {
		if !before[KindKey(r.Capability, r.Ext)] {
			out = append(out, r)
		}
	}
	return out
}

// PlaceForApp writes the File types choices of an install, an upgrade or an
// approved request, and answers the ones it could not write (the app is
// installed either way; the screen says which kinds kept their order).
//
// ⚠ Two refusals, both about whose decision it is:
//   - a choice that names ANOTHER app's handler: the install decides where
//     THIS app goes, nothing else;
//   - with only set (an upgrade), a choice for a kind not in it: the order the
//     administrator already has for a kind the app handled before stays as it
//     is.
func (s *Service) PlaceForApp(ctx context.Context, app string, placements []Placement, only map[string]bool, by *int64) []string {
	var failed []string
	for _, f := range s.PlaceForAppFailures(ctx, app, placements, only, by) {
		failed = append(failed, f.Capability+" ."+f.Ext+": "+f.Message)
	}
	return failed
}

// PlaceFailure is one File types choice that was not written: the kind, and
// why. Say names the reader's sentence (server.error.<Say>, said by the
// handlers in the reader's language) and Params fill it; Message is the
// English, for a log.
type PlaceFailure struct {
	Capability string
	Ext        string
	Handler    string
	Say        string
	Params     map[string]string
	Message    string
}

// PlaceForAppFailures is PlaceForApp answering each choice it could not
// write with the sentence it is said in.
func (s *Service) PlaceForAppFailures(ctx context.Context, app string, placements []Placement, only map[string]bool, by *int64) []PlaceFailure {
	if s == nil || len(placements) == 0 {
		return nil
	}
	var failed []PlaceFailure
	for _, p := range placements {
		f := PlaceFailure{Capability: p.Capability, Ext: p.Ext, Handler: p.Handler}
		if AppOf(p.Handler) != app {
			f.Say, f.Message = "place_not_own", p.Handler+" is not this app's"
			failed = append(failed, f)
			continue
		}
		if only != nil && !only[KindKey(p.Capability, p.Ext)] {
			f.Say, f.Message = "place_not_new", "not a kind this version adds; its order stays as it is"
			failed = append(failed, f)
			continue
		}
		if err := s.Place(ctx, p, by); err != nil {
			f.Say, f.Message = "place_failed", err.Error()
			var bad *ErrInvalid
			if errors.As(err, &bad) && bad.Say != "" {
				f.Say, f.Params = "rule_"+bad.Say, bad.Params
			}
			failed = append(failed, f)
		}
	}
	return failed
}
