package notify_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// What a click on each event OPENS — measured at the emitter, not read off the
// documentation table it is supposed to implement.
//
// ⚠⚠ Why these exist. `target_test.go` next door proves the notify SERVICE
// fills in the storage name and refuses half an address; it proves nothing
// about whether an emitter set a target in the first place. An emitter that
// quietly forgets one is invisible: the row still arrives, the bell still
// lists it, it simply does nothing when clicked — which looks exactly like a
// row that genuinely has nowhere to go (`none`). Rule 1 of
// docs/NOTIFICATIONS.md → "The bell, and who can reach it": *an event that
// quietly forgets its target is a bug, not a `none`.*
//
// So each case below runs the REAL emitter and reads the REAL persisted row.
// A future change that drops `Target:` from one of them turns one of these
// red rather than shipping a notification nobody can follow.

// waitForEvent polls until the named event shows up. writehook emits on a
// goroutine (it must never make a file write wait on a webhook fan-out), so
// there is nothing to await — but a bounded poll is not a sleep: it returns
// the moment the row lands.
func waitForEvent(t *testing.T, svc notify.Service, event notify.EventType) *model.Notification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, _, err := svc.List(context.Background(), nil, notify.AdminBell, false, 50, 0)
		require.NoError(t, err)
		for _, r := range rows {
			if r.Event == string(event) {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s row arrived within the deadline", event)
	return nil
}

// TestEmitterTargets_Writehook drives the six write events through the gate
// every write surface goes through, and asserts where each one's click lands.
//
// The pairs that are easy to get backwards, and cost something when they are:
//   - trashed → the Trash VIEW with the item selected, never the original
//     folder the file provably is not in any more, and never the bin's raw
//     `.filex-trash/…` folder (what it used to be, and the owner's report);
//   - deleted → the PARENT folder, because a permanent removal leaves no row
//     to select;
//   - moved → the NEW path: "where is it now" is the only useful answer.
func TestEmitterTargets_Writehook(t *testing.T) {
	cases := []struct {
		name  string
		emit  func(ctx context.Context, storageID, userID int64)
		event notify.EventType
		kind  model.NotificationTargetKind
		path  string
		why   string
	}{
		{
			name: "file.uploaded opens the file",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnFileWritten(ctx, id, &model.Node{
					ID: 0, Path: "Documents/report.pdf", Name: "report.pdf", Type: model.NodeTypeFile,
				}, writehook.OriginManager, writehook.Created)
			},
			event: notify.EventFileUploaded,
			kind:  model.TargetFile,
			path:  "Documents/report.pdf",
			why:   "the file that arrived is the thing to show",
		},
		{
			name: "file.updated opens the file too",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnFileWritten(ctx, id, &model.Node{
					Path: "Documents/report.pdf", Name: "report.pdf", Type: model.NodeTypeFile,
				}, writehook.OriginOnlyOffice, writehook.Replaced)
			},
			event: notify.EventFileUpdated,
			kind:  model.TargetFile,
			path:  "Documents/report.pdf",
			why:   "an edited file is still a file you can be shown",
		},
		{
			name: "file.moved opens the NEW path",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnFileMoved(ctx, id, "old/a.txt", "new/b.txt", "b.txt", writehook.OriginManager)
			},
			event: notify.EventFileMoved,
			kind:  model.TargetFile,
			path:  "new/b.txt",
			why:   "\"where is it now\" is the only useful answer to a move",
		},
		{
			// ⚠⚠ This case used to pin `{kind: file, path: ".filex-trash/…"}`
			// — the bug itself: a click opened the bin's raw folder, breadcrumb
			// `qldemo › .filex-trash`, nothing listed and nothing to restore
			// (owner's report, 2026-09-21). The Trash VIEW lists items by the
			// path they came from, so that is the only address it has.
			name: "file.trashed opens the Trash view on the item, by its original path",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnFileTrashed(ctx, id, "Docs/gone.txt", "gone.txt",
					".filex-trash/1726-abcdef__gone.txt", writehook.OriginManager)
			},
			event: notify.EventFileTrashed,
			kind:  model.TargetTrash,
			path:  "Docs/gone.txt",
			why:   "the trash key is a path nothing serves; the Trash view lists the item under where it lived",
		},
		{
			name: "file.trashed with no trash path still opens the Trash view",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnFileTrashed(ctx, id, "Docs/gone.txt", "gone.txt", "", writehook.OriginDAV)
			},
			event: notify.EventFileTrashed,
			kind:  model.TargetTrash,
			path:  "Docs/gone.txt",
			why:   "the Trash view is addressed by the original path, which every surface does pass",
		},
		{
			name: "file.deleted opens the parent folder",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnFileDeleted(ctx, id, "Docs/gone.txt", "gone.txt", writehook.OriginOps)
			},
			event: notify.EventFileDeleted,
			kind:  model.TargetDir,
			path:  "Docs",
			why:   "a permanent removal leaves no row to select",
		},
		{
			name: "file.upload_failed opens the folder the bytes were headed for",
			emit: func(ctx context.Context, id, uid int64) {
				writehook.OnUploadFailed(ctx, id, uid, "Inbox/big.iso", "big.iso",
					writehook.OriginManager, "storage refused the write")
			},
			event: notify.EventFileUploadFailed,
			kind:  model.TargetDir,
			path:  "Inbox",
			why:   "the bytes never landed; the folder is where the person retries",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, store := dbtest.NewTestDB(t)
			st := newStorage(t, store, "qldemo")
			svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
			defer svc.Stop()
			writehook.Configure(nil, svc)
			defer writehook.Configure(nil, nil)

			// ⚠ A REAL user row. `OnUploadFailed` scopes its event to the
			// person who uploaded, and the notifications table has a foreign
			// key to users — so a made-up id makes the INSERT fail inside the
			// emit goroutine, where the error is logged and nothing else
			// happens. The test would then read "no row arrived" and look like
			// a missing target.
			u, uerr := store.CreateUser(context.Background(), "uploader@example.test", "x", "user", "en", "UTC")
			require.NoError(t, uerr)

			tc.emit(context.Background(), st.ID, u.ID)

			row := waitForEvent(t, svc, tc.event)
			require.NotNil(t, row.Target, "%s carries no target at all — %s", tc.event, tc.why)
			assert.Equal(t, tc.kind, row.Target.Kind, tc.why)
			assert.Equal(t, tc.path, row.Target.Path, tc.why)
			// The storage NAME, every time: a client cannot turn an id into
			// one (/api/admin/storages is admin-only).
			assert.Equal(t, "qldemo", row.Target.Storage)
		})
	}
}

// TestEmitterTargets_NoneIsAnAnswer — the events that HONESTLY cannot name a
// place say so, and the honesty is worth a test of its own.
//
// ⚠ The five replica events carry a path and nothing else (`internal/replica`):
// a bare path does not name a storage, and guessing which storage it belongs
// to would send a click into another tenant's folder whenever two storages
// share a folder name. `none` there is the right answer, not a gap — which is
// exactly why it must be pinned, so that nobody "fixes" it into a guess.
func TestEmitterTargets_NoneIsAnAnswer(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	for _, ev := range []notify.EventType{
		notify.EventReplicaFail,
		notify.EventPrimaryReadFail,
		notify.EventReplicaReconcileDone,
		notify.EventReplicaStatusReport,
		notify.EventUpdateAvailable,
	} {
		id, err := svc.Send(context.Background(), notify.Event{
			Event:    ev,
			Severity: notify.SeverityWarning,
			// What these emitters really carry: a path in meta, no node.
			Meta: map[string]any{"path": "fileman/x"},
		})
		require.NoError(t, err)
		row, err := store.GetNotification(context.Background(), id)
		require.NoError(t, err)
		assert.Nil(t, model.TargetFromMeta(row.MetaJSON),
			"%s must not invent a location out of a bare path", ev)
	}
}

// TestEmitterTargets_PluginNoticeCarriesItsScreen — an app plugin's notice
// addresses a FILE plus one of that app's own screens, and the screen has to
// survive being stored and read back.
//
// ⚠⚠ This is the difference between "a file you have nothing to do with
// changed" and "please sign this". A `plugin.notice` that lands somebody in a
// folder has made them find the signing screen themselves, which for a signer
// is where the flow stops.
func TestEmitterTargets_PluginNoticeCarriesItsScreen(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st := newStorage(t, store, "qldemo")
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	target := notify.FileTarget("depo/contract.pdf")
	target.Open = &model.NotificationOpen{Plugin: "sign", Action: "sign-request"}

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventPluginNotice,
		Severity: notify.SeverityInfo,
		Title:    "Signature requested",
		Node:     &notify.NodeRef{StorageID: st.ID, Path: "/depo/contract.pdf", Name: "contract.pdf"},
		Target:   target,
		Meta:     map[string]any{"plugin": "sign"},
	})
	require.NoError(t, err)

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	got := model.TargetFromMeta(row.MetaJSON)
	require.NotNil(t, got)
	assert.Equal(t, model.TargetFile, got.Kind)
	assert.Equal(t, "qldemo", got.Storage)
	assert.Equal(t, "depo/contract.pdf", got.Path)
	require.NotNil(t, got.Open, "the app screen was dropped on the way to the database")
	assert.Equal(t, "sign", got.Open.Plugin)
	assert.Equal(t, "sign-request", got.Open.Action)
}

// An app's HOME page, at a section: no storage, no path — the plugin, the
// view and the section survive being stored and read back, and half of one
// is no address at all (2026-09-21: the signing app's requester notices
// open its Signatures page at "I asked for these").
func TestEmitterTargets_AppHomePageAtASection(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventPluginNotice, Severity: notify.SeverityInfo, Title: "Signed",
		Target: notify.AppTarget("sign", "envelopes", "requested"),
		Meta:   map[string]any{"plugin": "sign"},
	})
	require.NoError(t, err)
	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	got := model.TargetFromMeta(row.MetaJSON)
	require.NotNil(t, got)
	assert.Equal(t, model.TargetApp, got.Kind)
	assert.Empty(t, got.Storage)
	require.NotNil(t, got.Open)
	assert.Equal(t, "sign", got.Open.Plugin)
	assert.Equal(t, "envelopes", got.Open.View)
	assert.Equal(t, "requested", got.Open.Section)

	half, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventPluginNotice, Severity: notify.SeverityInfo, Title: "Half",
		Target: &notify.Target{Kind: notify.TargetApp, Open: &model.NotificationOpen{Plugin: "sign"}},
		Meta:   map[string]any{"plugin": "sign"},
	})
	require.NoError(t, err)
	row, err = store.GetNotification(context.Background(), half)
	require.NoError(t, err)
	if g := model.TargetFromMeta(row.MetaJSON); g != nil {
		assert.Equal(t, model.TargetNone, g.Kind, "a page with no view is not an address")
	}
}

// ── the guard ─────────────────────────────────────────────────────────────

// TestEveryEmitterAboutSomethingNamesIt is the rule rather than the roll call.
//
// ⚠⚠ The cases above pin the emitters that exist TODAY. This one pins the
// next one: any `notify.Event{…}` that carries a `Node` or a `Share` is an
// event ABOUT a file, a folder or a link — so it can name a place, and rule 1
// says it must. The failure mode this catches is silent by construction: the
// notification still arrives, still lists, and simply does nothing when
// clicked, which is indistinguishable from an event that genuinely has
// nowhere to go.
//
// A `Target:` set on the value AFTER the literal counts (antivirus does that —
// it picks the trash path or the original depending on whether quarantine
// worked), so the whole function body is searched, not just the literal.
func TestEveryEmitterAboutSomethingNamesIt(t *testing.T) {
	root := backendRootFromTest(t)
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // not our business to fail on unparseable Go
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			// Does this function build an Event with a Node or a Share?
			about, assignsTarget, litHasTarget := false, false, false
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				switch v := m.(type) {
				case *ast.CompositeLit:
					if !isNotifyEventLit(v) {
						return true
					}
					for _, el := range v.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						switch key.Name {
						case "Node", "Share":
							about = true
						case "Target":
							litHasTarget = true
						}
					}
				case *ast.AssignStmt:
					for _, lhs := range v.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Target" {
							assignsTarget = true
						}
					}
				}
				return true
			})
			if about && !litHasTarget && !assignsTarget {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, fmt.Sprintf("%s:%d (%s)",
					filepath.ToSlash(rel), fset.Position(fn.Pos()).Line, fn.Name.Name))
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)

	if len(offenders) > 0 {
		t.Fatalf("these emitters build an event ABOUT a file/folder/link and name no place to open;\n"+
			"a notification that cannot be followed is half a notification (docs/NOTIFICATIONS.md →\n"+
			"\"Click target\"). Set Target: with FileTarget / DirTarget / ParentDirTarget / ShareTarget:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// isNotifyEventLit recognises `notify.Event{…}` and, inside the notify
// package itself, a bare `Event{…}`.
func isNotifyEventLit(v *ast.CompositeLit) bool {
	switch t := v.Type.(type) {
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == "notify" && t.Sel.Name == "Event"
	case *ast.Ident:
		return t.Name == "Event"
	}
	return false
}

// backendRootFromTest finds `backend/internal` from this file's own location —
// the same trick catalog_test.go uses, spelled out here because that one lives
// in the `notify` package and this file is in `notify_test`.
func backendRootFromTest(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	// …/backend/internal/notify/emitter_target_test.go → …/backend/internal
	return filepath.Dir(filepath.Dir(self))
}
