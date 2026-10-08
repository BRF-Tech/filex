package auth

// A provider test is worded by the SERVER (0.54 audit, A13): every step's
// sentence comes back as `text`, so the panel, a tenant's page and the admin
// MCP tool admin_auth_providers_test read the same words. Until 0.54 the
// sentences were built in the browser (web lib/providerChecks.ts) and an API
// reader got step ids and parameter codes.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

func said(lang string, c ProbeCheck) string {
	cs := []ProbeCheck{c}
	SayChecks(lang, cs)
	return cs[0].Text
}

func TestSayChecks_AFailedStepNamesWhatAndWhyInTheReadersLanguage(t *testing.T) {
	c := Check("connect", ProbeFail, "host", "dc.example.com:389", "reason", "refused", "detail", "dial tcp: connection refused")
	tr := said("tr", c)
	assert.Contains(t, tr, "dc.example.com:389")
	assert.Contains(t, tr, srvtext.Text("tr", "server.auth_provider.reason.refused", nil))
	assert.NotContains(t, tr, "dial tcp", "the technical detail is not the sentence")
	assert.NotContains(t, tr, "{", "every placeholder filled")
	en := said("en", c)
	assert.Contains(t, en, "Could not connect to dc.example.com:389")
	assert.NotEqual(t, tr, en)
}

func TestSayChecks_TheRequiredFieldsAreNamedInWords(t *testing.T) {
	got := said("tr", Check("required", ProbeFail, "fields", "url,base_dn"))
	assert.Contains(t, got, srvtext.Text("tr", "server.auth_provider.field.url", nil))
	assert.Contains(t, got, srvtext.Text("tr", "server.auth_provider.field.base_dn", nil))
	assert.NotContains(t, got, "base_dn,")
}

func TestSayChecks_OneOrManyByTheCount(t *testing.T) {
	assert.Equal(t, "1000+ people found with (mail=*); 998 with an email (mail).",
		said("en", Check("people", ProbeOK, "n", "1000+", "mail", "998", "filter", "(mail=*)", "attr", "mail")))
	assert.Equal(t, "Directory sync would bring in 1 group.", said("en", Check("sync_groups", ProbeOK, "n", "1")))
	assert.Equal(t, "Directory sync would bring in 3 groups.", said("en", Check("sync_groups", ProbeOK, "n", "3")))
}

func TestSayChecks_AStepTheDriverWordedKeepsItsWords(t *testing.T) {
	hint := "pamtester is not installed at /usr/bin/pamtester. Install it."
	assert.Equal(t, hint, said("tr", Check("pamtester", ProbeFail, "reason", "missing", "hint", hint)))
}

func TestSayChecks_TheEnvironmentNameIsAValue(t *testing.T) {
	got := said("tr", Check("secret", ProbeFail))
	assert.Contains(t, got, "FILEX_SECRET_KEY")
	assert.NotContains(t, got, "{env}")
}

func TestSayChecks_AStepWithNoWordsIsItsIDNeverAKey(t *testing.T) {
	got := said("en", Check("brand_new_step", ProbeOK))
	assert.Equal(t, "brand_new_step: ok", got)
	assert.False(t, strings.HasPrefix(got, "server."))
}
