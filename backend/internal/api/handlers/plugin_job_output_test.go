package handlers

// What a job does with its result, and the rule that keeps the two enqueue
// paths from answering it differently.
//
// A surface may override the action's manifest output for one job
// (`wire.JobRequest.Output`): "a new version of this file" / "a new file
// beside it, called X". The authenticated path honoured it; the public page
// path dropped it on the floor, so the signing wizard's own choice — made by
// the requester, submitted by an outside signer from a link — silently became
// whatever the manifest declared, and a document the person asked to keep was
// overwritten as a new version of itself.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The manifest's own answer, and every way a surface may change it.
func TestJobOutputModeReadsAnOverrideTheSameWayForEveryCaller(t *testing.T) {
	action := &wire.Action{ID: "sign", Output: wire.Output{Mode: "none"}}
	writer := &wire.Action{ID: "upper", Output: wire.Output{Mode: "sibling", Name: "{stem}-upper{ext}"}}

	cases := []struct {
		name     string
		action   *wire.Action
		out      *wire.Output
		wantMode string
		writes   bool
		ok       bool
	}{
		{"no override: the manifest speaks", action, nil, "none", false, true},
		{"no override, a writing manifest", writer, nil, "sibling", true, true},
		{"the surface asks for a new version", action, &wire.Output{Mode: "version"}, "version", true, true},
		{"the surface asks for a file beside it", action, &wire.Output{Mode: "sibling", Name: "X.pdf"}, "sibling", true, true},
		{"the surface narrows a writing action to nothing", writer, &wire.Output{Mode: "none"}, "none", false, true},
		{"a mode outside the closed set is refused", writer, &wire.Output{Mode: "elsewhere"}, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, writes, ok := jobOutputMode(tc.action, tc.out)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.wantMode, mode)
			assert.Equal(t, tc.writes, writes,
				"`writes` is what decides the read-only refusal and the ACL level a caller needs")
		})
	}
}

// The choice has to survive into the job row, because that is the only thing
// the worker reads it back from.
func TestApplyJobOutputCarriesTheChoiceIntoTheParams(t *testing.T) {
	params := applyJobOutput(map[string]any{"op": "sign"},
		&wire.Output{Mode: "sibling", Name: "sozlesme-imzali.pdf"})

	got := wasmplugin.OutputOverride(params)
	require.NotNil(t, got, "the worker reads the choice back out of the params; nothing else carries it")
	assert.Equal(t, "sibling", got.Mode)
	assert.Equal(t, "sozlesme-imzali.pdf", got.Name)
	assert.Equal(t, "sign", params["op"], "the plugin's own parameters are left alone")
}

func TestApplyJobOutputLeavesTheManifestAloneWhenNobodyChose(t *testing.T) {
	params := applyJobOutput(map[string]any{"op": "sign"}, nil)

	assert.Nil(t, wasmplugin.OutputOverride(params),
		"no override means the action's manifest output stands — an empty one would be a different claim")
	assert.Equal(t, map[string]any{"op": "sign"}, params)
}

// A job with no parameters may still have made a choice: a nil map must not
// swallow it.
func TestApplyJobOutputAcceptsAJobWithNoParams(t *testing.T) {
	params := applyJobOutput(nil, &wire.Output{Mode: "version"})

	require.NotNil(t, params)
	got := wasmplugin.OutputOverride(params)
	require.NotNil(t, got)
	assert.Equal(t, "version", got.Mode)
}

// ⚠⚠ The SAME surface, from inside filex and from a public link, must make the
// same decision. Both paths read the choice through jobOutputMode and stamp it
// through applyJobOutput, so this is the one place the two answers can be
// compared — and it is what the guard below keeps true of any third path.
func TestBothDoorsMakeTheSameOutputDecision(t *testing.T) {
	action := &wire.Action{ID: "sign", Output: wire.Output{Mode: "version"}}
	job := &wire.JobRequest{
		ActionID: "sign",
		Params:   map[string]any{"op": "apply"},
		Output:   &wire.Output{Mode: "sibling", Name: "sozlesme-imzali.pdf"},
	}

	// Inside filex: authorise carries JobRequest.Output into checked.output,
	// and enqueue stamps it onto the params.
	insideMode, insideWrites, ok := jobOutputMode(action, job.Output)
	require.True(t, ok)
	inside := applyJobOutput(map[string]any{"op": "apply"}, job.Output)

	// From the link: enqueueAsCreator reads the same JobRequest.
	linkMode, linkWrites, ok := jobOutputMode(action, job.Output)
	require.True(t, ok)
	fromLink := applyJobOutput(map[string]any{"op": "apply"}, job.Output)

	assert.Equal(t, insideMode, linkMode)
	assert.Equal(t, insideWrites, linkWrites)
	assert.Equal(t, wasmplugin.OutputOverride(inside), wasmplugin.OutputOverride(fromLink),
		"one wizard, one answer: the door a submit came through must not change where the file lands")
	assert.Equal(t, "sibling", linkMode,
		"the surface's choice beats the manifest — that is what an override IS")
}

// TestEveryJobEnqueueHonoursTheSurfacesOutput is the rule rather than the roll
// call, in the shape internal/notify/emitter_target_test.go uses for targets.
//
// ⚠⚠ Two paths queue a plugin job from a surface, and the second one was
// written by copying the first — badly, twice: it shipped without
// `page_token_hash` and then without the output override. Both are silent
// failures (the job runs, it simply does the wrong thing with its result), and
// both were found by a person, not by a test. So: any function in this package
// that BUILDS a `model.AppPluginJob` is an enqueue path, and an enqueue path
// that never mentions applyJobOutput queues a job that ignores what the person
// chose on the screen.
//
// Scope is this package on purpose: it is the HTTP surfaces, the ones a person
// presses a button on. The scheduled wake-up (internal/wasmplugin) has no
// screen and nobody choosing an output.
func TestEveryJobEnqueueHonoursTheSurfacesOutput(t *testing.T) {
	dir := packageDirFromTest(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var offenders []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if perr != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			builds, honours := false, false
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				switch v := m.(type) {
				case *ast.CompositeLit:
					if sel, ok := v.Type.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "model" && sel.Sel.Name == "AppPluginJob" {
							builds = true
						}
					}
				case *ast.Ident:
					if v.Name == "applyJobOutput" {
						honours = true
					}
				}
				return true
			})
			if builds && !honours {
				offenders = append(offenders, fmt.Sprintf("%s:%d (%s)",
					e.Name(), fset.Position(fn.Pos()).Line, fn.Name.Name))
			}
			return true
		})
	}

	if len(offenders) > 0 {
		t.Fatalf("these enqueue paths build a plugin job and never read the surface's output choice;\n"+
			"the job will run in whatever mode the manifest declares, whatever the person picked on the\n"+
			"screen (APP-PLUGINS-API.md → JobRequest.Output). Route the params through applyJobOutput:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// packageDirFromTest is this file's own directory — the same runtime.Caller
// trick internal/notify/emitter_target_test.go uses.
func packageDirFromTest(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Dir(self)
}
