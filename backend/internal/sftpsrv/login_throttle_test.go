package sftpsrv_test

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
)

// SFTP shares the sign-in limit with every other password door: the peer's
// socket address is the client (SSH has no forwarded headers), and five wrong
// passwords lock the account for the right one too.
func TestWrongPasswordsLockTheAccountOverSFTP(t *testing.T) {
	hz := newHarness(t)
	hz.res.Guard = loginguard.New(hz.store)
	hz.user(t, "sftp@example.com")
	hz.storage(t, "main")

	for i := 0; i < 5; i++ {
		if _, err := hz.dial(t, "sftp@example.com", "wrong"); err == nil {
			t.Fatalf("attempt %d: a wrong password was accepted", i+1)
		}
	}
	if _, err := hz.dial(t, "sftp@example.com", testPassword); err == nil {
		t.Fatal("the right password got in while the account is locked")
	}
	acct, err := hz.store.GetLoginThrottle(t.Context(), model.LoginThrottleAccount, "sftp@example.com")
	if err != nil || acct == nil || acct.LockedUntil == nil {
		t.Fatalf("the account was not locked: %+v %v", acct, err)
	}
	if acct.LastProtocol != loginguard.ProtoSFTP || acct.LastIP != "127.0.0.1" {
		t.Fatalf("the attempt was filed under door %q address %q, want sftp / 127.0.0.1", acct.LastProtocol, acct.LastIP)
	}
}
