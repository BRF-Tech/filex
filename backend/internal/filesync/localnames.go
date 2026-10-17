package filesync

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/localname"
)

// ── names this computer and the server spell differently ─────────────────
//
// The server keeps the name it was given, so a pair's server folder can hold
// names THIS computer cannot (task #238, the hostile names): `report.` and
// `trailing.txt ` (Windows drops the trailing dot or space), `CON` and
// `aux.txt` (device names), `rapor:2024.txt` and `back\slash.txt`
// (characters Windows refuses or reads as a separator), a name over 255 bytes
// (ext4, APFS), and two names that are one file here (`Rapor.docx` beside
// `rapor.docx` on Windows and macOS, the same name composed and decomposed on
// macOS).
//
// ⚠⚠ What this engine did with them up to 0.56, read off the code on the
// Windows rules: `report.` was downloaded, landed as `report`, and the next
// pass saw `report.` "deleted here" and a new file `report`: it deleted the
// server's file (to its trash, shares and history with it) and uploaded a
// copy under the other name. A folder `report.` was removed on the server
// whole. Of two case twins the second's download stood down on every pass and
// fetched the whole body again, for ever; of two twin FOLDERS the children
// merged into one and the next pass deleted the twin on the server.
//
// So the pass works in THIS computer's names. Before anything is planned
// (mapNames), every server item gets the name it has here:
//
//   - its own name, when this computer holds it and nothing here is already
//     that name;
//   - the spelling an item here already has, when the two are one name to
//     this file system (`rapor.docx` here, `Rapor.docx` on the server: one
//     file on Windows, so they are compared as one item - never uploaded as
//     a second, twin, file);
//   - otherwise an alias (localname.Alias: `rapor:2024.txt` → `rapor_2024.txt`,
//     `CON` → `_CON`, the second of two twins → `rapor (2).docx`), downloaded
//     under it and REPORTED ("synced to this computer as …");
//   - nothing at all when the alias is already the name of a file here that
//     is not this item: it is not downloaded, and that is reported.
//
// The pairing (server name → name here) is kept in the pair's state
// (names/<pair-id>.json) and is the same on every pass. Everything after the
// mapping is the ordinary engine, in this computer's names: a change here to
// `rapor_2024.txt` is uploaded to the server's `rapor:2024.txt`, a change on
// the server downloads to `rapor_2024.txt`, a delete on either side follows
// the delete rules of any item. The server is never renamed, nor anything on
// it deleted, because of how a name is spelled here.
//
// And the other way: a name made HERE that the server does not accept
// (internal/leafname: a control character, `\`, `...`) is not uploaded to
// fail on every pass; it stays here and is reported once a pass.

// Why a name is reported.
const (
	// ReasonSyncedAs: the server item Rel is synced to this computer as
	// Other (Why says why its own name is not used).
	ReasonSyncedAs = "synced_as"
	// ReasonTaken: the server item Rel would be synced as Other, and a file
	// that is not it already has that name here; it is not downloaded.
	ReasonTaken = "taken"
	// ReasonServerRefuses: the item Rel here has a name the server does not
	// accept; it is not uploaded.
	ReasonServerRefuses = "server_refuses"
	// ReasonImage: the item Rel HERE is what Windows made, for an earlier
	// version of this engine, of the server item Other (folder `back` for the
	// file `back\slash.txt`); uploading it would copy the server's item under
	// the wrong name and shape.
	ReasonImage = "image"
	// WhyTwin is ReasonSyncedAs's Why for the second of two names that are
	// one name here; the other reasons are localname's codes (Reserved,
	// Forbidden, Trailing, TooLong).
	WhyTwin = "twin"
)

// NameReport is one item whose name a pass could not use as it is.
type NameReport struct {
	// Rel is the server's path for ReasonSyncedAs and ReasonTaken, this
	// computer's for ReasonServerRefuses and ReasonImage (pair-relative).
	Rel    string `json:"rel"`
	Reason string `json:"reason"`
	// Other is the name here (ReasonSyncedAs, ReasonTaken) or the server's
	// item (ReasonImage).
	Other string `json:"other,omitempty"`
	// Why is ReasonSyncedAs's cause: a localname code or WhyTwin.
	Why string `json:"why,omitempty"`
}

// goos is the operating system whose names the local folder follows:
// Engine.GOOS, else the one this runs on.
func (e *Engine) goos() string {
	if e.GOOS != "" {
		return e.GOOS
	}
	return runtime.GOOS
}

// nameMap pairs the server's names with this computer's for the items whose
// names differ. An item with no entry - on itself or on any of its folders -
// is called the same on both sides.
type nameMap struct {
	toLocal  map[string]string // server rel → rel here
	toRemote map[string]string // rel here → server rel
	dirty    bool
}

func newNameMap(saved map[string]string) *nameMap {
	m := &nameMap{toLocal: map[string]string{}, toRemote: map[string]string{}}
	for r, l := range saved {
		m.toLocal[r] = l
		m.toRemote[l] = r
	}
	return m
}

// set pairs the server item r with the name here l (an entry only when the
// item's own name differs; its contents follow by prefix).
func (m *nameMap) set(r, l string) {
	if old, ok := m.toLocal[r]; ok {
		if old == l {
			return
		}
		delete(m.toRemote, old)
	}
	m.toLocal[r] = l
	m.toRemote[l] = r
	m.dirty = true
}

func (m *nameMap) drop(r string) {
	if l, ok := m.toLocal[r]; ok {
		delete(m.toLocal, r)
		delete(m.toRemote, l)
		m.dirty = true
	}
}

// translate rewrites rel through the deepest entry of byPrefix that is rel or
// one of its folders.
func translate(byPrefix map[string]string, rel string) string {
	if len(byPrefix) == 0 {
		return rel
	}
	for p := rel; p != ""; p = parentDir(p) {
		if to, ok := byPrefix[p]; ok {
			return to + rel[len(p):]
		}
	}
	return rel
}

// remoteRel is the server's path for an item here.
func (m *nameMap) remoteRel(local string) string {
	if m == nil {
		return local
	}
	return translate(m.toRemote, local)
}

// covers reports whether rel (in either side's names) is an aliased item or
// lies inside one: a targeted pass reconciles such a folder from its parent.
func (m *nameMap) covers(rel string) bool {
	if m == nil {
		return false
	}
	for p := rel; p != ""; p = parentDir(p) {
		if _, ok := m.toLocal[p]; ok {
			return true
		}
		if _, ok := m.toRemote[p]; ok {
			return true
		}
	}
	return false
}

// remotePath is the server's `adapter://path` for an item here.
func (e *Engine) remotePath(rel string) string {
	return joinRemote(e.Pair.Remote, e.names.remoteRel(rel))
}

// loadNames reads the pair's name pairings for a pass (a missing or
// unreadable file is an empty map: every item is then mapped afresh, which
// gives the same names it gave before).
func (e *Engine) loadNames() {
	saved, _ := e.Store.LoadNames(e.Pair.ID)
	e.names = newNameMap(saved)
}

// saveNames writes the pairings back when the pass changed them.
func (e *Engine) saveNames() error {
	if e.names == nil || !e.names.dirty {
		return nil
	}
	if err := e.Store.SaveNames(e.Pair.ID, e.names.toLocal); err != nil {
		return err
	}
	e.names.dirty = false
	return nil
}

// mapNames turns one pass's server snapshot into this computer's names
// (see the top of this file), and takes out of the pass what cannot be
// synced: a server item whose alias is taken here, a local item whose name
// the server refuses, a Windows image of a server item. full says the server
// snapshot is the pair's whole tree, so a pairing whose server item is gone
// is forgotten.
func (e *Engine) mapNames(local, remote Snapshot, base Baseline, full bool) (Snapshot, Snapshot, Baseline, []NameReport) {
	goos := e.goos()
	if e.names == nil {
		e.names = newNameMap(nil)
	}
	nm := e.names
	var reports []NameReport

	localKids := map[string][]string{}
	for rel := range local {
		localKids[parentDir(rel)] = append(localKids[parentDir(rel)], rel)
	}
	byParent := map[string][]string{}
	for rel := range remote {
		byParent[parentDir(rel)] = append(byParent[parentDir(rel)], rel)
	}
	parents := make([]string, 0, len(byParent))
	for p := range byParent {
		parents = append(parents, p)
	}
	// Shallow first: a folder's name here is known before its contents'.
	sort.Slice(parents, func(i, j int) bool {
		di, dj := depthOf(parents[i]), depthOf(parents[j])
		if di != dj {
			return di < dj
		}
		return parents[i] < parents[j]
	})

	toLocal := map[string]string{} // every kept server item → its rel here
	gone := map[string]bool{}      // server items left out (and, by prefix, their contents)
	goneUnder := func(r string) bool {
		for p := r; p != ""; p = parentDir(p) {
			if gone[p] {
				return true
			}
		}
		return false
	}
	claimedLocal := map[string]bool{}
	localOut := map[string]bool{} // items here left out (and their contents)

	for _, pr := range parents {
		// The folder's name here: mapped above, or - a folder outside this
		// listing (a targeted pass) - through the kept pairings.
		pl := ""
		if pr != "" {
			l, mappedHere := toLocal[pr]
			_, listed := remote[pr]
			switch {
			case goneUnder(pr), listed && !mappedHere:
				for _, r := range byParent[pr] {
					gone[r] = true
				}
				continue
			case mappedHere:
				pl = l
			default:
				pl = translate(nm.toLocal, pr)
			}
		}
		kids := byParent[pr]
		sort.Strings(kids)

		// This folder's names here, by the key this file system compares.
		// (Sorted, first kept: a disk that does not fold may hold two.)
		here := map[string]string{}
		mine := localKids[pl]
		sort.Strings(mine)
		for _, lr := range mine {
			if k := localname.Key(path.Base(lr), goos); here[k] == "" {
				here[k] = lr
			}
		}
		claimed := map[string]bool{}
		assign := func(r, l string) {
			toLocal[r] = l
			claimedLocal[l] = true
			claimed[localname.Key(path.Base(l), goos)] = true
			if path.Base(l) != path.Base(r) {
				nm.set(r, l)
			} else {
				nm.drop(r)
			}
		}
		join := func(name string) string {
			if pl == "" {
				return name
			}
			return pl + "/" + name
		}
		// An item synced under another name is said on every pass, unless
		// the two are one name here anyway (`Rapor.docx` held as `rapor.docx`
		// on Windows: the same file, nothing to tell).
		said := func(r, l string) {
			name := path.Base(r)
			if localname.Key(path.Base(l), goos) == localname.Key(name, goos) {
				return
			}
			why := localname.Problem(name, goos)
			if why == "" {
				why = WhyTwin
			}
			reports = append(reports, NameReport{Rel: r, Reason: ReasonSyncedAs, Other: l, Why: why})
		}

		// 1. Pairings kept from earlier passes: the same name every pass.
		var rest []string
		for _, r := range kids {
			if l, ok := nm.toLocal[r]; ok && parentDir(l) == pl &&
				localname.Problem(path.Base(l), goos) == "" && !claimed[localname.Key(path.Base(l), goos)] {
				assign(r, l)
				said(r, l)
				continue
			}
			rest = append(rest, r)
		}
		// 2. Every other item under its own name, or under the spelling an
		// item here already has. An item already here, then one the baseline
		// knows, chooses first: neither becomes a change by losing its name.
		rank := func(r string) int {
			name := path.Base(r)
			if _, ok := local[join(name)]; ok {
				return 0
			}
			if _, ok := base[join(name)]; ok {
				return 1
			}
			return 2
		}
		sort.SliceStable(rest, func(i, j int) bool { return rank(rest[i]) < rank(rest[j]) })
		var deferred []string
		for _, r := range rest {
			name := path.Base(r)
			k := localname.Key(name, goos)
			if claimed[k] {
				deferred = append(deferred, r)
				continue
			}
			if _, ok := local[join(name)]; ok && !claimedLocal[join(name)] {
				assign(r, join(name))
				continue
			}
			if lr, ok := here[k]; ok && !claimedLocal[lr] {
				assign(r, lr)
				continue
			}
			if localname.Problem(name, goos) == "" {
				assign(r, join(name))
				continue
			}
			deferred = append(deferred, r)
		}
		// 3. An alias for what is left.
		for _, r := range deferred {
			name := path.Base(r)
			// What an earlier version made of it here (`report` for
			// `report.`), when it is of the same kind: that IS this item.
			if img, ok := localname.Image(name, goos); ok {
				if lr, found := here[localname.Key(img, goos)]; found && !claimedLocal[lr] &&
					!claimed[localname.Key(img, goos)] && local[lr].IsDir == remote[r].IsDir {
					assign(r, lr)
					said(r, lr)
					continue
				}
			}
			alias := localname.Alias(name, goos)
			for n := 2; claimed[localname.Key(alias, goos)]; n++ {
				alias = localname.Numbered(localname.Alias(name, goos), n, goos)
			}
			if lr, taken := here[localname.Key(alias, goos)]; taken && !claimedLocal[lr] {
				gone[r] = true
				nm.drop(r)
				reports = append(reports, NameReport{Rel: r, Reason: ReasonTaken, Other: lr})
				continue
			}
			assign(r, join(alias))
			said(r, join(alias))
		}
		// 4. A Windows image of a server item that cannot be that item (a
		// folder `back` for the file `back\slash.txt`) stays here, unsynced.
		for _, r := range kids {
			img, ok := localname.Image(path.Base(r), goos)
			if !ok {
				continue
			}
			if lr, found := here[localname.Key(img, goos)]; found && !claimedLocal[lr] && !localOut[lr] {
				localOut[lr] = true
				reports = append(reports, NameReport{Rel: lr, Reason: ReasonImage, Other: r})
			}
		}
	}

	if full {
		for r := range nm.toLocal {
			if _, ok := toLocal[r]; !ok {
				nm.drop(r)
			}
		}
	}

	// Items here the server would refuse to create: not uploaded. An item
	// paired with a server item has its server name and is judged in
	// serverRefused, after the plan.
	locals := make([]string, 0, len(local))
	for rel := range local {
		locals = append(locals, rel)
	}
	sort.Slice(locals, func(i, j int) bool {
		if di, dj := depthOf(locals[i]), depthOf(locals[j]); di != dj {
			return di < dj
		}
		return locals[i] < locals[j]
	})
	outUnder := func(rel string) bool {
		for p := rel; p != ""; p = parentDir(p) {
			if localOut[p] {
				return true
			}
		}
		return false
	}
	for _, rel := range locals {
		if claimedLocal[rel] || outUnder(rel) {
			continue
		}
		if leafname.Check(path.Base(rel)) != nil {
			localOut[rel] = true
			reports = append(reports, NameReport{Rel: rel, Reason: ReasonServerRefuses})
		}
	}

	mapped := make(Snapshot, len(remote))
	for r, n := range remote {
		if gone[r] || goneUnder(r) {
			continue
		}
		l, ok := toLocal[r]
		if !ok {
			continue
		}
		n.Rel = l
		mapped[l] = n
	}
	keptLocal := local
	keptBase := base
	if len(localOut) > 0 {
		keptLocal = make(Snapshot, len(local))
		for rel, n := range local {
			if !outUnder(rel) {
				keptLocal[rel] = n
			}
		}
		keptBase = make(Baseline, len(base))
		for rel, row := range base {
			if !outUnder(rel) {
				keptBase[rel] = row
			}
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Rel < reports[j].Rel })
	return keptLocal, mapped, keptBase, reports
}

// serverRefused takes out of a plan the uploads the server would refuse for
// their name: an item here paired with a server item whose own name the
// server does not accept any more (internal/leafname - a control character,
// say, in a name an older server took). The file stays as it is on both
// sides; the change here is reported on every pass, never sent to fail.
func (e *Engine) serverRefused(actions []Action) ([]Action, []NameReport) {
	var reports []NameReport
	out := actions[:0]
	for _, a := range actions {
		switch a.Kind {
		case ActionUpload, ActionMkdirRemote, ActionConflict:
			if a.Kind == ActionConflict && a.Mixed {
				break
			}
			if leafname.Check(path.Base(e.names.remoteRel(a.Rel))) != nil {
				reports = append(reports, NameReport{Rel: a.Rel, Reason: ReasonServerRefuses})
				continue
			}
		}
		out = append(out, a)
	}
	return out, reports
}

func depthOf(rel string) int {
	if rel == "" {
		return -1
	}
	return strings.Count(rel, "/")
}

// localSpelled reports whether rel, a folder of the pair, is on this computer
// the folder of exactly that name: every segment a name this computer can
// hold, and - where its file system folds names - present under that exact
// spelling or not present at all.
//
// ⚠ A targeted pass lists ONE folder on each side. `docs` announced by the
// server while this disk holds its twin `Docs` would be listed here as
// `Docs`'s contents and compared with the server's `docs`: every file of one
// twin would look new to the other. Such a folder is reconciled from its
// parent instead, where mapNames sees the twins side by side.
func localSpelled(root, rel, goos string) bool {
	dir := root
	for _, seg := range strings.Split(rel, "/") {
		if localname.Problem(seg, goos) != "" {
			return false
		}
		if localname.Folds(goos) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				// Missing or unreadable: the pass's own checks decide.
				return true
			}
			exact, twin := false, false
			for _, d := range entries {
				if d.Name() == seg {
					exact = true
					break
				}
				if localname.Key(d.Name(), goos) == localname.Key(seg, goos) {
					twin = true
				}
			}
			if !exact {
				return !twin
			}
		}
		dir = filepath.Join(dir, seg)
	}
	return true
}
