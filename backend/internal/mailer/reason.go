package mailer

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/textproto"
	"strings"
)

// Reason names why an SMTP check or send failed, for a screen that has to SAY
// it: the admin settings' "Send test" printed the Go error ("dial tcp
// 10.0.0.5:587: connect: connection refused", "535 5.7.8 Authentication
// credentials invalid") as the only answer (QA, 2026-09-21). The screen now
// says the reason in the reader's language and keeps the error text as a
// second line.
//
// The reasons, in the order a connection meets them:
//
//	not_configured  no host/port saved
//	host            the name does not resolve
//	connect         nothing answered at host:port (refused, timed out, unreachable)
//	tls             the TLS handshake or the certificate failed
//	starttls        STARTTLS was required and the server does not offer it
//	auth            the server rejected the username or password
//	recipient       the server refused the address (5xx on RCPT)
//	rejected        any other SMTP refusal
//	other           anything else
func Reason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrNotConfigured) {
		return "not_configured"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "host"
	}
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) || errors.As(err, &hostErr) || errors.As(err, &invalid) {
		return "tls"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "does not advertise starttls") {
		return "starttls"
	}
	var tp *textproto.Error
	if errors.As(err, &tp) {
		switch {
		case tp.Code == 530 || tp.Code == 534 || tp.Code == 535 || tp.Code == 538:
			return "auth"
		case tp.Code >= 550 && tp.Code <= 553:
			return "recipient"
		default:
			return "rejected"
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "connect"
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "connect"
	}
	switch {
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate"):
		return "tls"
	case strings.Contains(msg, "auth"):
		return "auth"
	}
	return "other"
}
