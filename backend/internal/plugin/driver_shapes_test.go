package plugin

import (
	"os"
	"os/exec"
	"testing"
)

// TestGeneratedShapesAreCurrent is the check gen/main.go's header promises.
//
// driver_shapes.go is generated, and a generated file that nobody verifies is
// a file that silently drifts: add an axis to Capabilities, forget to re-run
// the generator, and the shape factory keeps handing filex a value whose
// method set no longer matches what the plugin declared — a capability that
// exists on paper and is never type-asserted anywhere. That failure is
// invisible at compile time, which is exactly why it needs a test.
//
// It shells out to the generator rather than importing it: gen is package
// main, and running it the way a developer runs it is also the only way to
// prove the documented command still works.
func TestGeneratedShapesAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the generator through the go toolchain")
	}
	want, err := os.ReadFile("driver_shapes.go")
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	out, err := exec.Command("go", "run", "./gen").Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("run generator: %v\n%s", err, stderr)
	}
	if string(out) != string(want) {
		t.Fatalf("driver_shapes.go is stale: it does not match the generator.\n"+
			"Regenerate it with:\n"+
			"\tgo run ./internal/plugin/gen > internal/plugin/driver_shapes.go\n"+
			"generated %d bytes, file has %d", len(out), len(want))
	}
}
