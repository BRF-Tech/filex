package local

import (
	"errors"
	"unicode/utf8"
)

// MinPasswordLen is the shortest password an account may be given, counted in
// characters (a "ş" is one, not the two bytes UTF-8 spells it with).
//
// ⚠ One number, here, for every door that sets an account's password: the
// person's own change (POST /api/auth/password), an administrator adding an
// account and an administrator setting one (POST and PATCH /api/admin/users).
// Until 0.54 only the first held it. The refusal names the number in the
// reader's language (server.account.password_short), so no interface keeps a
// copy of it.
const MinPasswordLen = 8

// ErrPasswordTooShort is CheckPassword's refusal.
var ErrPasswordTooShort = errors.New("password too short")

// CheckPassword is the rule a password chosen for an account passes. A
// password filex generates itself (an invitation, a reset, the first
// administrator's) is longer by construction and is not asked.
func CheckPassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}
