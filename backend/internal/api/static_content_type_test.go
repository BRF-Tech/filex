package api

import "testing"

// TestContentTypeForName_Webmanifest pins the header for the PWA manifest.
//
// It exists because the answer used to be "whatever net/http sniffs", which for
// a JSON body is `text/plain; charset=utf-8`. Chrome installs the app regardless,
// so nothing looked broken — but the header was wrong on every platform and
// web/cypress/e2e/90-pwa-install.cy.ts could only assert "at least it is not
// HTML". A wrong Content-Type on the one file that declares the app's identity
// is worth a line of test.
func TestContentTypeForName_Webmanifest(t *testing.T) {
	for _, name := range []string{"manifest.webmanifest", "assets/app.WEBMANIFEST"} {
		if got, want := contentTypeForName(name), "application/manifest+json"; got != want {
			t.Errorf("contentTypeForName(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestHasAssetExt_Webmanifest — a MISSING manifest must 404, not fall through to
// index.html. The SPA fallback answering HTML with a 200 is the realistic break:
// it leaves the app un-installable while every status code says fine.
func TestHasAssetExt_Webmanifest(t *testing.T) {
	if !hasAssetExt("manifest.webmanifest") {
		t.Error("hasAssetExt(\"manifest.webmanifest\") = false — a missing manifest would be served index.html")
	}
}
