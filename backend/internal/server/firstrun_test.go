package server

import (
	"strings"
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	pw, err := generatePassword(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 16 {
		t.Fatalf("len=%d", len(pw))
	}
	if strings.ContainsAny(pw, " \t\n\r") {
		t.Fatal("password contains whitespace")
	}
}

func TestRandomHex(t *testing.T) {
	a, _ := RandomHex(8)
	b, _ := RandomHex(8)
	if a == b {
		t.Fatal("non-random output")
	}
	if len(a) != 16 {
		t.Fatalf("hex length wrong: %d", len(a))
	}
}
