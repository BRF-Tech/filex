package realtime

import (
	"testing"
	"time"
)

func TestTicketStore_MintConsumeSingleUse(t *testing.T) {
	s := NewTicketStore()
	tok, err := s.Mint(Ticket{UserID: 7, Name: "ayse"}, time.Minute)
	if err != nil || tok == "" {
		t.Fatalf("mint: tok=%q err=%v", tok, err)
	}

	got, ok := s.Consume(tok)
	if !ok || got.UserID != 7 || got.Name != "ayse" {
		t.Fatalf("consume: ok=%v got=%+v", ok, got)
	}
	// Single-use: a second consume fails.
	if _, ok := s.Consume(tok); ok {
		t.Fatal("ticket consumed twice")
	}
}

func TestTicketStore_Expiry(t *testing.T) {
	s := NewTicketStore()
	tok, _ := s.Mint(Ticket{UserID: 1}, -time.Second) // already expired
	if _, ok := s.Consume(tok); ok {
		t.Fatal("expired ticket accepted")
	}
	if _, ok := s.Consume("nope"); ok {
		t.Fatal("unknown token accepted")
	}
}

func TestClient_AllowsPath_Confinement(t *testing.T) {
	unconfined := &Client{}
	if !unconfined.AllowsPath("s3", "anything/here") {
		t.Fatal("unconfined client must allow any path")
	}

	c := &Client{Confined: true, ConfineAdapter: "s3", ConfineRel: "projeler/proje-x"}
	cases := []struct {
		adapter, rel string
		want         bool
	}{
		{"s3", "projeler/proje-x", true},     // the root itself
		{"s3", "projeler/proje-x/alt", true}, // under the root
		{"s3", "projeler/proje-y", false},    // sibling project
		{"s3", "projeler/proje-xy", false},   // prefix-but-not-segment
		{"s3", "", false},                    // storage root
		{"other", "projeler/proje-x", false}, // wrong adapter
	}
	for _, tc := range cases {
		if got := c.AllowsPath(tc.adapter, tc.rel); got != tc.want {
			t.Errorf("AllowsPath(%q,%q)=%v want %v", tc.adapter, tc.rel, got, tc.want)
		}
	}

	// Adapter-root confinement (empty Rel) allows everything in that adapter.
	root := &Client{Confined: true, ConfineAdapter: "s3", ConfineRel: ""}
	if !root.AllowsPath("s3", "a/b/c") || root.AllowsPath("other", "x") {
		t.Fatal("adapter-root confinement wrong")
	}
}
