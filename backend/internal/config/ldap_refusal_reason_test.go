package config

import "testing"

// show_refusal_reason is off unless the operator says so - in config.yaml or
// FILEX_LDAP_SHOW_REFUSAL_REASON (docs/CONFIGURATION.md).
func TestLDAPShowRefusalReason_OffUnlessSaid(t *testing.T) {
	t.Setenv("FILEX_LDAP_SHOW_REFUSAL_REASON", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.LDAP.ShowRefusalReason {
		t.Fatal("show_refusal_reason must default to off")
	}
	t.Setenv("FILEX_LDAP_SHOW_REFUSAL_REASON", "true")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.LDAP.ShowRefusalReason {
		t.Fatal("FILEX_LDAP_SHOW_REFUSAL_REASON=true must switch it on")
	}
}
