package proxyheader

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// The proxy has said who the person is: the first-login rule's refusal carries
// its reason (auth.RefusalToTell) for the 401 the sign-in page reads, and is
// still ErrUnauthorized to every caller. A request from no trusted proxy, or
// naming nobody, carries none.
func TestRefusalReason_TheFirstLoginRuleIsTold(t *testing.T) {
	d, _ := initDriverWithStore(t, map[string]any{"auto_create": false})
	_, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", ""))
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonAutoCreateOff, auth.SSOReason(err))

	g, _ := initDriverWithStore(t, map[string]any{"allowed_groups": "staff"})
	_, err = g.Authenticate(withRoles("files.example.com", "can@example.com", "guests"))
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonGroupNotAllowed, auth.SSOReason(err))

	_, err = d.Authenticate(withRoles("files.example.com", "", ""))
	assert.Same(t, auth.ErrUnauthorized, err, "nobody named: nothing to tell")
}
