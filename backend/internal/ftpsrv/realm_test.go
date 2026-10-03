package ftpsrv_test

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	goftp "github.com/jlaffaye/ftp"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
)

// FTPS on a multi-tenant install (#128): the tenant comes from the name the
// client connected to — TLS SNI, FTP's only "Host" — and without one from
// `realm/name`. Each alex here has their OWN password, so a login that lands
// on the wrong account is visible as a refusal.
func TestFTPSRealmFromSNI(t *testing.T) {
	hz := newHarness(t)
	hz.res.MultiTenant = true
	ctx := context.Background()
	acme, err := hz.store.CreateProvider(ctx, &model.Provider{Slug: "acme", Host: "files.acme.test", AuthType: "local", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := hz.store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(email, pass string, p *model.Provider) {
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
	}
	mk("alex@local", "MainPass!1", nil)
	mk("alex@acme.local", "AcmePass!1", acme)
	mk("alex@beta.local", "BetaPass!1", beta)

	login := func(sni, user, pass string) error {
		c, err := goftp.Dial(hz.addr,
			goftp.DialWithTimeout(10*time.Second),
			goftp.DialWithExplicitTLS(&tls.Config{InsecureSkipVerify: true, ServerName: sni}),
		)
		if err != nil {
			return err
		}
		defer func() { _ = c.Quit() }()
		return c.Login(user, pass)
	}

	// Connected to acme's own name: alex is acme's.
	if err := login("files.acme.test", "alex", "AcmePass!1"); err != nil {
		t.Errorf("alex at files.acme.test (SNI): %v", err)
	}
	if err := login("files.acme.test", "alex", "MainPass!1"); err == nil {
		t.Error("the platform's alex signed in at acme's address")
	}
	// ⚠ The realm typed and the address disagree: refused.
	if err := login("files.acme.test", "beta/alex", "BetaPass!1"); err == nil {
		t.Error("beta/alex was accepted at acme's address")
	}
	// No tenant name in the handshake (an IP, the platform's name): the realm
	// in the user name, else the platform's own.
	if err := login("127.0.0.1", "alex", "MainPass!1"); err != nil {
		t.Errorf("alex without SNI: %v", err)
	}
	if err := login("", "beta/alex", "BetaPass!1"); err != nil {
		t.Errorf("beta/alex without SNI: %v", err)
	}
	if err := login("", "alex", "BetaPass!1"); err == nil {
		t.Error("beta's alex signed in without naming beta")
	}
}
