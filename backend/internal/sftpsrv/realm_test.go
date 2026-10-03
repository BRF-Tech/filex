package sftpsrv_test

import (
	"context"
	"testing"

	"golang.org/x/crypto/ssh"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/sftpsrv"
)

// SFTP on a multi-tenant install (#128). SSH never tells the server which
// address the client dialled — there is no SNI or Host to read — so a password
// login names its tenant as `realm/name`, and a bare name is the platform's
// own. A registered key needs no realm (it belongs to one account); a realm
// written in front of it must be that account's.
func TestSFTPRealm(t *testing.T) {
	hz := newHarness(t)
	hz.res.MultiTenant = true
	hz.storage(t, "main")
	ctx := context.Background()
	acme, err := hz.store.CreateProvider(ctx, &model.Provider{Slug: "acme", AuthType: "local", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := hz.store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(email, pass string, p *model.Provider) *model.User {
		hash, err := authlocal.HashPassword(pass)
		if err != nil {
			t.Fatal(err)
		}
		u, err := hz.store.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
		if err != nil {
			t.Fatal(err)
		}
		if p != nil {
			if err := hz.store.SetUserProvider(ctx, u.ID, p.ID, ""); err != nil {
				t.Fatal(err)
			}
		}
		return u
	}
	mk("alex@local", "MainPass!1", nil)
	alexA := mk("alex@acme.local", "AcmePass!1", acme)
	mk("alex@beta.local", "BetaPass!1", beta)

	ok := func(login, pass string) bool {
		c, err := hz.dial(t, login, pass)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	if !ok("acme/alex", "AcmePass!1") {
		t.Error("acme/alex was refused")
	}
	if !ok("alex", "MainPass!1") {
		t.Error("the platform's alex was refused")
	}
	if ok("alex", "AcmePass!1") {
		t.Error("acme's alex signed in without naming acme")
	}
	if ok("beta/alex", "AcmePass!1") {
		t.Error("acme's password opened beta's realm")
	}
	if ok("nobody/alex", "AcmePass!1") {
		t.Error("a realm nobody has was accepted")
	}

	// A key registered by acme's alex.
	signer, pub := testKey(t)
	fp, wire, _, err := sftpsrv.ParseAuthorizedKey(string(ssh.MarshalAuthorizedKey(pub)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hz.store.CreateSSHPublicKey(ctx, &model.SSHPublicKey{UserID: alexA.ID, Name: "laptop", Fingerprint: fp, PublicKey: wire}); err != nil {
		t.Fatal(err)
	}
	withKey := func(login string) bool {
		c, err := hz.dialAuth(t, login, ssh.PublicKeys(signer))
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	if !withKey("alex") {
		t.Error("the key's own account was refused without a realm")
	}
	if !withKey("acme/alex") {
		t.Error("the key's own account was refused with its realm")
	}
	if withKey("beta/alex") {
		t.Error("acme's key was accepted as beta/alex")
	}
}
