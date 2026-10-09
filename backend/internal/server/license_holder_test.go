package server

import "testing"

type recordedHolds map[string]string

func (r recordedHolds) SetLicenseHold(name, reason string) { r[name] = reason }

// A license's verdict reaches the runtime its id names (#215): an app's id
// its app, a storage plugin's ("storage:<name>") the storage plugin manager,
// by the plugin's own name - never the other one.
func TestLicenseHolder_RoutesAVerdictToItsRuntime(t *testing.T) {
	apps, plugins := recordedHolds{}, recordedHolds{}
	h := licenseHolder{apps: apps, plugins: plugins}
	h.SetLicenseHold("sign", "license: revoked")
	h.SetLicenseHold("storage:myfs", "license: missing")
	if apps["sign"] != "license: revoked" || len(apps) != 1 {
		t.Fatalf("apps %v", apps)
	}
	if plugins["myfs"] != "license: missing" || len(plugins) != 1 {
		t.Fatalf("plugins %v", plugins)
	}
	// A runtime that is off holds nothing, and nothing panics.
	off := newLicenseHolder(nil, nil)
	off.SetLicenseHold("sign", "x")
	off.SetLicenseHold("storage:myfs", "x")
}
