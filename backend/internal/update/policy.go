package update

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Policy is how far an install is willing to move on its own.
type Policy string

const (
	// PolicyOff disables the update check entirely — no outbound request.
	PolicyOff Policy = "off"
	// PolicyManual checks and announces, but never applies anything by itself.
	PolicyManual Policy = "manual"
	// PolicyPatch (the default) applies z-moves automatically; y and x are
	// announced. This is what AUTO_UPGRADE=true selects.
	PolicyPatch Policy = "patch"
	// PolicyMinor also applies y-moves automatically. Refused while the
	// install is on a 0.x version — see Decide.
	PolicyMinor Policy = "minor"
)

// ParsePolicy maps config text to a Policy, falling back to PolicyManual for
// anything unrecognized: an unreadable setting must not silently grant MORE
// automation than the operator asked for.
func ParsePolicy(s string) Policy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none", "disabled":
		return PolicyOff
	case "patch", "auto":
		return PolicyPatch
	case "minor":
		return PolicyMinor
	default:
		return PolicyManual
	}
}

// InstallMode is how this filex was installed, which decides whether it can
// replace itself at all.
type InstallMode string

const (
	// ModeBinary — a binary on disk (systemd, bare process). filex can swap it.
	ModeBinary InstallMode = "binary"
	// ModeDocker — running inside a container. filex CANNOT upgrade itself: the
	// image layer is immutable, so a replaced binary vanishes at the next
	// `docker compose up` and the version silently reverts. Container installs
	// get instructions (or an external updater), never a self-apply.
	ModeDocker InstallMode = "docker"
)

// DetectInstallMode inspects the runtime. FILEX_INSTALL_MODE overrides it for
// setups the heuristics cannot see (e.g. a binary inside a container image
// that is intentionally managed as a binary).
func DetectInstallMode() InstallMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FILEX_INSTALL_MODE"))) {
	case "binary":
		return ModeBinary
	case "docker", "container":
		return ModeDocker
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ModeDocker
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods") {
			return ModeDocker
		}
	}
	return ModeBinary
}

// CanSelfApply reports whether this install may replace its own binary.
func (m InstallMode) CanSelfApply() bool { return m == ModeBinary }

// Action is what the install should do about an available release.
type Action string

const (
	// ActionNone — already current.
	ActionNone Action = "none"
	// ActionAuto — apply without asking (patch under an allowing policy).
	ActionAuto Action = "auto"
	// ActionConfirm — offer a one-click upgrade; the operator decides.
	ActionConfirm Action = "confirm"
	// ActionInstruct — show upgrade instructions; filex cannot do it itself.
	ActionInstruct Action = "instruct"
)

// Decision is the full answer, including WHY — the reason is shown in the UI
// and logged, because "there is an update but I am not taking it" is exactly
// the state an operator needs explained.
type Decision struct {
	Action  Action
	Step    Step
	Target  Release
	Reason  string
	Skipped []Release // releases between current and target (informational)
}

// Input is everything Decide needs. It takes no globals so the whole policy is
// testable as a pure function.
type Input struct {
	Current  Version
	Manifest *Manifest
	Policy   Policy
	Mode     InstallMode
	// Now and Window gate automatic application to a maintenance window. A zero
	// Window means "any time".
	Now    time.Time
	Window Window
}

// Decide resolves what to do. The rules, in order:
//
//  1. Nothing newer → none.
//  2. Below a release's MinVersion → instruct (must step through).
//  3. Major move → instruct, always. A major is a decision, not an event.
//  4. Cannot self-apply (docker) → instruct for anything it would otherwise
//     have applied, confirm stays confirm.
//  5. Patch: auto when the policy allows AND the release is auto_ok AND no
//     migration is involved; otherwise confirm.
//  6. Minor: auto only under PolicyMinor and only when Current.Major > 0 —
//     while a project is on 0.x, semver gives minor no compatibility promise.
//     Otherwise confirm.
//  7. Outside the maintenance window, an auto becomes a deferred auto (still
//     ActionAuto, but Ready reports false — see Window.Allows).
func Decide(in Input) Decision {
	latest, ok := in.Manifest.Latest()
	if !ok {
		return Decision{Action: ActionNone, Step: StepNone, Reason: "no releases in manifest"}
	}
	target, err := ParseVersion(latest.Version)
	if err != nil {
		return Decision{Action: ActionNone, Step: StepNone, Reason: "unparsable release version"}
	}
	step := in.Current.StepTo(target)
	if step == StepNone {
		return Decision{Action: ActionNone, Step: StepNone, Target: latest, Reason: "up to date"}
	}

	skipped := in.Manifest.Between(in.Current, target)
	d := Decision{Step: step, Target: latest, Skipped: skipped}

	if latest.MinVersion != "" {
		if min, err := ParseVersion(latest.MinVersion); err == nil && in.Current.Compare(min) < 0 {
			d.Action, d.Reason = ActionInstruct, "install is older than this release's minimum ("+min.String()+"); upgrade in steps"
			return d
		}
	}

	if step == StepMajor {
		d.Action, d.Reason = ActionInstruct, "major release — read the upgrade notes first"
		return d
	}

	// Would this be automatic if the install could apply it?
	auto, reason := autoAllowed(in, step, latest, skipped)
	switch {
	case auto && !in.Mode.CanSelfApply():
		d.Action = ActionInstruct
		d.Reason = "container install cannot replace its own image — use an external updater or upgrade manually"
	case auto:
		d.Action, d.Reason = ActionAuto, reason
	case !in.Mode.CanSelfApply():
		d.Action, d.Reason = ActionInstruct, reason
	default:
		d.Action, d.Reason = ActionConfirm, reason
	}
	return d
}

// autoAllowed answers "may this move happen without asking?" plus the reason,
// which is phrased for the operator either way.
func autoAllowed(in Input, step Step, target Release, skipped []Release) (bool, string) {
	if in.Policy == PolicyOff || in.Policy == PolicyManual {
		return false, "policy is " + string(in.Policy) + " — updates are announced, not applied"
	}
	if !target.AutoOK {
		return false, "release is not marked auto_ok — apply it deliberately"
	}
	// A patch must not carry schema changes. If one does, it is a packaging
	// mistake, and the safe reading is "this is not really a patch".
	for _, r := range append(append([]Release{}, skipped...), target) {
		if r.Migrations {
			return false, "release changes the database schema — confirm so a backup is taken first"
		}
	}
	switch step {
	case StepPatch:
		return true, "patch release under policy " + string(in.Policy)
	case StepMinor:
		if in.Policy != PolicyMinor {
			return false, "minor release — policy " + string(in.Policy) + " applies patches only"
		}
		if in.Current.Major == 0 {
			return false, "0.x minor releases may break compatibility — confirm required"
		}
		return true, "minor release under policy minor"
	}
	return false, "unhandled step"
}

// Window is a daily maintenance window, e.g. 03:00–05:00 local time. The zero
// value allows any time.
type Window struct {
	FromMin, ToMin int // minutes since midnight; equal values = always
	Set            bool
}

// ParseWindow reads "03:00-05:00". An unparsable value yields an unset window
// (always allowed) rather than an error: a typo in a schedule must not stop
// security patches from landing.
func ParseWindow(s string) Window {
	s = strings.TrimSpace(s)
	if s == "" {
		return Window{}
	}
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return Window{}
	}
	from, ok1 := parseHHMM(parts[0])
	to, ok2 := parseHHMM(parts[1])
	if !ok1 || !ok2 {
		return Window{}
	}
	return Window{FromMin: from, ToMin: to, Set: true}
}

// Allows reports whether an automatic apply may run at t. Windows that cross
// midnight (22:00-04:00) are supported.
func (w Window) Allows(t time.Time) bool {
	if !w.Set || w.FromMin == w.ToMin {
		return true
	}
	cur := t.Hour()*60 + t.Minute()
	if w.FromMin < w.ToMin {
		return cur >= w.FromMin && cur < w.ToMin
	}
	return cur >= w.FromMin || cur < w.ToMin
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
