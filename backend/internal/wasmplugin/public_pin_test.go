package wasmplugin

// share_pin — the one door an app has to the PIN of a link it minted.
//
// The rules measured here are the rules "My shares" already applies, because
// this is the SAME door: the caller must be the person who created the link
// (or an administrator), the link must be this plugin's, every read writes
// the platform's own audit row, and a PIN that cannot be recovered comes back
// as a REASON rather than as a blank. Nothing here lets an app keep a PIN of
// its own — the whole point of asking the host instead.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

func sharePIN(t *testing.T, s *Scope, token string) (map[string]any, error) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"token": token})
	require.NoError(t, err)
	out, err := hfSharePIN(context.Background(), s, b)
	if err != nil {
		return nil, err
	}
	m, ok := out.(map[string]any)
	require.True(t, ok, "share_pin answered %T", out)
	return m, nil
}

// mintedLink is a PIN-protected link this plugin opened, as the given person.
func mintedLink(t *testing.T, h *harness, p *Installed, u *model.User) (token, pin string) {
	t.Helper()
	h.writeCatalogued(t, "docs/contract-signed.txt", "signed")
	s := h.jobScope(t, p, u, "docs/contract-signed.txt")
	out, err := shareCreate(t, s, map[string]any{"subject": "contract-signed.txt", "pin": "auto", "ttl_days": 5})
	require.NoError(t, err)
	token, _ = out["token"].(string)
	pin, _ = out["pin"].(string)
	require.NotEmpty(t, token)
	require.Len(t, pin, 6)
	return token, pin
}

// ⭐ The person who made the link reads its PIN, and the read is in the audit
// trail under the SAME action the "My shares" door writes — one spelling, so
// an operator grepping for PIN reads finds every one of them.
func TestSharePIN_TheMakerReadsItAndTheReadIsAudited(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	u := h.actor(t)
	token, pin := mintedLink(t, h, p, u)

	got, err := sharePIN(t, h.jobScope(t, p, u), token)
	require.NoError(t, err)
	assert.Equal(t, pin, got["pin"], "the PIN the link really carries")
	assert.NotContains(t, got, "reason")

	rows, err := h.wide(t).ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	var found *model.AuditEntry
	for _, e := range rows {
		if e.Action == share.AuditActionPinReveal {
			found = e
		}
	}
	require.NotNil(t, found, "no audit row for the read")
	assert.Equal(t, true, found.Metadata["revealed"])
	assert.Equal(t, false, found.Metadata["as_admin"])
	assert.Equal(t, "echo", found.Metadata["plugin"])
	// ⚠ Neither the secret nor the live link is in the row.
	for _, v := range found.Metadata {
		assert.NotEqual(t, token, v)
		assert.NotEqual(t, pin, v)
	}
}

// Somebody else's link is not theirs to read — and neither is a link with no
// person behind the call at all (a visitor's page, the hourly wake-up).
func TestSharePIN_OnlyTheMakerOrAnAdministrator(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	owner := h.actor(t)
	token, pin := mintedLink(t, h, p, owner)

	other, err := h.wide(t).CreateUser(context.Background(), "nosy@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	_, err = sharePIN(t, h.jobScope(t, p, other), token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only the person who made this link")

	_, err = sharePIN(t, h.jobScope(t, p, nil), token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read by a person")

	admin, err := h.wide(t).CreateUser(context.Background(), "boss@example.com", "x", "admin", "en", "UTC")
	require.NoError(t, err)
	got, err := sharePIN(t, h.jobScope(t, p, admin), token)
	require.NoError(t, err, "an administrator may, exactly as on the Shares screen")
	assert.Equal(t, pin, got["pin"])
	rows, err := h.wide(t).ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	var asAdmin bool
	for _, e := range rows {
		if e.Action == share.AuditActionPinReveal && e.Metadata["as_admin"] == true {
			asAdmin = true
		}
	}
	assert.True(t, asAdmin, "the trail says WHICH principal was used")
}

// A PIN that cannot be recovered answers a REASON, never a blank — and the
// refusal is still audited, because "somebody asked" is the event.
func TestSharePIN_ARefusalIsAReason(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	u := h.actor(t)
	h.writeCatalogued(t, "docs/open.txt", "no pin here")
	s := h.jobScope(t, p, u, "docs/open.txt")
	out, err := shareCreate(t, s, map[string]any{"subject": "open.txt", "ttl_days": 5})
	require.NoError(t, err)
	token, _ := out["token"].(string)

	got, err := sharePIN(t, h.jobScope(t, p, u), token)
	require.NoError(t, err)
	assert.Empty(t, got["pin"])
	assert.Equal(t, "no_pin", got["reason"], "which of the three it is, said out loud")

	rows, err := h.wide(t).ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	var found *model.AuditEntry
	for _, e := range rows {
		if e.Action == share.AuditActionPinReveal {
			found = e
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, false, found.Metadata["revealed"])
	assert.Equal(t, "no_pin", found.Metadata["reason"])
}

// A token that is not this plugin's is "no such link" — the same answer an
// unknown token gets, so one cannot be told from the other.
func TestSharePIN_AnotherAppsLinkIsNotFound(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	u := h.actor(t)
	token, _ := mintedLink(t, h, p, u)
	// The link stays where it is; the ASKER becomes another app.
	s := h.jobScope(t, p, u)
	was := p.Row.ID
	p.Row.ID = was + 1000
	_, err := sharePIN(t, s, token)
	p.Row.ID = was
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such link")
	_, err = sharePIN(t, h.jobScope(t, p, u), "0123456789abcdef0123456789abcdef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such link")
}
