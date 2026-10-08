package handlers

// What an audit row says, said by the server (0.54 audit, A14). Until 0.54 the
// panel composed these labels in the browser (web lib/auditLabel.ts) and the
// admin MCP tool admin_audit_list got only the wire names. These hold the
// composition the browser used to do - the same cases its tests held - now on
// the one code path every reader goes through.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditLabel_AnActionIsAResourceAndAVerbInTheReadersLanguage(t *testing.T) {
	assert.Equal(t, "User: password reset", auditActionLabel("en", "user.password_reset"))
	assert.Equal(t, "Kullanıcı: parola sıfırlandı", auditActionLabel("tr", "user.password_reset"))
	assert.Equal(t, "Encryption policy: updated", auditActionLabel("en", "e2e_policy.update"))
	assert.Equal(t, "Şifreleme politikası: güncellendi", auditActionLabel("tr", "e2e_policy.update"))
	assert.Equal(t, "Encryption: folder cleaned up", auditActionLabel("en", "e2e.folder_cleanup"))
	assert.Equal(t, "Çok kiracılı mod: açıldı", auditActionLabel("tr", "tenancy.enable"))
	// The middleware's generic fallback names a route segment with a dash.
	assert.Equal(t, "Uygulama: oluşturuldu", auditActionLabel("tr", "app-plugins.create"))
	for _, lang := range []string{"en", "tr"} {
		got := auditActionLabel(lang, "user.password_reset")
		assert.NotContains(t, got, "user.password_reset")
		assert.NotContains(t, got, "_")
	}
}

func TestAuditLabel_AnActionThroughTheAIAdminSurfaceIsTheSameActionMarked(t *testing.T) {
	got := auditActionLabel("tr", "ai.user.create")
	assert.Contains(t, got, "Kullanıcı")
	assert.Contains(t, got, "(AI)")
	assert.NotContains(t, got, "ai user")
}

func TestAuditLabel_AnActionTheCatalogueHasNeverHeardOfIsStillReadable(t *testing.T) {
	assert.Equal(t, "mystery things: frobnicate", auditActionLabel("en", "mystery-things.frobnicate"))
	assert.Equal(t, "-", auditActionLabel("en", ""))
}

func TestAuditLabel_ATargetIsItsKindAndWhichOne(t *testing.T) {
	assert.Equal(t, "Kullanıcı “ayse@example.com”", auditTargetLabel("tr", "user", "12", "ayse@example.com"))
	assert.Equal(t, "Kullanıcı #12", auditTargetLabel("tr", "user", "12", ""))
	assert.Equal(t, "Tenant “acme”", auditTargetLabel("en", "providers", "3", "acme"))
	assert.Equal(t, "Encryption policy", auditTargetLabel("en", "e2e_policy", "", ""))
	assert.Equal(t, "Şifreleme isteği “Dosyalar://Maaşlar”", auditTargetLabel("tr", "e2e_request", "7", "Dosyalar://Maaşlar"))
	assert.Equal(t, "Çok kiracılı mod", auditTargetLabel("tr", "tenancy", "", ""))
	// The generic fallback's target is the route segment: the resource's word
	// stands in for a target word it has none of.
	assert.Equal(t, "Uygulama", auditTargetLabel("tr", "app-plugins", "", ""))
	assert.Equal(t, "Uygulama “board”", auditTargetLabel("tr", "app-plugins", "board", ""))
}

func TestAuditLabel_ADemoMaskedAddressIsSaidInWords(t *testing.T) {
	byID := auditTargetLabel("tr", "login", demoMaskedIP, "")
	assert.Contains(t, byID, "demoda gizli")
	assert.NotContains(t, byID, demoMaskedIP)
	byName := auditTargetLabel("tr", "login", demoMaskedIP, demoMaskedIP)
	assert.Contains(t, byName, "demoda gizli")
	assert.NotContains(t, byName, demoMaskedIP)
	assert.Contains(t, auditTargetLabel("en", "login", "ada@example.com", ""), "ada@example.com")
}

// The "What" filter reaches the spellings a row can carry: the fixed action's
// and the admin route segment's, under one name.
func TestAuditLabel_TheResourceFilterNamesEverySpellingOnce(t *testing.T) {
	opts := auditResourceOptions("tr")
	require.NotEmpty(t, opts)
	labels := map[string]bool{}
	var login, appPlugins string
	for _, o := range opts {
		assert.False(t, labels[o.Label], "one entry per label: %q", o.Label)
		labels[o.Label] = true
		for _, p := range strings.Split(o.Value, ",") {
			assert.True(t, strings.HasSuffix(p, "."), "a prefix ends with a dot: %q", p)
			switch p {
			case "login-security.":
				login = o.Value
			case "app-plugins.":
				appPlugins = o.Value
			}
		}
	}
	assert.Contains(t, login, "login_security.", "both spellings of sign-in security under one name")
	assert.NotEmpty(t, appPlugins)
	// Sorted by the label in the reader's order.
	for i := 1; i < len(opts); i++ {
		assert.NotEqual(t, opts[i-1].Label, opts[i].Label)
	}
}
