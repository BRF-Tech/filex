package windows

import (
	"errors"
	"fmt"
)

// Logon is what a successful Windows sign-in tells filex: who the account is
// (its SID) and which groups its token carries. Nothing else leaves the OS call
// — in particular not the password, and not the token handle.
type Logon struct {
	// SID is the account's security identifier, `S-1-5-21-…-1001`.
	SID string
	// Groups are the names of the groups in the account's token, in both forms:
	// `CORP\Editors` and `Editors`, so an operator can write either.
	Groups []string
}

// logonFunc signs an account in with the operating system. user and domain are
// LogonUserW's two arguments (domain "." = this machine, "" = user is a UPN).
// It must return a *LogonError for a Win32 failure so the driver can tell
// "wrong password" from "the account is locked" from "could not ask".
type logonFunc func(user, domain, password string) (*Logon, error)

// defaultLogon is the operating system's sign-in call: set by logon_windows.go
// on Windows and left nil everywhere else, where Init says so instead of
// pretending.
var defaultLogon logonFunc

// SetLogonForTest replaces the operating-system sign-in call — for tests of
// this and other packages that need a Windows provider to run on any machine
// (the handlers' HTTP tests, the live-set tests). It returns the function that
// puts the previous one back; a test defers it. Passing nil makes the provider
// behave as it does off Windows.
//
// ⚠ It swaps a package variable: tests that use it must not run in parallel.
func SetLogonForTest(f func(user, domain, password string) (*Logon, error)) (restore func()) {
	prev := defaultLogon
	defaultLogon = f
	return func() { defaultLogon = prev }
}

// LogonError is a Win32 failure of the sign-in call.
type LogonError struct {
	// Code is the Win32 error (GetLastError).
	Code uint32
}

func (e *LogonError) Error() string {
	return fmt.Sprintf("windows: LogonUser failed: Win32 error %d", e.Code)
}

// errNotWindows is what Init answers on any other operating system.
var errNotWindows = errors.New("windows: this sign-in provider works only on Windows (it signs accounts in with the operating system); it cannot run on this machine")

// Win32 codes LogonUserW answers with.
const (
	errInvalidParameter   = 87
	errLogonFailure       = 1326 // unknown user name or bad password
	errAccountRestriction = 1327
	errInvalidLogonHours  = 1328
	errInvalidWorkstation = 1329
	errPasswordExpired    = 1330
	errAccountDisabled    = 1331
	errLogonTypeNotGrant  = 1385
	errAccountExpired     = 1793
	errPasswordMustChange = 1907
	errAccountLockedOut   = 1909
)

// Verdicts a Win32 code is judged as.
type verdict int

const (
	// verdictWrong: the OS judged the credentials and said no — a wrong
	// password or an account it does not know. The person is answered
	// ErrUnauthorized and nothing more is said.
	verdictWrong verdict = iota
	// verdictRefused: the OS knows the account and would not let it in — locked,
	// disabled, expired, password must change, forbidden here. The person still
	// gets the one answer (ErrUnauthorized: no oracle); the OPERATOR is told
	// why, in the log.
	verdictRefused
	// verdictUnknown: the OS could not judge (no domain controller, access
	// denied to the call itself, anything unexpected). Not ErrUnauthorized: the
	// login chain must not read it as "wrong password".
	verdictUnknown
)

// judge classifies a Win32 code and gives the operator's reason for a refusal.
func judge(code uint32) (verdict, string) {
	switch code {
	case errLogonFailure, errInvalidParameter:
		return verdictWrong, ""
	case errAccountLockedOut:
		return verdictRefused, "account_locked_out"
	case errAccountDisabled:
		return verdictRefused, "account_disabled"
	case errAccountExpired:
		return verdictRefused, "account_expired"
	case errPasswordExpired:
		return verdictRefused, "password_expired"
	case errPasswordMustChange:
		return verdictRefused, "password_must_change"
	case errAccountRestriction:
		return verdictRefused, "account_restriction"
	case errInvalidLogonHours:
		return verdictRefused, "logon_hours"
	case errInvalidWorkstation:
		return verdictRefused, "workstation_restriction"
	case errLogonTypeNotGrant:
		return verdictRefused, "network_logon_not_granted"
	}
	return verdictUnknown, ""
}
