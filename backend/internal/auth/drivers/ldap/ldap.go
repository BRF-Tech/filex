// Package ldap implements simple-bind LDAP authentication.
//
// On successful bind, the user is upserted into the local users table —
// downstream requests still travel via the same session cookie that the
// local driver maintains.
package ldap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func init() {
	auth.Register("ldap", func() auth.Driver { return &Driver{} })
}

// Driver is the LDAP/AD auth driver.
type Driver struct {
	store      db.Store
	url        string // ldap:// or ldaps://
	bindDN     string // service account
	bindPass   string
	baseDN     string
	userFilter string // e.g. "(mail=%s)"
	emailAttr  string // e.g. "mail"
	startTLS   bool
}

// New constructs an empty driver — Init must be called.
func New(store db.Store) *Driver {
	return &Driver{store: store, emailAttr: "mail", userFilter: "(mail=%s)"}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return "ldap" }

// Init configures the driver.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("ldap: nil store")
	}
	d.url, _ = cfg["url"].(string)
	d.bindDN, _ = cfg["bind_dn"].(string)
	d.bindPass, _ = cfg["bind_password"].(string)
	d.baseDN, _ = cfg["base_dn"].(string)
	if v, ok := cfg["user_filter"].(string); ok && v != "" {
		d.userFilter = v
	}
	if v, ok := cfg["email_attr"].(string); ok && v != "" {
		d.emailAttr = v
	}
	d.startTLS, _ = cfg["start_tls"].(bool)
	if d.url == "" || d.baseDN == "" {
		return errors.New("ldap: url and base_dn required")
	}
	return nil
}

// Capabilities implements auth.Driver.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{
		SignIn:         true,
		Logout:         true,
		ChangePassword: false,
		Register:       false,
	}
}

// Authenticate is a no-op — LDAP requires explicit Login. Downstream
// session handling is performed by the local driver once the handler
// has minted a session token.
func (d *Driver) Authenticate(_ *http.Request) (*model.User, error) {
	return nil, auth.ErrUnauthorized
}

// Login attempts a simple bind against the configured directory.
func (d *Driver) Login(ctx context.Context, email, password string) (*model.User, string, error) {
	if password == "" {
		return nil, "", auth.ErrUnauthorized
	}
	conn, err := ldap.DialURL(d.url)
	if err != nil {
		return nil, "", fmt.Errorf("ldap: dial: %w", err)
	}
	defer conn.Close()

	if d.startTLS {
		// Caller-supplied TLS cert pinning is V2.
		if err := conn.StartTLS(nil); err != nil {
			return nil, "", fmt.Errorf("ldap: starttls: %w", err)
		}
	}
	if d.bindDN != "" {
		if err := conn.Bind(d.bindDN, d.bindPass); err != nil {
			return nil, "", fmt.Errorf("ldap: service bind: %w", err)
		}
	}

	// Find user DN.
	filter := fmt.Sprintf(d.userFilter, ldap.EscapeFilter(strings.ToLower(email)))
	res, err := conn.Search(ldap.NewSearchRequest(
		d.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false,
		filter, []string{"dn", d.emailAttr}, nil,
	))
	if err != nil || len(res.Entries) == 0 {
		return nil, "", auth.ErrUnauthorized
	}
	userDN := res.Entries[0].DN
	if err := conn.Bind(userDN, password); err != nil {
		return nil, "", auth.ErrUnauthorized
	}

	em := res.Entries[0].GetAttributeValue(d.emailAttr)
	if em == "" {
		em = email
	}
	user, err := d.store.GetUserByEmail(ctx, strings.ToLower(em))
	if err != nil {
		user, err = d.store.CreateUser(ctx, strings.ToLower(em), "", model.RoleUser, "en", "UTC")
		if err != nil {
			return nil, "", err
		}
	}
	_ = d.store.TouchLastLogin(ctx, user.ID)
	// Caller mints session token via the local driver.
	return user, "", nil
}
