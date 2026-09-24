package filesync

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// FingerprintSnapshot digests a snapshot: two equal fingerprints mean the
// same paths with the same signatures. The watcher keeps one per pair to tell,
// with a local walk and no request at all, whether this side changed since
// the last run.
func FingerprintSnapshot(s Snapshot) string {
	sigs := make(map[string]string, len(s))
	for k, n := range s {
		sigs[k] = n.Signature()
	}
	return fingerprintSigs(sigs)
}

// fingerprintSigs is FingerprintSnapshot over rel → signature.
func fingerprintSigs(sigs map[string]string) string {
	keys := make([]string, 0, len(sigs))
	for k := range sigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = io.WriteString(h, k)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, sigs[k])
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// LocalFingerprint walks a pair's local side exactly as a run does and
// fingerprints it. It is the same value a full run reports in
// Result.LocalFingerprint when nothing changed on this machine in between.
func LocalFingerprint(p Pair) (string, error) {
	if !p.File {
		snap, _, err := WalkLocal(p.Local)
		if err != nil {
			return "", err
		}
		return FingerprintSnapshot(snap), nil
	}
	out := Snapshot{}
	info, err := os.Lstat(p.Local)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && info.Mode().IsRegular() {
		name := filepath.Base(p.Local)
		out[name] = Node{Rel: name, Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}
	}
	return FingerprintSnapshot(out), nil
}

// localView is the local side as a full pass leaves it, KNOWN rather than
// walked: the snapshot the plan was made from, with every change the pass
// itself made on this machine applied to it. (Result.LocalFingerprint says why
// it must not be a fresh walk.) A nil view ignores everything — a targeted
// pass keeps none.
type localView struct {
	sigs map[string]string
}

func newLocalView(s Snapshot) *localView {
	lv := &localView{sigs: make(map[string]string, len(s))}
	for k, n := range s {
		lv.sigs[k] = n.Signature()
	}
	return lv
}

// set records what the pass left at rel.
func (lv *localView) set(rel, sig string) {
	if lv == nil || sig == "" {
		return
	}
	lv.sigs[rel] = sig
}

// apply folds one completed action in.
func (lv *localView) apply(a Action, o outcome) {
	if lv == nil {
		return
	}
	switch a.Kind {
	case ActionDownload:
		if o.row != nil {
			lv.sigs[a.Rel] = o.row.Local
		}
	case ActionMkdirLocal:
		lv.sigs[a.Rel] = "dir"
	case ActionDeleteLocal:
		delete(lv.sigs, a.Rel)
	}
}

func (lv *localView) fingerprint() string {
	if lv == nil {
		return ""
	}
	return fingerprintSigs(lv.sigs)
}
