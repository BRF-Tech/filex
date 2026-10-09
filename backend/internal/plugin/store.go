package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Storage plugins from an app store (#215) ────────────────────────────
//
// A store lists a storage plugin from its release's filex-storage.json: it
// pins the feed by sha256, downloads and pins every build, has its own
// plugin validator run filex's conformance probes on one of them, and signs
// every build - its name, version, platform and sha256 (signature.go
// VerifyBuild) - with its artifact key. The install link (signed with
// the store's index key, internal/appstore) carries those pins. What filex
// does with them is its ordinary "from its source" install, held to the
// store's pins instead of the source's latest release:
//
//   - ReadPinnedFeed reads the very feed the store reviewed (the release's,
//     not "latest") and refuses other bytes;
//   - InstallBuild / UpgradeBuild download this platform's build, hold it to
//     the pinned sha256 before anything runs, and install or upgrade it
//     through the same staged path every binary takes (the signature check,
//     the conformance gate at start, the roll-back of a failed upgrade);
//   - the plugin's source is kept as its repository, so the daily check
//     keeps announcing newer releases.
//
// A paid plugin's license is the store's (internal/appstore): a license that
// does not hold HOLDS the plugin (SetLicenseHold) - nothing runs, its storages
// do not open, nothing is removed.

// Platform is the GOOS/GOARCH key a build is chosen by ("linux/amd64").
func (m *Manager) Platform() string { return m.platform() }

// JudgeRange judges a feed's `filex` range against this filex (the hook the
// server wires, wasmplugin.JudgeFilexRange): ok, the range as filex reads it,
// and this filex's version. No hook or an empty range: ok.
func (m *Manager) JudgeRange(rng string) (ok bool, requires, filex string, err error) {
	rng = strings.TrimSpace(rng)
	judge := m.hooks().Range
	if rng == "" || judge == nil {
		return true, rng, "", nil
	}
	return judge(rng)
}

// ErrFeedChanged: the feed at a pinned address is not the one pinned.
var ErrFeedChanged = errors.New("the feed is not the one the store reviewed")

// ReadPinnedFeed reads the feed at addr through the guarded client and holds
// its bytes to sha (lower-case hex) before reading a word of it.
func (m *Manager) ReadPinnedFeed(ctx context.Context, addr, sha string) (*Feed, error) {
	addr = strings.TrimSpace(addr)
	if !strings.HasPrefix(addr, "https://") {
		return nil, reject("a feed is read from an https address, not %q", addr)
	}
	body, err := m.get(ctx, addr, maxFeedBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FeedFileName, err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, strings.TrimSpace(sha)) {
		return nil, RejectedError{fmt.Errorf("%w (sha256 %s, pinned %s)", ErrFeedChanged, short(got), short(sha))}
	}
	return parseFeed(body)
}

// ByName answers the installed plugin called name, or nil.
func (m *Manager) ByName(ctx context.Context, name string) (*Status, error) {
	row, err := m.store.GetPluginByName(ctx, name)
	if err != nil || row == nil {
		return nil, nil
	}
	e, err := m.entryFor(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return m.statusOf(ctx, e), nil
}

// StoreLink is what a store's signed install link says of a build beyond its
// file: the store whose link it is (its origin) and the version the link
// names - what the build's signature is checked as (VerifyBuild), and the
// store the build gate lets through without asking (BuildGate).
type StoreLink struct {
	Store   string
	Version string
}

// pickSignature chooses what an install from a store keeps as the build's
// signature: on an instance that requires signatures, the first of sigs that
// verifies against FILEX_PLUGIN_TRUSTED_KEYS for this name, the link's
// version, this platform and the build's sha256 (the store's artifact key
// first, then the publisher's own) and that the build gate lets through, and
// a refusal naming the rule when none does; elsewhere the first one given,
// kept as the record (it is checked at every start once trusted keys are
// set).
func (m *Manager) pickSignature(ctx context.Context, name string, b FeedBinary, from StoreLink, sigs []string) (string, error) {
	c := BuildClaim{Name: name, Version: from.Version, Platform: m.platform(), SHA256: b.SHA256}
	first := ""
	var firstErr error
	for _, s := range sigs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if first == "" {
			first = s
		}
		if len(m.trusted) == 0 {
			continue
		}
		_, err := m.verifyBuild(ctx, c, from.Store, s)
		if err == nil {
			return s, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if len(m.trusted) == 0 {
		return first, nil
	}
	if firstErr == nil {
		firstErr = signatureRefusal(ErrSignatureRequired)
	}
	return "", firstErr
}

// SignatureVerifies reports whether this instance requires signed plugins
// and, if so, whether one of sigs verifies for this build of name (and the
// build gate lets it through) - what the store review says before anything
// is downloaded.
func (m *Manager) SignatureVerifies(ctx context.Context, name string, b FeedBinary, from StoreLink, sigs []string) (required, verifies bool) {
	required, err := m.StoreSignature(ctx, name, b, from, sigs)
	return required, required && err == nil
}

// StoreSignature is SignatureVerifies with the reason: required, and nil
// when a signature stands for this build; otherwise the refusal - inside it
// ErrStoreBuild when a store's signature verified but the build gate did not
// let it through (the review and the install then say so in their own
// words).
func (m *Manager) StoreSignature(ctx context.Context, name string, b FeedBinary, from StoreLink, sigs []string) (required bool, err error) {
	if len(m.trusted) == 0 {
		return false, nil
	}
	_, err = m.pickSignature(ctx, name, b, from, sigs)
	return true, err
}

// InstallBuild installs a store's build of a plugin as name: b's bytes,
// downloaded through the guarded client and held to b.SHA256 before anything
// runs, with the signature pickSignature chooses; source (the plugin's
// repository, owner/name) is kept so the daily check follows it.
func (m *Manager) InstallBuild(ctx context.Context, name, source string, b FeedBinary, from StoreLink, sigs []string) (*Status, error) {
	if !validName(name) {
		return nil, ErrBadName
	}
	sig, err := m.pickSignature(ctx, name, b, from, sigs)
	if err != nil {
		return nil, err
	}
	st, err := m.installFromURL(ctx, name, b.URL, b.SHA256, sig, buildSource{version: from.Version, via: from.Store})
	if err != nil {
		return st, err
	}
	if strings.TrimSpace(source) == "" {
		return st, nil
	}
	return m.SetSource(ctx, st.ID, source)
}

// UpgradeBuild upgrades plugin id in place to a store's build: the same
// download, pin and signature rules as InstallBuild, then the ordinary
// upgrade (stop, swap, start, probe; the previous binary back when the new
// one does not come up).
func (m *Manager) UpgradeBuild(ctx context.Context, id int64, b FeedBinary, from StoreLink, sigs []string) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	name := e.row.Name
	e.mu.Unlock()
	sig, err := m.pickSignature(ctx, name, b, from, sigs)
	if err != nil {
		return m.statusOf(ctx, e), err
	}
	resp, err := m.download(ctx, strings.TrimSpace(b.URL))
	if err != nil {
		return m.statusOf(ctx, e), err
	}
	defer resp.Body.Close()
	return m.upgrade(ctx, id, resp.Body, sig, strings.ToLower(strings.TrimSpace(b.SHA256)), buildSource{version: from.Version, via: from.Store})
}

// SetLicenseHold holds plugin name (reason non-empty: why, "license:
// <status>") or lets it run again (""). A held plugin is stopped, its driver
// unregistered and its state StateHeld; released, it starts if it is
// enabled. A plugin installed later under a held name starts held.
func (m *Manager) SetLicenseHold(name, reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.holds == nil {
		m.holds = map[string]string{}
	}
	prev := m.holds[name]
	if reason == "" {
		delete(m.holds, name)
	} else {
		m.holds[name] = reason
	}
	var e *entry
	for _, x := range m.entries {
		x.mu.Lock()
		match := x.row != nil && x.row.Name == name
		x.mu.Unlock()
		if match {
			e = x
			break
		}
	}
	m.mu.Unlock()
	if e == nil || prev == reason {
		return
	}
	if reason != "" {
		m.stop(e)
		e.mu.Lock()
		e.state, e.stateErr = StateHeld, reason
		e.mu.Unlock()
		e.logs.Add("warn", "held: "+reason)
		return
	}
	e.mu.Lock()
	on := e.row.Enabled
	e.state, e.stateErr = StateDisabled, ""
	e.mu.Unlock()
	e.logs.Add("info", "released: the license holds")
	if on {
		m.start(e)
		// An upgrade that landed while it was held proves itself now - or
		// is rolled back (settleUpgrade).
		m.settleLater(e)
	}
}

// heldReason is why name is held ("" = it is not).
func (m *Manager) heldReason(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.holds[name]
}

// dropUpgradeBackup removes the previous binary (and its signature) an
// upgrade kept beside target.
func dropUpgradeBackup(target string) {
	_ = os.Remove(target + ".previous")
	_ = os.Remove(signaturePath(target) + ".previous")
}

// settleLater watches e's start in the background when an upgrade that
// landed held is still unproven (settleUpgrade); otherwise it does nothing.
func (m *Manager) settleLater(e *entry) {
	e.mu.Lock()
	pending := e.pendingBackup && e.state != StateHeld
	e.mu.Unlock()
	if pending {
		go m.settleUpgrade(e)
	}
}

// settleUpgrade finishes an upgrade that landed while the plugin was held,
// once it has been started: when the new binary comes up, adopt drops the
// previous one; when it does not (its probes refuse it, it does not start),
// the previous binary is put back and started - the roll-back the upgrade
// would have made had it not been held.
func (m *Manager) settleUpgrade(e *entry) {
	ctx := m.ctx
	st := m.waitOutOfStarting(ctx, e, 45*time.Second)
	switch st.State {
	case StateRunning, StateHeld, StateDisabled, StateStarting:
		return
	}
	e.mu.Lock()
	pending := e.pendingBackup
	row := e.row
	e.mu.Unlock()
	if !pending || row == nil || row.Kind == model.PluginKindRemote || rowError(row) != nil {
		return
	}
	target := filepath.Join(m.dir, row.Name, row.Binary)
	backup := target + ".previous"
	prevSum, err := fileSHA256(backup)
	if err != nil {
		m.log.Warn("plugin: the upgraded binary did not come up and the previous one cannot be read",
			slog.String("plugin", row.Name), slog.Any("err", err))
		return
	}
	m.log.Warn("plugin upgrade failed once its license held; rolling back",
		slog.String("plugin", row.Name), slog.String("state", st.State), slog.String("err", st.StateError))
	m.stop(e)
	_ = os.Remove(target)
	_ = os.Remove(signaturePath(target))
	if err := os.Rename(backup, target); err != nil {
		m.log.Warn("plugin: could not put the previous binary back", slog.String("plugin", row.Name), slog.Any("err", err))
		return
	}
	_ = os.Rename(signaturePath(target)+".previous", signaturePath(target))
	e.mu.Lock()
	e.pendingBackup = false
	row.SHA256 = prevSum
	snapshot := *row
	e.mu.Unlock()
	if err := m.store.UpdatePlugin(ctx, &snapshot); err != nil {
		m.log.Warn("plugin: roll-back not recorded", slog.String("plugin", row.Name), slog.Any("err", err))
	}
	e.logs.Add("warn", "the upgraded binary did not come up ("+st.State+": "+st.StateError+") - the previous one was restored")
	m.start(e)
}

// fileSHA256 is the lower-hex sha256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
