package wasmplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// ── Versions: the one an upgrade replaced, kept to go back to ───────────
//
// ⚠⚠ Owner's rule (2026-09-27): NOTHING updates itself — an app with or
// without an interface, a language pack, a storage plugin. The daily check
// says a newer version is there (updates.go); an administrator approves it;
// everybody uses the same version. And an approved version can be undone:
// the files it replaced are moved into <dir>/_versions/<name>/<folder>/ and
// recorded (app_plugin_versions, migration 00066) with the grant they ran
// under, so "Back to <version>" puts them back WITHOUT asking again — that
// grant was approved when that version was installed.
//
// One previous version is kept per app. It is also what an interface that is
// still open in somebody's tab keeps loading from (its address carries its
// bundle's hash): an upgrade does not pull a page out from under a person.

// versionsDirName is the folder of kept versions beside the apps. An app's
// name cannot start with `_` (nameRe), so it can never be an app's folder.
const versionsDirName = "_versions"

// keepVersions is how many replaced versions are kept per app.
const keepVersions = 1

var safeVersionRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (r *Registry) versionsRoot(name string) string {
	return filepath.Join(r.opts.Dir, versionsDirName, name)
}

// PreviousVersion is the kept version an administrator can go back to.
type PreviousVersion struct {
	Version    string    `json:"version"`
	ReplacedAt time.Time `json:"replaced_at"`
	// Message says it in the reader's language, on the reader's clock
	// (handlers sayStatus, 0.55).
	Message string `json:"message,omitempty"`
	// UI: the kept version has an interface of its own.
	UI bool `json:"ui,omitempty"`
}

// prevState is what an Installed knows about its kept version: the row, and
// the interface bundle loaded from it on first use (for tabs still open on
// it).
type prevState struct {
	mu  sync.Mutex
	row *model.AppPluginVersion
	ui  *uiBundle
}

func (p *Installed) previous() *model.AppPluginVersion {
	if p.prev == nil {
		return nil
	}
	p.prev.mu.Lock()
	defer p.prev.mu.Unlock()
	return p.prev.row
}

func (p *Installed) setPrevious(v *model.AppPluginVersion) {
	if p.prev == nil {
		p.prev = &prevState{}
	}
	p.prev.mu.Lock()
	p.prev.row, p.prev.ui = v, nil
	p.prev.mu.Unlock()
}

// previousInfo is the kept version as the list shows it.
func (p *Installed) previousInfo() *PreviousVersion {
	v := p.previous()
	if v == nil {
		return nil
	}
	return &PreviousVersion{Version: v.Version, ReplacedAt: v.ReplacedAt, UI: v.UISHA256 != ""}
}

// loadPrevious reads the kept version of p (Load, and after every upgrade).
func (r *Registry) loadPrevious(ctx context.Context, p *Installed) {
	vs, err := r.opts.Store.ListAppPluginVersions(ctx, p.Row.ID)
	if err != nil || len(vs) == 0 {
		p.setPrevious(nil)
		return
	}
	p.setPrevious(vs[0])
}

// keepPrevious moves the files an upgrade replaced (the stash at backup) into
// the versions folder, records them with the grant they ran under, and drops
// what is older than keepVersions. Failure to keep them costs the roll-back,
// never the upgrade: the stash is removed and the upgrade stands.
func (r *Registry) keepPrevious(ctx context.Context, old *model.AppPlugin, backup string, by *int64) {
	root := r.versionsRoot(old.Name)
	folder := fmt.Sprintf("%d-%s", time.Now().UnixNano(), safeVersionRe.ReplaceAllString(old.Version, "_"))
	if err := os.MkdirAll(root, 0o700); err == nil {
		err = os.Rename(backup, filepath.Join(root, folder))
		if err != nil {
			r.log.Warn("app-plugins: the replaced version could not be kept", slog.String("plugin", old.Name), slog.Any("err", err))
			_ = os.RemoveAll(backup)
			return
		}
	} else {
		_ = os.RemoveAll(backup)
		return
	}
	row := &model.AppPluginVersion{
		PluginID: old.ID, Version: old.Version, ManifestJSON: old.ManifestJSON, WasmPath: old.WasmPath,
		SHA256: old.SHA256, UISHA256: old.UISHA256, PermissionsJSON: old.PermissionsJSON,
		Source: old.Source, SourceURL: old.SourceURL, ManifestURL: old.ManifestURL,
		Signed: old.Signed, Signature: old.Signature, Dir: folder, ReplacedBy: by,
	}
	if _, err := r.opts.Store.CreateAppPluginVersion(ctx, row); err != nil {
		r.log.Warn("app-plugins: the replaced version could not be recorded", slog.String("plugin", old.Name), slog.Any("err", err))
		_ = os.RemoveAll(filepath.Join(root, folder))
		return
	}
	vs, err := r.opts.Store.ListAppPluginVersions(ctx, old.ID)
	if err != nil {
		return
	}
	for i, v := range vs {
		if i < keepVersions {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, v.Dir))
		_ = r.opts.Store.DeleteAppPluginVersion(ctx, v.ID)
	}
}

// Rollback puts the kept version of an app back: its module, manifest,
// interface and mirrored files, under the grant it ran under — no new
// approval, because that grant was approved when it was installed. The
// version it replaces becomes the kept one, so the administrator can go
// forward again the same way. `by` is the administrator.
func (r *Registry) Rollback(ctx context.Context, id int64, by *int64, lang string) (*Status, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	v := p.previous()
	if v == nil {
		return nil, installErr(ErrCodeNotFound, p.Row.Name+" has no earlier version kept to go back to")
	}
	in, err := r.keptInput(p.Row.Name, v)
	if err != nil {
		return nil, err
	}
	in.ActorID, in.Lang, in.Rollback = by, lang, true
	st, _, err := r.Upgrade(ctx, id, in)
	return st, err
}

// keptInput reads a kept version back as an install: every byte held to the
// hash recorded when it was replaced.
func (r *Registry) keptInput(name string, v *model.AppPluginVersion) (*InstallInput, error) {
	dir := filepath.Join(r.versionsRoot(name), v.Dir)
	manifest, err := os.ReadFile(filepath.Join(dir, "filex-app.json"))
	if err != nil {
		return nil, installErr(ErrCodeNotFound, "the kept version's files are gone: "+err.Error())
	}
	if string(manifest) != v.ManifestJSON {
		return nil, installErr(ErrCodeSHA256Mismatch, "the kept version's manifest is not the one it ran with")
	}
	var granted []string
	_ = json.Unmarshal([]byte(v.PermissionsJSON), &granted)
	in := &InstallInput{
		Manifest: manifest, SHA256: v.SHA256, Signature: v.Signature, Granted: granted,
		Source: v.Source, SourceURL: v.SourceURL, ManifestURL: v.ManifestURL, UIPin: v.UISHA256,
	}
	if v.WasmPath != "" {
		wasm, err := os.ReadFile(filepath.Join(dir, v.WasmPath))
		if err != nil {
			return nil, installErr(ErrCodeNotFound, "the kept version's module is gone: "+err.Error())
		}
		in.Wasm = strings.NewReader(string(wasm))
	}
	if v.UISHA256 != "" {
		ui, err := os.ReadFile(filepath.Join(dir, uiZipName))
		if err != nil {
			return nil, installErr(ErrCodeNotFound, "the kept version's interface is gone: "+err.Error())
		}
		in.UI = strings.NewReader(string(ui))
		in.Mirrors = map[string][]byte{}
		if entries, err := os.ReadDir(filepath.Join(dir, uiExtDir)); err == nil {
			for _, e := range entries {
				if b, err := os.ReadFile(filepath.Join(dir, uiExtDir, e.Name())); err == nil {
					in.Mirrors[e.Name()] = b
				}
			}
		}
	}
	return in, nil
}

// previousUIBundle is the kept version's interface when an address names it
// (a tab still open on it), with the grant it ran under; nil otherwise.
func (r *Registry) previousUIBundle(p *Installed, short string) (*uiBundle, Grants) {
	if p.prev == nil {
		return nil, nil
	}
	p.prev.mu.Lock()
	defer p.prev.mu.Unlock()
	v := p.prev.row
	if v == nil || len(v.UISHA256) < uiShortLen || v.UISHA256[:uiShortLen] != short {
		return nil, nil
	}
	var granted []string
	_ = json.Unmarshal([]byte(v.PermissionsJSON), &granted)
	perms := make([]Permission, 0, len(granted))
	for _, g := range granted {
		if pm, err := ParsePermission(g); err == nil {
			perms = append(perms, pm)
		}
	}
	if p.prev.ui == nil {
		m, err := ParseManifest([]byte(v.ManifestJSON))
		if err != nil {
			return nil, nil
		}
		b, err := loadUIBundle(filepath.Join(r.versionsRoot(p.Row.Name), v.Dir), p.Row.Name, v.UISHA256, m)
		if err != nil {
			return nil, nil
		}
		p.prev.ui = b
	}
	return p.prev.ui, NewGrants(perms)
}

// UpgradeListener hears every approved version change (an upgrade, a
// roll-back): the explorers that are open tell their interfaces to reload.
type UpgradeListener func(app, version string)

// SetUpgradeListener wires the listener (the realtime hub).
func (r *Registry) SetUpgradeListener(f UpgradeListener) { r.upgraded = f }

// afterUpgrade writes the audit row of an approved version change, rings the
// administrators' bell and tells the open explorers.
func (r *Registry) afterUpgrade(ctx context.Context, p *Installed, old *model.AppPlugin, in *InstallInput, up *DryRunUpgrade) {
	action := "app_plugin.upgrade"
	if in.Rollback {
		action = "app_plugin.rollback"
	}
	meta := map[string]any{"plugin": p.Row.Name, "from": old.Version, "to": p.Row.Version, "source": p.Row.Source}
	if up != nil {
		meta["added"], meta["removed"] = up.Added, up.Removed
	}
	if old.UISHA256 != p.Row.UISHA256 {
		meta["ui_from"], meta["ui_to"] = old.UISHA256, p.Row.UISHA256
	}
	_ = r.opts.Store.InsertAuditEntry(context.WithoutCancel(ctx), &model.AuditEntry{
		UserID: in.ActorID, Action: action, TargetType: "app_plugin", TargetID: p.Row.Name, Metadata: meta,
	})
	if r.notify != nil {
		nm := labelMeta(p.Row.Name, p.Manifest.Label)
		nm["version"], nm["from"] = p.Row.Version, old.Version
		if in.Rollback {
			nm["rollback"] = true
		}
		// No sentence here: the server says it from these facts, in each
		// reader's language (internal/notify say.go, server.notify.app_updated).
		if _, err := r.notify.Send(context.WithoutCancel(ctx), notify.Event{Event: notify.EventAppUpdated, Severity: notify.SeverityInfo,
			Meta: nm}); err != nil {
			r.log.Warn("app-plugins: update notice not sent", slog.String("plugin", p.Row.Name), slog.Any("err", err))
		}
	}
	if r.upgraded != nil {
		r.upgraded(p.Row.Name, p.Row.Version)
	}
}
