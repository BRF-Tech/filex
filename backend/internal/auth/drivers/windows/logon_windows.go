//go:build windows

package windows

import (
	"errors"
	"syscall"
	"unsafe"

	xwin "golang.org/x/sys/windows"
)

// LogonUserW's arguments. A NETWORK logon judges the password and takes no
// interactive-session rights (nothing is loaded, no desktop, no profile): it
// is the cheap, side-effect-free way to ask "is this password right". It
// needs the "Access this computer from the network" right the account has by
// default; when a machine's policy withholds it, the call answers 1385 and the
// operator is told (judge).
const (
	logon32LogonNetwork    = 3
	logon32ProviderDefault = 0
)

var (
	modAdvapi32    = xwin.NewLazySystemDLL("advapi32.dll")
	procLogonUserW = modAdvapi32.NewProc("LogonUserW")
)

func init() { defaultLogon = winLogon }

// winLogon is the real sign-in: LogonUserW, then the SID and groups of the
// token it returned. The token is closed before this returns; the password is
// held in a UTF-16 buffer that is zeroed after the call.
//
// ⚠ Nothing here logs, and the error carries only the Win32 code — never an
// argument.
func winLogon(user, domain, password string) (*Logon, error) {
	u, err := xwin.UTF16PtrFromString(user)
	if err != nil {
		return nil, &LogonError{Code: errInvalidParameter}
	}
	var d *uint16
	if domain != "" {
		if d, err = xwin.UTF16PtrFromString(domain); err != nil {
			return nil, &LogonError{Code: errInvalidParameter}
		}
	}
	pw, err := xwin.UTF16FromString(password)
	if err != nil {
		// A NUL inside a password: no Windows password can hold one.
		return nil, &LogonError{Code: errLogonFailure}
	}
	defer func() {
		for i := range pw {
			pw[i] = 0
		}
	}()

	var tok xwin.Token
	r1, _, callErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(u)),
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(&pw[0])),
		logon32LogonNetwork,
		logon32ProviderDefault,
		uintptr(unsafe.Pointer(&tok)),
	)
	if r1 == 0 {
		var en syscall.Errno
		if errors.As(callErr, &en) && en != 0 {
			return nil, &LogonError{Code: uint32(en)}
		}
		return nil, errors.New("windows: LogonUserW failed without an error code")
	}
	defer tok.Close()

	tu, err := tok.GetTokenUser()
	if err != nil {
		return nil, err
	}
	out := &Logon{SID: tu.User.Sid.String()}

	if tg, err := tok.GetTokenGroups(); err == nil {
		seen := map[string]bool{}
		add := func(s string) {
			if s != "" && !seen[s] {
				seen[s] = true
				out.Groups = append(out.Groups, s)
			}
		}
		for _, g := range tg.AllGroups() {
			// A deny-only group grants nothing; the logon-session SID is not a
			// group anybody joined.
			if g.Attributes&xwin.SE_GROUP_USE_FOR_DENY_ONLY != 0 ||
				g.Attributes&xwin.SE_GROUP_LOGON_ID == xwin.SE_GROUP_LOGON_ID {
				continue
			}
			name, dom, _, lerr := g.Sid.LookupAccount("")
			if lerr != nil || name == "" {
				continue
			}
			if dom != "" {
				add(dom + `\` + name)
			}
			add(name)
		}
	}
	return out, nil
}
