package dav

import "github.com/go-chi/chi/v5"

// chi only routes HTTP methods it knows about; WebDAV's extension verbs
// (PROPFIND & friends) would otherwise be rejected with 405 by the router
// before ever reaching this handler — even through r.Mount. Registering
// them in an init keeps the fix wherever the package is imported, and it
// runs before any mux is built.
func init() {
	for _, m := range []string{
		"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK",
	} {
		chi.RegisterMethod(m)
	}
}
