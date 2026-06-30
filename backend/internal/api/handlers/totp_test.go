package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TestVerifyTOTP proves the second factor is real: only the live RFC-6238
// code for the secret passes. The previous placeholder accepted ANY 6-digit
// string, so the "wrong but well-formed" case is the load-bearing assertion.
func TestVerifyTOTP(t *testing.T) {
	secret, err := generateTotpSecret()
	if err != nil {
		t.Fatalf("generateTotpSecret: %v", err)
	}
	now := time.Now()
	valid, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	// A 6-digit code that is NOT the live one. Flip the first digit so it
	// can never accidentally equal `valid`.
	wrong := "0" + valid[1:]
	if valid[0] == '0' {
		wrong = "1" + valid[1:]
	}

	cases := []struct {
		name   string
		secret string
		code   string
		want   bool
	}{
		{"valid live code", secret, valid, true},
		{"valid with surrounding spaces", secret, " " + valid + " ", true},
		{"wrong 6-digit code", secret, wrong, false},
		{"too short", secret, "12345", false},
		{"non-numeric", secret, "abcdef", false},
		{"empty code", secret, "", false},
		{"empty secret", "", valid, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyTOTP(tc.secret, tc.code); got != tc.want {
				t.Fatalf("verifyTOTP(%q,%q)=%v want %v", tc.secret, tc.code, got, tc.want)
			}
		})
	}
}

// TestRenderQRSVG checks we emit a real QR (vector path) and not the old
// "QR placeholder" stub the frontend used to v-html.
func TestRenderQRSVG(t *testing.T) {
	svg := renderQRSVG("otpauth://totp/filex:a@b?secret=JBSWY3DPEHPK3PXP&issuer=filex")
	if !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("expected an <svg> root, got: %.40q", svg)
	}
	if strings.Contains(svg, "QR placeholder") {
		t.Fatal("placeholder SVG is still being returned")
	}
	if !strings.Contains(svg, "<path") {
		t.Fatal("expected QR module <path> data")
	}
}
