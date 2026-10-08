package share

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// ── THE PIN RULE FOR EVERY PUBLIC LINK ─────────────────────────────────
//
// How long a link's PIN may be is decided HERE, once, for every door that
// mints a link: the share dialog (POST /api/files/share), the agent API and
// the MCP file_share tool, the permission rule that forces a PIN, and an app
// plugin's share_create. Service.Create refuses anything else, so no door can
// forget it.
//
// ⚠ It used to live in one door only: an app plugin's page was held to 4-12
// characters (wasmplugin/public.go) while POST /api/files/share took a PIN of
// any length — and the visitor's PIN box (PublicPinGate) stopped typing at
// 12. A link made through the API with a 16-character PIN could therefore
// never be opened from the page its recipient was sent to. The upper bound
// travels to the page as `pin_max` (GET /api/public/s|d/{token}), so the box
// and the rule are one number.

const (
	// PINMinLen is the shortest PIN a link may carry, in characters.
	PINMinLen = 4
	// PINMaxLen is the longest PIN a link may carry, in characters. The
	// visitor's PIN box is told it as `pin_max`.
	PINMaxLen = 12
)

// ErrPINLength refuses a PIN outside PINMinLen..PINMaxLen characters.
var ErrPINLength = fmt.Errorf("share: pin must be %d-%d characters", PINMinLen, PINMaxLen)

// CheckPINLength reports whether pin may be a link's PIN: ErrPINLength when
// it is shorter than PINMinLen or longer than PINMaxLen characters (not
// bytes - a PIN typed in another script is as long as it looks), nil
// otherwise. An empty PIN is "no PIN" and is not this rule's business.
func CheckPINLength(pin string) error {
	if pin == "" {
		return nil
	}
	if n := utf8.RuneCountInString(pin); n < PINMinLen || n > PINMaxLen {
		return ErrPINLength
	}
	return nil
}

// IsPINLength reports whether err is the PIN-length refusal.
func IsPINLength(err error) bool { return errors.Is(err, ErrPINLength) }
