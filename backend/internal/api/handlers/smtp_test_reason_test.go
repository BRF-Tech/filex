package handlers_test

// The admin "Send test" names WHY it failed (`reason`, mailer.Reason), so the
// screen can say it in words; the Go error stays as `error` for its second
// line (QA, 2026-09-21: the Go error was the whole answer).

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestSMTPTest_SaysTheReason(t *testing.T) {
	srv, client, store := testutil.NewTestServerWith(t, func(c *config.Config) {}, func(d *api.Deps) {
		d.Mailer = mailer.New(d.Store)
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	code, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/settings/smtp-test", map[string]any{})
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.Equal(t, false, body["ok"])
	assert.Equal(t, "not_configured", body["reason"], "an install with no SMTP host says so by name")
	assert.NotEmpty(t, body["error"], "the raw words stay for the administrator's second line")
}
