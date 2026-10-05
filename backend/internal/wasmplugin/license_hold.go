package wasmplugin

// ── License holds (filex 0.52.0, internal/appstore) ──────────────────────
//
// A paid app installed from a store runs while its license holds. When the
// store says the license is revoked, expired, for another app, out of seats
// or invalid - or the store could not be asked and the grace it signed has
// ended - the app is HELD: it stays installed, with its settings, its records
// and its files, and runs nothing (State answers StateUnlicensed) until the
// license holds again. Nothing is removed by a license.

// SetLicenseHold holds app (reason != "") or releases it (""). The hold is
// kept by name, so it outlives an upgrade (which replaces the entry) and
// applies to an app installed after it was set.
func (r *Registry) SetLicenseHold(app, reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.holds == nil {
		r.holds = map[string]string{}
	}
	if reason == "" {
		delete(r.holds, app)
	} else {
		r.holds[app] = reason
	}
	// ⚠ The entry's hold is set under the registry lock, as put sets a new
	// entry's: two calls (or a call and an upgrade's put) cannot leave the
	// entry with a hold the registry no longer has, or without one it has.
	// Lock order: r.mu, then p.mu - as Close takes them.
	p := r.byName[app]
	before := ""
	if p != nil {
		before = p.holdReason()
		p.setHold(reason)
	}
	r.mu.Unlock()
	if p != nil {
		switch {
		case reason != "" && before != reason:
			p.log("warn", "held by its license: "+reason)
		case reason == "" && before != "":
			p.log("info", "its license holds again; the app runs")
		}
	}
}

// LicenseHold answers why app is held ("" = it is not).
func (r *Registry) LicenseHold(app string) string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.holds[app]
}

func (p *Installed) setHold(reason string) {
	p.mu.Lock()
	p.hold = reason
	p.mu.Unlock()
}

func (p *Installed) holdReason() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hold
}

// loadState is the state of the app's loading alone - what compile decided -
// without its license hold: what an install or an upgrade proves the module
// by.
func (p *Installed) loadState() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state, p.stateErr
}
