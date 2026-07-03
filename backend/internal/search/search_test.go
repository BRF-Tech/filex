package search

import "testing"

func TestSQLLike(t *testing.T) {
	if got := SQLLike("foo"); got != "%foo%" {
		t.Fatalf("got %q", got)
	}
	if got := SQLLike("a%b"); got != `%a\%b%` {
		t.Fatalf("got %q", got)
	}
	if got := SQLLike(""); got != "%" {
		t.Fatalf("got %q", got)
	}
}
