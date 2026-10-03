package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func refusalOf(t *testing.T, err error) string {
	t.Helper()
	var hr *HandoffRefusal
	require.True(t, errors.As(err, &hr), "not a refusal: %v", err)
	require.ErrorIs(t, err, ErrHandoffRefused)
	return hr.Reason
}

// The primitive every handoff purpose shares: one use, one purpose, one host,
// a short life — and the code itself is never held.
func TestHandoffStore(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	s := NewHandoffStore()
	s.Now = func() time.Time { return now }
	tk := HandoffTicket{Purpose: HandoffLogin, ActorID: 7, SubjectID: 7, Host: "Files.Acme.Test", ProviderID: 3}

	code, err := s.Issue(tk, time.Minute)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(code), 43, "32 random bytes")
	_, byHash := s.m[handoffKey(code)]
	assert.True(t, byHash, "the store is keyed by the code's SHA-256, not the code")
	for _, v := range s.m {
		assert.NotContains(t, fmt.Sprintf("%+v", *v), code)
	}

	got, err := s.Redeem(code, HandoffLogin, "files.acme.test")
	require.NoError(t, err)
	assert.Equal(t, int64(7), got.SubjectID)
	assert.Equal(t, "files.acme.test", got.Host, "the host is folded at issue")
	_, err = s.Redeem(code, HandoffLogin, "files.acme.test")
	assert.Equal(t, HandoffRefuseUnknown, refusalOf(t, err), "spent by its first use")

	// Every refusal of a real ticket spends it, and hands the ticket back for
	// the audit row.
	for _, c := range []struct {
		purpose HandoffPurpose
		host    string
		advance time.Duration
		reason  string
	}{
		{"impersonate", "files.acme.test", 0, HandoffRefusePurpose},
		{HandoffLogin, "files.beta.test", 0, HandoffRefuseHost},
		{HandoffLogin, "", 0, HandoffRefuseHost},
		{HandoffLogin, "files.acme.test", 61 * time.Second, HandoffRefuseExpired},
	} {
		code, err := s.Issue(tk, time.Minute)
		require.NoError(t, err)
		now = now.Add(c.advance)
		got, err := s.Redeem(code, c.purpose, c.host)
		assert.Equal(t, c.reason, refusalOf(t, err))
		require.NotNil(t, got, "the refused ticket is returned for the audit")
		_, err = s.Redeem(code, HandoffLogin, "files.acme.test")
		assert.Equal(t, HandoffRefuseUnknown, refusalOf(t, err), "%s: spent all the same", c.reason)
	}

	_, err = s.Redeem("", HandoffLogin, "files.acme.test")
	assert.Equal(t, HandoffRefuseUnknown, refusalOf(t, err))
	_, err = s.Redeem(strings.Repeat("A", 43), HandoffLogin, "files.acme.test")
	assert.Equal(t, HandoffRefuseUnknown, refusalOf(t, err))
}
