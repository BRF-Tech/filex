package server

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// holdSetter is what a runtime does with a license's verdict: hold the
// named entry (reason non-empty) or let it run ("").
type holdSetter interface {
	SetLicenseHold(name, reason string)
}

// licenseHolder is the app store's Holder (appstore.Options.Holder): a
// license names an app, or - its id starting with appstore.StoragePrefix - a
// storage plugin installed from a store (#215). Each is held, or let run, by
// its own runtime; a runtime that is off (nil) holds nothing.
type licenseHolder struct {
	apps    holdSetter
	plugins holdSetter
}

// newLicenseHolder wires the two runtimes, leaving out the one that is off
// (a nil pointer in an interface would not read as off).
func newLicenseHolder(apps *wasmplugin.Registry, plugins *plugin.Manager) licenseHolder {
	var h licenseHolder
	if apps != nil {
		h.apps = apps
	}
	if plugins != nil {
		h.plugins = plugins
	}
	return h
}

// SetLicenseHold holds (reason non-empty) or releases the app or storage
// plugin a license id names.
func (h licenseHolder) SetLicenseHold(id, reason string) {
	if name, ok := strings.CutPrefix(id, appstore.StoragePrefix); ok {
		if h.plugins != nil {
			h.plugins.SetLicenseHold(name, reason)
		}
		return
	}
	if h.apps != nil {
		h.apps.SetLicenseHold(id, reason)
	}
}
