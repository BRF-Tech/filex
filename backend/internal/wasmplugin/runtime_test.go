package wasmplugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

const fixtureWasm = "testdata/echo/echo.wasm"

// One compiled fixture per test binary: compiling a 3 MB module costs a few
// seconds, instantiating it per call costs milliseconds — which is also the
// production shape (compile at install, instance per call).
var (
	sharedOnce sync.Once
	sharedRT   *Runtime
	sharedC    *Compiled
	sharedErr  error
)

// fixture returns the compiled echo plugin, or skips when the wasm has not
// been built (scripts/build-wasm-fixture.sh). Where the artefact is built
// first (GitLab CI sets FILEX_REQUIRE_WASM_FIXTURE=1) a missing file is a
// failure, not a skip.
func fixture(t *testing.T) *Compiled {
	t.Helper()
	if _, err := os.Stat(fixtureWasm); err != nil {
		if os.Getenv("FILEX_REQUIRE_WASM_FIXTURE") != "" {
			t.Fatalf("%s is missing on CI: run scripts/build-wasm-fixture.sh before go test", fixtureWasm)
		}
		t.Skipf("%s not built (bash scripts/build-wasm-fixture.sh)", fixtureWasm)
	}
	sharedOnce.Do(func() {
		raw, err := os.ReadFile("testdata/echo/manifest.json")
		if err != nil {
			sharedErr = err
			return
		}
		m, err := ParseManifest(raw)
		if err != nil {
			sharedErr = err
			return
		}
		dir, _ := os.MkdirTemp("", "wasmplugin-cache-")
		sharedRT, err = NewRuntime(filepath.Join(dir, "cache"))
		if err != nil {
			sharedErr = err
			return
		}
		sharedC, sharedErr = sharedRT.Compile(context.Background(), fixtureWasm, "", m, nil, func(level, msg string) {})
	})
	require.NoError(t, sharedErr)
	// A copy so a test may swap the manifest without touching the others.
	c := *sharedC
	return &c
}

func runAction(t *testing.T, c *Compiled, action string, budget time.Duration) ([]byte, error) {
	t.Helper()
	in, _ := json.Marshal(wire.ActionRunInput{JobID: "job-1", ActionID: action, Actor: wire.Actor{ID: 1}})
	return c.Call(context.Background(), "action_run", in, budget)
}

func TestDescribe_EchoesTheInstalledManifest(t *testing.T) {
	c := fixture(t)
	got, err := c.Describe(context.Background(), "tr")
	require.NoError(t, err)
	assert.Equal(t, "echo", got.Name)
	assert.Equal(t, "0.0.1", got.Version)
	assert.Equal(t, "Yankı", got.Label.Get("tr"))
	raw, _ := os.ReadFile("testdata/echo/manifest.json")
	want, err := ParseManifest(raw)
	require.NoError(t, err)
	assert.ElementsMatch(t, want.Permissions, got.Permissions, "describe echoes the manifest on disk")
}

func TestDescribe_RefusesAModuleThatDisagreesWithTheManifest(t *testing.T) {
	c := fixture(t)
	// Same module, a manifest claiming another version: the file on disk is
	// the operator's intent, the guest's answer is the proof it is that program.
	other := *c.Manifest
	other.Version = "9.9.9"
	c.Manifest = &other
	_, err := c.Describe(context.Background(), "en")
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeRefused), err)
}

func TestAction_RunsAndReturnsJSON(t *testing.T) {
	c := fixture(t)
	out, err := runAction(t, c, "upper", 10*time.Second)
	require.NoError(t, err)
	var res wire.ActionRunOutput
	require.NoError(t, json.Unmarshal(out, &res))
	assert.True(t, res.OK)
	assert.Equal(t, "ok JOB-1", res.Message.Get("en"))
}

func TestView_RoundTrip(t *testing.T) {
	c := fixture(t)
	in, _ := json.Marshal(wire.ViewEventInput{ViewID: "hello", Event: "open"})
	out, err := c.Call(context.Background(), "view_event", in, 0)
	require.NoError(t, err)
	var s wire.Surface
	require.NoError(t, json.Unmarshal(out, &s))
	require.Len(t, s.Nodes, 1)
	assert.Equal(t, "text", s.Nodes[0].Type)
	assert.False(t, s.Done)

	in, _ = json.Marshal(wire.ViewEventInput{ViewID: "hello", Event: "submit"})
	out, err = c.Call(context.Background(), "view_event", in, 0)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(out, &s))
	assert.True(t, s.Done)
}

func TestCall_TimeoutTearsTheInstanceDown(t *testing.T) {
	c := fixture(t)
	started := time.Now()
	_, err := runAction(t, c, "slow", 1500*time.Millisecond)
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeTimeout), err)
	assert.Less(t, time.Since(started), 10*time.Second, "the budget must be enforced, not the guest's own loop")
}

func TestCall_MemoryCeilingIsAnOOMNotAHostCrash(t *testing.T) {
	c := fixture(t)
	_, err := runAction(t, c, "hungry", 20*time.Second)
	require.Error(t, err)
	assert.True(t, IsCode(err, CodePluginOOM) || IsCode(err, CodePluginTrap), err)
	// And the runtime is still usable afterwards.
	_, err = runAction(t, c, "upper", 10*time.Second)
	require.NoError(t, err)
}

func TestCall_GuestErrorIsTheGuestsOwnWords(t *testing.T) {
	c := fixture(t)
	_, err := runAction(t, c, "boom", 5*time.Second)
	require.Error(t, err)
	assert.True(t, IsCode(err, CodePluginError), err)
	assert.Contains(t, err.Error(), "boom: the plugin said no")
}

func TestCall_TrapIsClassifiedAndNotAStack(t *testing.T) {
	c := fixture(t)
	_, err := runAction(t, c, "crash", 5*time.Second)
	require.Error(t, err)
	ce, ok := err.(*CallError)
	require.True(t, ok)
	assert.Contains(t, []string{CodePluginTrap, CodePluginError}, ce.Code)
	assert.False(t, strings.Contains(ce.Message, "wazero"), "no runtime internals in the user-facing message: %q", ce.Message)
}

func TestCall_UnknownExport(t *testing.T) {
	c := fixture(t)
	_, err := c.Call(context.Background(), "no_such_export", nil, 0)
	require.Error(t, err)
	assert.True(t, IsCode(err, CodePluginError), err)
}

func TestCompile_RefusesASHA256Mismatch(t *testing.T) {
	if _, err := os.Stat(fixtureWasm); err != nil {
		t.Skip("fixture not built")
	}
	raw, _ := os.ReadFile("testdata/echo/manifest.json")
	m, err := ParseManifest(raw)
	require.NoError(t, err)
	rt, err := NewRuntime("")
	require.NoError(t, err)
	defer rt.Close(context.Background())
	_, err = rt.Compile(context.Background(), fixtureWasm, strings.Repeat("0", 64), m, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256 mismatch")
}
