package handlers

import (
	"strconv"

	"github.com/brf-tech/filex/backend/internal/thumb"
)

// thumbURL renders the `thumb_url` field for a node id, stamped by `signer`.
//
// The stamp is what makes a bare `<img src>` work: the caller reading this
// listing has already cleared tenancy and ACL for the node, so the URL carries
// that decision forward for a client that cannot send a header. When no
// signature can be minted (nil signer, unreachable settings row) the URL is
// still emitted unsigned — every in-repo consumer fetches it with credentials
// and is authorized per request, so an unsigned URL degrades to "authenticated
// callers only" rather than to a missing thumbnail.
//
// ⚠⚠ The signer is threaded through the handlers rather than kept in a
// package-level variable, because the verifying half already is per-router
// (handlers.Thumb.Signer). A process that builds two routers — which is exactly
// what a test binary does — would otherwise have one instance's listing minting
// stamps the other instance's endpoint cannot verify, and the failure is
// invisible: every in-repo client falls back to its authenticated fetch and
// only a bare <img> in an embed goes blank.
func thumbURL(signer *thumb.Signer, id int64) string {
	u := "/api/files/thumb/" + strconv.FormatInt(id, 10)
	if q := signer.Query(id); q != "" {
		u += "?" + q
	}
	return u
}
