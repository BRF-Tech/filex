package config

import "testing"

func TestStorageTLSSkipVerifyIsOffUnlessAskedFor(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"true", true},
		{"1", true},
	} {
		t.Setenv("FILEX_STORAGE_TLS_SKIP_VERIFY", tc.env)
		c, err := Load("")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if c.StorageTLS.SkipVerify != tc.want {
			t.Errorf("%q: SkipVerify = %v, want %v", tc.env, c.StorageTLS.SkipVerify, tc.want)
		}
	}
}
