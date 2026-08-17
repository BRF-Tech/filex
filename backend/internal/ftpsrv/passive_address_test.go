package ftpsrv

import (
	"net"
	"strings"
	"testing"
)

// ⚠⚠ The setting is called PUBLIC_HOST, is documented as "the address the
// client would use", and used to accept only a literal IPv4 address: a host
// name stopped the FTPS listener with "invalid passive IP" while every other
// endpoint came up healthy, so the server looked fine and one protocol was
// simply missing. Measured on fm.example.com during the v0.20.0 rollout.
func TestPassiveAddressAcceptsANameAsWellAsAnAddress(t *testing.T) {
	if got, err := passiveAddress("203.0.113.7"); err != nil || got != "203.0.113.7" {
		t.Errorf("a literal must pass through: %q, %v", got, err)
	}
	// Empty keeps the library's default (answer with the control connection's
	// own address), which is right for a direct, un-NATed deployment.
	if got, err := passiveAddress("  "); err != nil || got != "" {
		t.Errorf("empty must stay empty: %q, %v", got, err)
	}
	// A name resolves. localhost is the only name a test may rely on.
	got, err := passiveAddress("localhost")
	if err != nil {
		t.Fatalf("localhost must resolve: %v", err)
	}
	if ip := net.ParseIP(got); ip == nil || ip.To4() == nil {
		t.Errorf("resolved to %q, want an IPv4 literal", got)
	}
}

// The failures have to NAME the setting, because the operator reading the log
// has one line to work from and the previous message mentioned neither the
// variable nor what would fix it.
func TestPassiveAddressFailuresExplainThemselves(t *testing.T) {
	if _, err := passiveAddress("::1"); err == nil ||
		!strings.Contains(err.Error(), "IPv4") {
		t.Errorf("an IPv6 literal must be refused with a reason: %v", err)
	}
	_, err := passiveAddress("no-such-host.invalid")
	if err == nil || !strings.Contains(err.Error(), "no-such-host.invalid") {
		t.Errorf("an unresolvable name must be quoted back: %v", err)
	}
}
