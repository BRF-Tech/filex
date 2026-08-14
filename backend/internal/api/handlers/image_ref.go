package handlers

import (
	"errors"
	"fmt"
	"strings"
)

// An "image reference" is what filex stores when the operator hands us a
// picture without a file to put it in: the branding logo and, since 00023, a
// user's profile picture. Both accept the same three shapes — an inline
// data:image/… URI, an absolute http(s) URL, or a site-relative path — and both
// need the same refusal for anything else, so the rule lives in one place
// rather than being written twice with two different sets of holes.

// validateImageRef checks one image reference. field names the setting in the
// error message ("branding.logo_url", "avatar_url"); maxBytes caps an inline
// data URI. An empty value always passes — that is how a picture is cleared.
func validateImageRef(field, value string, maxBytes int) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(v, "data:"):
		if !strings.HasPrefix(v, "data:image/") {
			return fmt.Errorf("%s data URI must be an image", field)
		}
		if len(v) > maxBytes {
			return fmt.Errorf("%s data URI exceeds %d KB", field, maxBytes/1024)
		}
	case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"), strings.HasPrefix(v, "/"):
		if len(v) > 2048 {
			return fmt.Errorf("%s is too long", field)
		}
	default:
		return errors.New(field + " must be an http(s) URL, a site-relative path or a data:image/… URI")
	}
	return nil
}
