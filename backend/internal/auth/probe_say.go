package auth

import (
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// SayChecks puts each step's sentence on checks, in lang, in place: what was
// reached (a host, a DN, an address) and, when the step failed, why - the
// reason the driver classified (refused, timeout, dns, tls, credentials ...)
// in words. The server catalogue holds the words:
//
//	server.auth_provider.check.<id>.<status>   the step
//	server.auth_provider.reason.<reason>       why it failed
//	server.auth_provider.field.<key>           a field the step names
//
// ⚠⚠ The ONE place a provider test is worded. Until 0.54 the panel built
// these sentences in the browser (web lib/providerChecks.ts, 57 step and 29
// reason strings in the panel's catalogue), so the admin MCP tool
// admin_auth_providers_test and every other API reader got step ids and
// parameter codes they could not read. A failed step a driver already
// worded (`hint`, the PAM fixes with their commands) keeps those words; the
// technical detail stays in params, for the "technical detail" line.
func SayChecks(lang string, checks []ProbeCheck) {
	lang = srvtext.Pick(lang)
	for i := range checks {
		checks[i].Text = sayCheck(lang, checks[i])
	}
}

func sayCheck(lang string, c ProbeCheck) string {
	if c.Status == ProbeFail && strings.TrimSpace(c.Params["hint"]) != "" {
		return c.Params["hint"]
	}
	vars := srvtext.Vars{}
	for k, v := range c.Params {
		vars[k] = v
	}
	if f := vars["fields"]; f != "" {
		parts := strings.Split(f, ",")
		for i, k := range parts {
			k = strings.TrimSpace(k)
			parts[i] = k
			if key := "server.auth_provider.field." + k; srvtext.Has(key) {
				parts[i] = srvtext.Text(lang, key, nil)
			}
		}
		vars["fields"] = strings.Join(parts, ", ")
	}
	if r := vars["reason"]; r != "" {
		if key := "server.auth_provider.reason." + r; srvtext.Has(key) {
			vars["reason"] = srvtext.Text(lang, key, vars)
		}
	}
	// The one environment name a step names: a value, never a word to
	// translate.
	vars["env"] = "FILEX_SECRET_KEY"
	key := "server.auth_provider.check." + c.ID + "." + c.Status
	if !srvtext.Has(key) {
		return c.ID + ": " + c.Status
	}
	// A step that counts ("1000+" past the test's cap) picks its form by it.
	if raw := strings.TrimSuffix(vars["n"], "+"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return srvtext.Plural(lang, key, n, vars)
		}
	}
	return srvtext.Text(lang, key, vars)
}
