package sync

import "testing"

func TestEtagDrift(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", false},
		{`"abc"`, "abc", false},
		{"abc-3", "abc-3", false},
		{"abc-3", "abc-2", true},
		{"", "", false},
		{"x", "", true},
	}
	for _, c := range cases {
		if got := etagDrift(c.a, c.b); got != c.want {
			t.Errorf("etagDrift(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCountParts(t *testing.T) {
	if got := CountParts(`"abc-3"`); got != 3 {
		t.Errorf("CountParts: %d", got)
	}
	if got := CountParts("abc"); got != 1 {
		t.Errorf("CountParts no suffix: %d", got)
	}
}
