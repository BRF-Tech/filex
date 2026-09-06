// Package antivirus — settings.go
//
// The three settings that decide WHETHER filex scans and HOW it reaches
// ClamAV, and the one function that answers both questions.
//
// # What moved, and what that costs
//
// Until now `FILEX_CLAMAV=0` was the kill-switch and it was env-only: turning
// scanning off meant editing compose and restarting, and the admin page could
// only report what the environment had decided. It is now a settings-table
// row like `antivirus.max_scan_mb` and `antivirus.save_scan_window_minutes`,
// which an admin flips on the Protection page.
//
// ⚠⚠ IT TAKES EFFECT AT THE NEXT RESTART, IN BOTH DIRECTIONS, AND THE UI SAYS
// SO AT THE MOMENT OF THE FLIP. This is a deliberate choice, not an
// implementation shortcut. The scan pipeline is wired once at boot in
// internal/server: the queue handler is registered and the upload surfaces are
// handed an enqueue function only when scanning resolved as available. Making
// "off" live while "on" stayed deferred — the shape that falls out of a naive
// implementation, since suppressing work is easy and creating a worker pool
// handler is not — would give an operator a control that is sometimes
// immediate and sometimes not, with no way to tell which from looking at it.
// A switch that is honestly always deferred can be reasoned about; one that is
// live half the time cannot.
//
// The two sibling settings (max_scan_mb, save_scan_window_minutes) ARE live,
// and that is not an inconsistency: they tune a pipeline that is already
// running, and they are read at the point of use on every file. This one
// decides whether the pipeline exists.
package antivirus

import (
	"context"
	"os"
	"strings"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// The transports filex can use to reach ClamAV. An operator picks one
// explicitly rather than filex inferring it, so that "daemon mode with a
// broken address" reads as broken instead of quietly falling back to a binary
// that may not be the scanner they meant.
const (
	ModeBinary = "binary"
	ModeDaemon = "daemon"
)

// Modes is the closed set, shipped to the admin UI with the value so the form
// cannot offer an option the API refuses.
var Modes = []string{ModeBinary, ModeDaemon}

// EnabledSetting is the scanning kill-switch.
//
// ⚠⚠ FILEX_CLAMAV is a SEED, not an override: it is consumed on a boot where
// no row exists yet, which for an upgrading install is the first boot after
// this version. From then on the row wins and editing the variable in compose
// does nothing. An install that had FILEX_CLAMAV=0 therefore keeps scanning
// off across the upgrade, and gets a switch on the Protection page instead of
// a compose edit.
//
// Default true: unset has always meant "scan if you can", and the absence of a
// variable must not become a silent opt-out.
var EnabledSetting = dbsetting.BoolSpec{
	Key:     "antivirus.enabled",
	EnvVar:  "FILEX_CLAMAV",
	Default: true,
}

// ModeSetting picks the transport.
//
// Default binary, because that is what every install before this version was
// doing and a default may not change behaviour under an upgrade.
var ModeSetting = dbsetting.StringSpec{
	Key:     "antivirus.mode",
	EnvVar:  "FILEX_CLAMAV_MODE",
	Default: ModeBinary,
	// ⚠ Case-folded. "Daemon" and "DAEMON" are the same choice to the person
	// typing them, and a closed set that refuses them teaches an operator
	// nothing except that the field is fussy.
	Normalize: func(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) },
	Check:     dbsetting.OneOf(ModeBinary, ModeDaemon),
}

// AddrSetting is the clamd daemon address used in daemon mode: `clamav:3310`,
// `tcp://127.0.0.1:3310` or a unix socket path. Empty is legal and means "not
// configured yet" — it is only an error once daemon mode is selected, and
// Resolve is where that becomes an error.
//
// ⚠ Check runs ParseAddr, so an unparseable address is refused by the admin
// API at SAVE time. Without that the mistake surfaces at scan time, i.e. in
// the log of the file that did not get scanned.
var AddrSetting = dbsetting.StringSpec{
	Key:     "antivirus.clamd_addr",
	EnvVar:  "FILEX_CLAMAV_ADDR",
	Default: "",
	Check: func(v string) error {
		if v == "" {
			return nil
		}
		_, _, err := ParseAddr(v)
		return err
	},
}

// Settings is the family, in the order they are seeded at boot.
func Settings() []dbsetting.Seeder {
	return []dbsetting.Seeder{
		EnabledSetting, ModeSetting, AddrSetting,
		SaveWindowSetting, MaxScanSetting,
	}
}

// SeedSettings consumes the antivirus environment variables into the settings
// table, first boot only. Call once at boot, before anything resolves them.
//
// ⚠ Beyond the per-spec seeding it applies ONE cross-variable rule: an install
// that set FILEX_CLAMAV_ADDR but no FILEX_CLAMAV_MODE is seeded into daemon
// mode. Without it, a compose file that names a clamd container comes up in
// binary mode, finds no binary, and reports scanning unavailable — a
// deployment that looks configured and scans nothing. The rule obeys the same
// first-boot-only contract as every other seed (StringSpec.SeedValue), so it
// can never overwrite a mode an admin has since chosen.
func SeedSettings(ctx context.Context, st dbsetting.Store) {
	dbsetting.SeedAll(ctx, st, Settings()...)
	if os.Getenv("FILEX_CLAMAV_MODE") == "" && os.Getenv("FILEX_CLAMAV_ADDR") != "" {
		_ = ModeSetting.SeedValue(ctx, st, ModeDaemon)
	}
}

// Config is the three settings AS STORED, independent of whether scanning is
// available or reachable: what an admin last saved, which is what the form has
// to render.
//
// ⚠⚠ It is separate from Resolution because the two legitimately differ, and
// reading the rows directly instead would reintroduce exactly the drift this
// package exists to prevent. Resolution stops at the first thing that decides
// the outcome — switched off, and the transport is moot; so Resolution.Mode is
// empty for a disabled scanner even though the admin picked one. A form that
// rendered its toggle from Resolution would show "binary, off" as "binary" and
// a form that read the raw row would miss the unseeded-environment fallback and
// show the toggle ON while scanning was off. Both come from here instead.
type Config struct {
	Enabled bool
	Mode    string
	Addr    string
}

// Configured reads the settings in force, applying the same unseeded fallback
// Resolve does, so the form and the pipeline never disagree about what was
// saved.
func Configured(ctx context.Context, g dbsetting.Getter) Config {
	addr, _ := resolveStringStrict(ctx, g, AddrSetting)
	return Config{
		Enabled: resolveEnabled(ctx, g),
		Mode:    resolveMode(ctx, g),
		Addr:    addr,
	}
}

// Resolution is the complete answer to "is scanning on, and how is ClamAV
// reached" — the single place both the advertised capability flag and the real
// scan pipeline get their answer from, so the green light and the pipeline can
// never disagree.
type Resolution struct {
	// Enabled is the antivirus.enabled switch alone. It can be true while
	// Available() is false: switched on, but with nothing to reach.
	Enabled bool
	// Mode is ModeBinary or ModeDaemon. It is reported even when scanning is
	// unavailable, because "daemon mode, bad address" and "binary mode, no
	// binary" need different fixes and an operator seeing one red light needs
	// to know which.
	Mode string
	// Bin is the resolved executable path in binary mode ("" if none).
	Bin string
	// Addr is the daemon address exactly as configured, for display.
	Addr string
	// Network and Address are the dial target parsed out of Addr.
	Network, Address string
	// Err says why an enabled scanner is nonetheless unusable. nil in every
	// healthy state, and nil when Enabled is false (that is a choice, not a
	// fault).
	Err error
}

// Available reports whether a scan can be attempted at all. It is what the
// capability flag and the boot-time wiring both key off.
//
// ⚠ Available does not mean REACHABLE. In daemon mode it means an address is
// configured and parses; whether clamd answers is a question with a network
// round-trip in it, asked by Health where an operator is waiting for the
// answer, and answered by Scan returning an error — never by Scan returning
// "clean".
func (r Resolution) Available() bool {
	if !r.Enabled || r.Err != nil {
		return false
	}
	if r.Mode == ModeDaemon {
		return r.Address != ""
	}
	return r.Bin != ""
}

// Resolve reads the settings in force and works out how ClamAV is reached.
//
// ⚠ g may be nil — the CLI and unit tests have no settings table. With no
// table there are no rows, so the environment variables that would have SEEDED
// them are the only source of intent and are read directly. That is the same
// order the package documents (row → env → default) with the first term
// missing, not a second resolution path.
func Resolve(ctx context.Context, g dbsetting.Getter) Resolution {
	var res Resolution
	res.Enabled = resolveEnabled(ctx, g)
	if !res.Enabled {
		return res
	}
	res.Mode = resolveMode(ctx, g)
	if res.Mode == ModeDaemon {
		// ⚠ ResolveStrict, not Resolve: a stored address that will not parse
		// must report THAT, not "no address is configured". The second reads
		// as "you have not set this up yet" on a page whose next field shows
		// the address the operator did set.
		var aerr error
		res.Addr, aerr = resolveStringStrict(ctx, g, AddrSetting)
		if aerr != nil {
			res.Err = aerr
			return res
		}
		if res.Addr == "" {
			res.Err = ErrNoDaemonAddress
			return res
		}
		network, address, err := ParseAddr(res.Addr)
		if err != nil {
			res.Err = err
			return res
		}
		res.Network, res.Address = network, address
		return res
	}
	res.Bin = ResolveBin()
	return res
}

// ---------------------------------------------------------------------------
// Reading a setting that has not been seeded yet.
//
// ⚠⚠ These three helpers exist for ONE case: a resolution that happens before
// SeedSettings has run, or with no settings table at all (a test harness, a
// CLI subcommand). With no row there is nothing to have overridden, so the
// environment variable that WOULD have seeded the row is the only statement of
// intent on record, and ignoring it would make FILEX_CLAMAV=0 silently stop
// working in exactly the situations where nobody is watching.
//
// It is not a second resolution path and it is not an override. The instant a
// row exists — written by seeding, or by an admin on the Protection page — the
// row wins and the variable is inert, which is the contract package dbsetting
// documents. In the running server SeedSettings is called before Resolve, so
// this branch is only ever taken on the very first boot, before the write.
// ---------------------------------------------------------------------------

// hasRow reports whether the settings table holds a non-blank value for key.
func hasRow(ctx context.Context, g dbsetting.Getter, key string) bool {
	if g == nil {
		return false
	}
	raw, err := g.GetSetting(ctx, key)
	return err == nil && strings.TrimSpace(raw) != ""
}

func resolveEnabled(ctx context.Context, g dbsetting.Getter) bool {
	if hasRow(ctx, g, EnabledSetting.Key) {
		return EnabledSetting.Resolve(ctx, g)
	}
	if v, ok := dbsetting.ParseBool(os.Getenv(EnabledSetting.EnvVar)); ok {
		return v
	}
	return EnabledSetting.Default
}

// resolveMode adds the address-implies-daemon rule, so an unseeded caller
// reaches the same conclusion a seeded one would.
func resolveMode(ctx context.Context, g dbsetting.Getter) string {
	if hasRow(ctx, g, ModeSetting.Key) {
		return ModeSetting.Resolve(ctx, g)
	}
	if raw := os.Getenv(ModeSetting.EnvVar); raw != "" {
		if v, err := ModeSetting.Canonical(raw); err == nil {
			return v
		}
	}
	if os.Getenv(AddrSetting.EnvVar) != "" || hasRow(ctx, g, AddrSetting.Key) {
		return ModeDaemon
	}
	return ModeSetting.Default
}

// resolveStringStrict reads a StringSpec and keeps the reason an unusable
// value is not in force.
func resolveStringStrict(ctx context.Context, g dbsetting.Getter, spec dbsetting.StringSpec) (string, error) {
	if hasRow(ctx, g, spec.Key) {
		return spec.ResolveStrict(ctx, g)
	}
	if raw := os.Getenv(spec.EnvVar); raw != "" {
		v, err := spec.Canonical(raw)
		if err != nil {
			return raw, err
		}
		return v, nil
	}
	return spec.Default, nil
}
