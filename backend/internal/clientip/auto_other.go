//go:build !linux

package clientip

import "errors"

// linkKinds has nothing to ask outside Linux: there filex is a plain install
// (ResolveAuto answers loopback only before it would get here).
func linkKinds() (map[string]string, error) {
	return nil, errors.New("link kinds are read on Linux only")
}
