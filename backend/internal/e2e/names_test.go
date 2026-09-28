package e2e

import "testing"

func TestLooksEncryptedName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// What the browser stores (backend/internal/e2edecrypt/testdata/name-vectors.json).
		{"ugkmCcOceKUyA7tsdZxzltk", true},
		{"RK_chqfshg00TF_YAhrf6nAQFJIkmjUbmdrmCcSjV9Y", true},
		{"-starts-with-a-dash_0123456", true},
		{"21d4xY_T-0Cos4uxsBA6kXQ1j4JOw8-KedEwgAy-NO0.fxl", true},
		{"21d4xY_T-0Cos4uxsBA6kXQ1j4JOw8-KedEwgAy-NO0.fxl.name", true},
		// A folder: its name, a dot, its 22-character id.
		{"ugkmCcOceKUyA7tsdZxzltk.ZGVmZ2hpamtsbW5vcHFycw", true},
		{"21d4xY_T-0Cos4uxsBA6kXQ1j4JOw8-KedEwgAy-NO0.fxl.ZGVmZ2hpamtsbW5vcHFycw", true},
		// Not ours.
		{"ugkmCcOceKUyA7tsdZxzltk.ZGVmZ2hpamtsbW5vcHFyc", false}, // a 21-character "id"
		{"short.ZGVmZ2hpamtsbW5vcHFycw", false},
		{"notes.txt", false},
		{"ugkmCcOceKUyA7tsdZxzlt", false}, // 22 characters: shorter than any tag + byte
		{"has space in it 12345678901", false},
		{"short.fxl", false},
		{"21d4xY_T-0Cos4uxsBA6kXQ1j4JOw8-KedEwgAy-NO.fxl", false}, // 42-char hash
		{"", false},
		{".filex-e2e.json", false},
	}
	for _, c := range cases {
		if got := LooksEncryptedName(c.name); got != c.want {
			t.Errorf("LooksEncryptedName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
