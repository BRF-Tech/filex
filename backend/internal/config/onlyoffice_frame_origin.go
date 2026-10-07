package config

import (
	"fmt"
	"strings"
)

// normalizeOfficeFrameOrigin reads FILEX_ONLYOFFICE_FRAME_ORIGIN
// (`external_services.onlyoffice.frame_origin`, task #92): the origin the
// ONLYOFFICE editor's frame is served on - normally the document server's own
// (https://docs.example.com), whose reverse proxy sends one path,
// /filex-frame/editor, to filex. "" stays "" (no such frame: the editor runs in
// a frame on FILEX_APP_UI_ORIGIN when there is one, else in filex's page).
//
// A scheme and a host with an optional port, lower-cased, no trailing slash;
// never filex's own origin (a frame there IS filex - it would isolate
// nothing) and never the app-interface origin (that host answers the
// interface route only; leave this empty to use it).
func normalizeOfficeFrameOrigin(raw, publicURL, appUIOrigin string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	origin, ok := bareOrigin(v)
	if !ok {
		return "", fmt.Errorf("onlyoffice.frame_origin (FILEX_ONLYOFFICE_FRAME_ORIGIN) %q is not an origin: write a scheme and a host, normally the document server's own, e.g. https://docs.example.com", v)
	}
	if isPublicOrigin(publicURL, origin) {
		return "", fmt.Errorf("onlyoffice.frame_origin (FILEX_ONLYOFFICE_FRAME_ORIGIN) %q is filex's own origin: name the document server's origin (a frame on filex's own origin would run its script with filex's session)", v)
	}
	if appUIOrigin != "" && strings.EqualFold(appUIOrigin, origin) {
		return "", fmt.Errorf("onlyoffice.frame_origin (FILEX_ONLYOFFICE_FRAME_ORIGIN) %q is the app-interface origin (FILEX_APP_UI_ORIGIN): leave it empty and the editor's frame is served there", v)
	}
	return origin, nil
}
