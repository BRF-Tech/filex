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

func selfSignedFTPS(t *testing.T) *Driver {
	t.Helper()
	cert, _ := selfSigned(t)
	s := newFakeFTP(t)
	s.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}}
	return ftpDriver(t, s.addr(), map[string]any{"tls": true})
}

func TestSelfSignedFTPS_RefusedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	d := selfSignedFTPS(t)

	if err := d.Write(context.Background(), "/x.bin", bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("an FTPS server nothing trusts was accepted with the check on")
	}
}

func TestSelfSignedFTPS_AcceptedWithTheCheckOff(t *testing.T) {
	storage.SetSkipTLSVerify(true)
	t.Cleanup(func() { storage.SetSkipTLSVerify(false) })
	d := selfSignedFTPS(t)

	if err := d.Write(context.Background(), "/x.bin", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatalf("FTPS upload with the check off: %v", err)
	}
}
