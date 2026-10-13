package config

import "testing"

// FILEX_SEARCH_MEMORY_PERCENT (issue #115): the share of the memory filex was
// given that indexing may take. Its bounds are applied where the budget is
// set (server.setSearchMemoryBudget); here it is read as the operator wrote it.
func TestSearchMemoryPercent(t *testing.T) {
	if got := Default().Search.MemoryPercent; got != 25 {
		t.Fatalf("default = %d, want 25", got)
	}
	cases := []struct {
		env  string
		want int
	}{
		{"40", 40},
		{" 30% ", 30},
		{"75", 75},
		{"half", DefaultMemoryPercent},
	}
	for _, c := range cases {
		t.Setenv("FILEX_SEARCH_MEMORY_PERCENT", c.env)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Search.MemoryPercent != c.want {
			t.Errorf("FILEX_SEARCH_MEMORY_PERCENT=%q read as %d, want %d", c.env, cfg.Search.MemoryPercent, c.want)
		}
	}
}
