package ftp

// Issue #134: an FTPS server with a certificate nothing trusts. Unlike
// TestFTPS_DataConnectionsSpeakTLS these do not hand the driver the stand-in's
// root, so what connects is the switch and nothing else.

import (
	"bytes"
	"context"
	"crypto/tls"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func selfSignedFTPS(t *testing.T, extra map[string]any) *Driver {
	t.Helper()
	cert, _ := selfSigned(t)
	s := newFakeFTP(t)
	s.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}}
	cfg := map[string]any{"tls": true}
	for k, v := range extra {
		cfg[k] = v
	}
	return ftpDriver(t, s.addr(), cfg)
}

func TestSelfSignedFTPS_RefusedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	d := selfSignedFTPS(t, nil)

	if err := d.Write(context.Background(), "/x.bin", bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("an FTPS server nothing trusts was accepted with the check on")
	}
}

func TestSelfSignedFTPS_AcceptedWithTheCheckOff(t *testing.T) {
	d := selfSignedFTPS(t, map[string]any{storage.SkipTLSVerifyKey: true})

	if err := d.Write(context.Background(), "/x.bin", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatalf("FTPS upload with the check off: %v", err)
	}
}

// The data connection is a TLS connection of its own: the switch has to reach
// it as well as the control connection, and the next storage must not hear it.
func TestSelfSignedFTPS_TheSwitchIsTheStoragesOwn(t *testing.T) {
	on := selfSignedFTPS(t, map[string]any{storage.SkipTLSVerifyKey: true})
	off := selfSignedFTPS(t, nil)

	if err := on.Write(context.Background(), "/x.bin", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatalf("the storage with the check off: %v", err)
	}
	if err := off.Write(context.Background(), "/x.bin", bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("the storage with the check on took the other one's answer")
	}
}
