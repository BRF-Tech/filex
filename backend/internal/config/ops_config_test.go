package config

import "testing"

// FILEX_OPS_DELETE_WORKERS bounds how many items of ONE delete job are put in
// the trash at once. Unset, zero, negative or not a number all mean the
// default: a knob that could be set to "stop deleting" is not a knob anybody
// wants.
func TestOpsDeleteWorkers(t *testing.T) {
	if got := Default().Ops.DeleteWorkers; got != 4 {
		t.Fatalf("default delete workers = %d, want 4", got)
	}

	t.Setenv("FILEX_OPS_DELETE_WORKERS", "8")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Ops.DeleteWorkers != 8 {
		t.Fatalf("FILEX_OPS_DELETE_WORKERS=8 gave %d", cfg.Ops.DeleteWorkers)
	}

	for _, bad := range []string{"0", "-3", "many"} {
		t.Setenv("FILEX_OPS_DELETE_WORKERS", bad)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Ops.DeleteWorkers != 4 {
			t.Fatalf("FILEX_OPS_DELETE_WORKERS=%q gave %d, want the default 4", bad, cfg.Ops.DeleteWorkers)
		}
	}
}
