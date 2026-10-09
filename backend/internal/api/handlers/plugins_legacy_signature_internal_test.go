package handlers

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
)

// sec055 S5: a build whose signature verified only in the old sha256-only
// form is still taken during 0.55 - and the plugin's row on the
// administrator's screen says so, in the reader's language, from the server.
func TestPluginWire_AnOldFormSignatureIsSaidOnTheRow(t *testing.T) {
	h := &Plugins{}
	r := httptest.NewRequest("GET", "/api/admin/plugins", nil)

	old := h.wire(r, &plugin.Status{Plugin: &model.Plugin{Name: "oldfs"}, LegacySignature: true})
	if !strings.Contains(old.SignatureNotice, "0.56") || !strings.Contains(old.SignatureNotice, "SHA-256") {
		t.Fatalf("the row does not say the signature is the old form: %q", old.SignatureNotice)
	}

	tr := httptest.NewRequest("GET", "/api/admin/plugins", nil)
	tr.Header.Set("Accept-Language", "tr")
	if got := h.wire(tr, &plugin.Status{Plugin: &model.Plugin{Name: "oldfs"}, LegacySignature: true}).SignatureNotice; !strings.Contains(got, "eski biçim") {
		t.Fatalf("the sentence is not in the reader's language: %q", got)
	}

	if got := h.wire(r, &plugin.Status{Plugin: &model.Plugin{Name: "newfs"}}).SignatureNotice; got != "" {
		t.Fatalf("a build signed over its name, version and platform carries a warning: %q", got)
	}
}

// A build the store gate refused is said in its own words, on the plugin
// pages and in the store review: never the generic "could not be installed"
// nor "no signature verifies" (sec055 S5).
func TestStoreSignedBuild_TheGatesRefusalHasItsOwnSentence(t *testing.T) {
	refused := fmt.Errorf("%w: the store has withdrawn myfs 1.2.0", plugin.ErrStoreBuild)
	if got := installSaid(refused, "plugin_install_failed"); got != "store_signed_build" {
		t.Fatalf("install refusal said as %q", got)
	}
	if got := installSaid(errors.New("sha256 mismatch"), "plugin_upgrade_failed"); got != "plugin_upgrade_failed" {
		t.Fatalf("an ordinary failure said as %q", got)
	}
	if got := signatureRefusalKey(refused); got != "server.store_storage.signature_store_build" {
		t.Fatalf("review refusal said as %q", got)
	}
	if got := signatureRefusalKey(errors.New("signature does not verify")); got != "server.store_storage.signature_refused" {
		t.Fatalf("an ordinary refusal said as %q", got)
	}
}
