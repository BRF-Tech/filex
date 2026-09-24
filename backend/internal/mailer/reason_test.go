package mailer

import (
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"testing"
)

// The admin "Send test" says WHY in words; these are the Go errors it meets.
func TestReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrNotConfigured, "not_configured"},
		{&net.DNSError{Err: "no such host", Name: "smtp.example.invalid"}, "host"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}, "connect"},
		{errors.New("server does not advertise STARTTLS"), "starttls"},
		{&textproto.Error{Code: 535, Msg: "5.7.8 Authentication credentials invalid"}, "auth"},
		{fmt.Errorf("rcpt: %w", &textproto.Error{Code: 550, Msg: "5.1.1 no such user"}), "recipient"},
		{&textproto.Error{Code: 554, Msg: "transaction failed"}, "rejected"},
		{errors.New("tls: first record does not look like a TLS handshake"), "tls"},
		{errors.New("something odd"), "other"},
		{nil, ""},
	} {
		if got := Reason(tc.err); got != tc.want {
			t.Errorf("Reason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
