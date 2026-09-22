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
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = io.WriteString(h, k)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, s[k].Signature())
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// LocalFingerprint walks a pair's local side exactly as a run does and
// fingerprints it. It is the same value a run reports in
// Result.LocalFingerprint when nothing changed in between.
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
