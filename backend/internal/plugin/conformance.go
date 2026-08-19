package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Conformance: does the plugin actually do what it says it does?
//
// # Why this exists
//
// A plugin declares its capabilities and filex believes them — it registers a
// driver whose method set matches, and every surface then offers those
// operations. If the plugin declared `write` and its write is broken, the
// user meets an upload button that fails, a trash move that fails and a
// version snapshot that fails, and reads all three as **filex** being broken.
// The plugin is the faulty part, but the product wears the fault.
//
// So a claim is not taken on trust. Every capability a plugin declares is
// PROBED against a scratch area before the plugin is usable:
//
//   - at install and at every start (against the throwaway instance the
//     plugin opens for POST /v1/selftest);
//   - and again whenever a storage on it is saved, against that storage's
//     real configuration, inside a scratch folder that is removed afterwards.
//
// A plugin that fails its own claims is REFUSED, with a report naming the
// probe, what was expected and what happened. Half a plugin is not offered as
// a whole one.
//
// ⚠ What conformance cannot check is stated where it matters rather than
// hidden: a presigned URL is verified to exist and parse, not to be reachable
// from a browser on another network; `watch` is verified to open a stream,
// not to deliver an event for every possible change; and nothing here can
// tell a slow backend from a broken one beyond the timeout.

// ProbeResult is one capability's verdict.
type ProbeResult struct {
	// Name is the capability or behaviour probed ("read", "write", "range"…).
	Name string `json:"name"`
	// Status is pass | fail | skip.
	Status string `json:"status"`
	// Detail says what happened — for a failure, precisely enough to fix it.
	Detail string `json:"detail,omitempty"`
	// Took is how long the probe ran. ⚠ NOT serialised: encoding/json has no
	// marshaller for time.Duration and writes the raw int64, so a field named
	// took_ms carrying a Duration is off by a million — measured by the admin
	// page reading nanoseconds as milliseconds. TookMS below is what goes on
	// the wire, and it really is milliseconds.
	Took   time.Duration `json:"-"`
	TookMS int64         `json:"took_ms"`
}

// Probe statuses.
const (
	ProbePass = "pass"
	ProbeFail = "fail"
	ProbeSkip = "skip"
)

// Report is the outcome of a conformance run.
type Report struct {
	// Verified is true when every declared capability passed.
	Verified bool `json:"verified"`
	// Scratch says where the probes ran: "selftest" (the plugin's own
	// throwaway area) or "storage" (a folder inside a real storage).
	Scratch string        `json:"scratch"`
	Results []ProbeResult `json:"results"`
	RanAt   time.Time     `json:"ran_at"`
}

// Failures returns the probes that failed, most useful first.
func (r *Report) Failures() []ProbeResult {
	var out []ProbeResult
	for _, p := range r.Results {
		if p.Status == ProbeFail {
			out = append(out, p)
		}
	}
	return out
}

// Summary is the one-line form for a log or an error message.
func (r *Report) Summary() string {
	if r == nil {
		return "not verified"
	}
	pass, fail, skip := 0, 0, 0
	for _, p := range r.Results {
		switch p.Status {
		case ProbePass:
			pass++
		case ProbeFail:
			fail++
		default:
			skip++
		}
	}
	s := fmt.Sprintf("%d passed", pass)
	if fail > 0 {
		s += fmt.Sprintf(", %d FAILED", fail)
	}
	if skip > 0 {
		s += fmt.Sprintf(", %d skipped", skip)
	}
	return s
}

// FailureError turns the failures into one error whose message a person can
// act on. Nil when nothing failed.
func (r *Report) FailureError() error {
	fails := r.Failures()
	if len(fails) == 0 {
		return nil
	}
	parts := make([]string, 0, len(fails))
	for _, f := range fails {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Detail))
	}
	return fmt.Errorf("plugin fails its own claims — %s", strings.Join(parts, "; "))
}

// ConformanceTimeout bounds a whole run. A plugin that cannot answer a
// handful of small operations in this long is not one a storage should be
// built on.
const ConformanceTimeout = 90 * time.Second

// scratchPrefix names the folder the storage-side probes work in. Visible on
// purpose: an operator who finds it knows what made it.
const scratchPrefix = ".filex-conformance-"

// RunConformance probes every capability caps declares, using drv as the
// driver under test and base as a directory the probes may write inside.
//
// It never leaves anything behind that it created (best effort — a plugin
// whose delete is broken is exactly what the delete probe reports).
func RunConformance(ctx context.Context, drv storage.Driver, caps Capabilities, base, scratchKind string) *Report {
	ctx, cancel := context.WithTimeout(ctx, ConformanceTimeout)
	defer cancel()

	rep := &Report{Scratch: scratchKind, RanAt: time.Now().UTC()}
	add := func(name string, started time.Time, err error, skip string) {
		took := time.Since(started)
		res := ProbeResult{Name: name, Took: took, TookMS: took.Milliseconds()}
		switch {
		case skip != "":
			res.Status, res.Detail = ProbeSkip, skip
		case err != nil:
			res.Status, res.Detail = ProbeFail, err.Error()
		default:
			res.Status = ProbePass
		}
		rep.Results = append(rep.Results, res)
	}

	join := func(name string) string {
		if base == "" {
			return name
		}
		return path.Join(base, name)
	}

	// ── read: list is the one call every driver must answer ────────────────
	t0 := time.Now()
	_, listErr := drv.List(ctx, base)
	add("list", t0, listErr, "")

	// A backend that cannot even list is not worth probing further; the rest
	// would all fail with the same cause and bury it.
	if listErr != nil {
		rep.Verified = false
		return rep
	}

	// ── not-found must be storage.ErrNotFound, not a 500 ───────────────────
	t0 = time.Now()
	missing := join("does-not-exist-" + randomSuffix())
	_, statErr := drv.Stat(ctx, missing)
	switch {
	case statErr == nil:
		add("not_found", t0, errors.New("Stat on a path that does not exist returned success"), "")
	case errors.Is(statErr, storage.ErrNotFound):
		add("not_found", t0, nil, "")
	default:
		add("not_found", t0, fmt.Errorf("Stat on a missing path answered %v, want storage.ErrNotFound "+
			"(return the SDK's ErrNotFound / the protocol's not_found code, so filex answers 404 rather than 500)", statErr), "")
	}

	w, canWrite := drv.(storage.Writer)
	d, canDelete := drv.(storage.Deleter)

	if !caps.Writable() {
		// A read-only plugin: prove the READ half against something that is
		// already there, and say plainly that the rest was not probed.
		add("write", time.Now(), nil, "plugin does not declare write")
		add("read", time.Now(), nil, "read-only plugin: nothing was created to read back")
		rep.Verified = len(rep.Failures()) == 0
		return rep
	}
	if !canWrite || !canDelete {
		add("write", time.Now(), errors.New("declared write+delete but the driver adapter does not offer them (internal error)"), "")
		rep.Verified = false
		return rep
	}

	// ── write → read → range → delete, on one scratch object ───────────────
	name := join("probe-" + randomSuffix() + ".bin")
	payload := []byte("filex conformance probe: 0123456789abcdefghijklmnopqrstuvwxyz")

	t0 = time.Now()
	writeErr := w.Write(ctx, name, bytes.NewReader(payload), int64(len(payload)))
	add("write", t0, writeErr, "")

	if writeErr == nil {
		t0 = time.Now()
		got, err := readAll(ctx, drv, name)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			// The commonest way a plugin lies: Write answers success and
			// stores nothing. Said plainly, because "not found" on its own
			// sends the author looking at the read path.
			err = errors.New("the object is gone immediately after a Write that reported success — " +
				"the write path accepted the bytes and stored nothing")
		case err != nil:
			err = fmt.Errorf("reading back what was just written failed: %w", err)
		case !bytes.Equal(got, payload):
			err = fmt.Errorf("read back %d bytes, wrote %d, and they differ", len(got), len(payload))
		}
		add("read", t0, err, "")

		// stat must agree with what was written — a size that lies breaks
		// ranged serving, quota accounting and sync in three different ways.
		t0 = time.Now()
		st, err := drv.Stat(ctx, name)
		if errors.Is(err, storage.ErrNotFound) {
			err = errors.New("Stat cannot find an object that was just written successfully")
		}
		if err == nil {
			if st.Size != int64(len(payload)) {
				err = fmt.Errorf("Stat reports size %d for an object of %d bytes", st.Size, len(payload))
			} else if st.Kind != storage.KindFile {
				err = fmt.Errorf("Stat reports kind %q for a file", st.Kind)
			}
		}
		add("stat", t0, err, "")

		// The object must appear in its directory listing.
		t0 = time.Now()
		objs, err := drv.List(ctx, base)
		if err == nil {
			found := false
			for _, o := range objs {
				if path.Base(o.Path) == path.Base(name) || o.Name == path.Base(name) {
					found = true
					break
				}
			}
			if !found {
				err = errors.New("an object that was just written does not appear in its own directory listing")
			}
		}
		add("list_after_write", t0, err, "")

		// Ranged read: filex emulates it when the plugin does not declare it,
		// so this probe is about CORRECTNESS, not speed — a driver that
		// ignores the offset hands corrupt bytes to a video player.
		t0 = time.Now()
		if rr, ok := drv.(storage.RangeReader); ok {
			rc, err := rr.ReadRange(ctx, name, 8, 10)
			if err == nil {
				var buf []byte
				buf, err = io.ReadAll(rc)
				rc.Close()
				if err == nil && !bytes.Equal(buf, payload[8:18]) {
					err = fmt.Errorf("ranged read of bytes 8-17 returned %q, want %q", buf, payload[8:18])
				}
			}
			skip := ""
			if !caps.Range {
				skip = ""
			}
			add("range", t0, err, skip)
		} else {
			add("range", t0, errors.New("the adapter offers no RangeReader (internal error)"), "")
		}

		if caps.SetMtime {
			t0 = time.Now()
			var err error
			tr, ok := drv.(storage.Toucher)
			if !ok {
				err = errors.New("declared set_mtime but the driver offers no Toucher (internal error)")
			} else {
				want := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
				if err = tr.SetMtime(ctx, name, want); err == nil {
					var st storage.Object
					if st, err = drv.Stat(ctx, name); err == nil {
						if diff := st.Mtime.UTC().Sub(want); diff > time.Second || diff < -time.Second {
							err = fmt.Errorf("set_mtime was accepted but Stat still reports %s (want %s) — "+
								"a timestamp that is accepted and dropped makes every sync run copy everything again",
								st.Mtime.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
						}
					}
				}
			}
			add("set_mtime", t0, err, "")
		} else {
			add("set_mtime", time.Now(), nil, "not declared")
		}

		if caps.Copy || caps.Move {
			t0 = time.Now()
			dst := join("probe-moved-" + randomSuffix() + ".bin")
			var err error
			if caps.Copy {
				if cp, ok := drv.(storage.Copier); ok {
					if err = cp.Copy(ctx, name, dst); err == nil {
						var got []byte
						if got, err = readAll(ctx, drv, dst); err == nil && !bytes.Equal(got, payload) {
							err = errors.New("the copy does not have the same bytes as its source")
						}
					}
				} else {
					err = errors.New("declared copy but the driver offers no Copier (internal error)")
				}
				add("copy", t0, err, "")
				_ = d.Delete(ctx, dst)
			} else {
				add("copy", t0, nil, "not declared (filex emulates it as read+write)")
			}

			t0 = time.Now()
			if caps.Move {
				moved := join("probe-moved2-" + randomSuffix() + ".bin")
				if mv, ok := drv.(storage.Mover); ok {
					if err = mv.Move(ctx, name, moved); err == nil {
						if _, serr := drv.Stat(ctx, name); serr == nil {
							err = errors.New("after Move the source still exists")
						} else if !errors.Is(serr, storage.ErrNotFound) {
							err = fmt.Errorf("after Move, Stat on the source answered %v, want storage.ErrNotFound", serr)
						}
						name = moved // keep cleaning up the right object
					}
				} else {
					err = errors.New("declared move but the driver offers no Mover (internal error)")
				}
				add("move", t0, err, "")
			} else {
				add("move", t0, nil, "not declared (filex emulates it as copy+delete)")
			}
		}

		if caps.Mkdir {
			t0 = time.Now()
			dir := join("probe-dir-" + randomSuffix())
			var err error
			if mk, ok := drv.(storage.Mkdirer); ok {
				err = mk.Mkdir(ctx, dir)
			} else {
				err = errors.New("declared mkdir but the driver offers no Mkdirer (internal error)")
			}
			add("mkdir", t0, err, "")
			_ = d.Delete(ctx, dir)
		} else {
			add("mkdir", time.Now(), nil, "not declared (treated as an object store)")
		}

		// ── delete last: it is also the cleanup ────────────────────────────
		t0 = time.Now()
		err = d.Delete(ctx, name)
		if err == nil {
			if _, serr := drv.Stat(ctx, name); serr == nil {
				err = errors.New("the object still exists after Delete")
			} else if !errors.Is(serr, storage.ErrNotFound) {
				err = fmt.Errorf("after Delete, Stat answered %v, want storage.ErrNotFound", serr)
			}
		}
		add("delete", t0, err, "")

		// Deleting something already gone must be a no-op, not an error:
		// trash purge, sync and the ops worker all rely on that.
		t0 = time.Now()
		err = d.Delete(ctx, name)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			err = fmt.Errorf("deleting a path that is already gone answered %v — Delete must be idempotent", err)
		} else {
			err = nil
		}
		add("delete_idempotent", t0, err, "")
	}

	if caps.Presign {
		t0 = time.Now()
		var err error
		pr, ok := drv.(storage.Presigner)
		if !ok {
			err = errors.New("declared presign but the driver offers no Presigner (internal error)")
		} else {
			var link string
			link, err = pr.PresignDownload(ctx, join("anything.bin"), time.Minute)
			if err == nil {
				if u, perr := url.Parse(link); perr != nil || u.Scheme == "" || u.Host == "" {
					err = fmt.Errorf("presign-download returned %q, which is not an absolute URL", link)
				}
			}
		}
		// ⚠ Deliberately not a network check: the URL is meant for the
		// CLIENT's network, which filex may not share.
		add("presign", t0, err, "")
	} else {
		add("presign", time.Now(), nil, "not declared")
	}

	if caps.Multipart {
		t0 = time.Now()
		var err error
		mp, ok := drv.(storage.PartUploader)
		if !ok {
			err = errors.New("declared multipart but the driver offers no PartUploader (internal error)")
		} else {
			target := join("probe-mp-" + randomSuffix() + ".bin")
			var uploadID string
			uploadID, _, err = mp.InitMultipart(ctx, target, int64(len(payload)), 1)
			if err == nil {
				var etag string
				etag, err = mp.UploadPart(ctx, target, uploadID, 1, bytes.NewReader(payload), int64(len(payload)))
				if err == nil {
					err = mp.CompleteMultipart(ctx, target, uploadID, []storage.PartCompletion{{PartNumber: 1, Etag: etag}})
				}
				if err == nil {
					var got []byte
					if got, err = readAll(ctx, drv, target); err == nil && !bytes.Equal(got, payload) {
						err = errors.New("the object assembled from parts does not match what was uploaded")
					}
				}
				if err != nil {
					_ = mp.AbortMultipart(ctx, target, uploadID)
				}
				_ = d.Delete(ctx, target)
			}
		}
		add("multipart", t0, err, "")
	} else {
		add("multipart", time.Now(), nil, "not declared")
	}

	if caps.Watch {
		t0 = time.Now()
		var err error
		wt, ok := drv.(storage.Watcher)
		if !ok {
			err = errors.New("declared watch but the driver offers no Watcher (internal error)")
		} else {
			wctx, cancelWatch := context.WithTimeout(ctx, 10*time.Second)
			var ch <-chan storage.Event
			ch, err = wt.Subscribe(wctx)
			if err == nil && ch == nil {
				err = errors.New("Subscribe returned no channel")
			}
			// The stream only has to OPEN. Requiring an event would mean
			// requiring the plugin to notice a change filex made during a
			// ten-second window, which a polling backend legitimately cannot.
			cancelWatch()
		}
		add("watch", t0, err, "")
	} else {
		add("watch", time.Now(), nil, "not declared")
	}

	sort.SliceStable(rep.Results, func(i, j int) bool {
		rank := map[string]int{ProbeFail: 0, ProbePass: 1, ProbeSkip: 2}
		return rank[rep.Results[i].Status] < rank[rep.Results[j].Status]
	})
	rep.Verified = len(rep.Failures()) == 0
	return rep
}

// VerifyStorage probes a driver against a REAL storage configuration, inside
// a scratch folder it creates and removes.
//
// This is the second gate. The first (a plugin's selftest at install) proves
// the code works; this one proves it works against the configuration an
// operator just typed — the credentials, the bucket, the path. A plugin can
// be perfectly correct and the storage still be unusable, and finding that
// out when the storage is SAVED is much cheaper than finding out when a user
// uploads a file into it.
//
// caps must be the plugin's declared capability set. Returns the report; the
// caller decides what to do with a failure.
func VerifyStorage(ctx context.Context, drv storage.Driver, caps Capabilities) *Report {
	base := scratchPrefix + randomSuffix()

	// A writable driver gets a real folder to work in, so nothing the probes
	// create can collide with the operator's own files.
	if mk, ok := drv.(storage.Mkdirer); ok && caps.Mkdir {
		_ = mk.Mkdir(ctx, base)
	} else if !caps.Writable() {
		// Read-only: probe the storage root, creating nothing.
		base = ""
	}

	rep := RunConformance(ctx, drv, caps, base, "storage")

	if base != "" {
		if d, ok := drv.(storage.Deleter); ok {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = d.Delete(cleanCtx, base)
			cancel()
		}
	}
	return rep
}

func readAll(ctx context.Context, drv storage.Driver, p string) ([]byte, error) {
	rc, err := drv.Read(ctx, p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 1<<20))
}

func randomSuffix() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b)
}
