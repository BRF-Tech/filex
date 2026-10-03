package assoc

import (
	"context"
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
	if s == nil || len(placements) == 0 {
		return nil
	}
	var failed []string
	for _, p := range placements {
		what := p.Capability + " ." + p.Ext + ": "
		if AppOf(p.Handler) != app {
			failed = append(failed, what+p.Handler+" is not this app's")
			continue
		}
		if only != nil && !only[KindKey(p.Capability, p.Ext)] {
			failed = append(failed, what+"not a kind this version adds; its order stays as it is")
			continue
		}
		if err := s.Place(ctx, p, by); err != nil {
			failed = append(failed, what+err.Error())
		}
	}
	return failed
}
