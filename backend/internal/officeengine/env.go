package officeengine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/emscripten"
	"github.com/tetratelabs/wazero/sys"
)

// ── What x2t still imports from emscripten's "env" ──────────────────────
//
// x2t-server.wasm is a WASI command (its files, clock and exit go through
// WASI, which wazero provides). emscripten still leaves a few imports in
// "env", answered here the way emscripten's JavaScript answers them in a
// browser:
//
//   - invoke_<sig>, _emscripten_throw_longjmp and setThrew: setjmp/longjmp
//     (libpng, libjpeg, FreeType). The JavaScript calls the function table
//     entry inside a try; a longjmp is a throw it catches, then it restores
//     the stack and calls setThrew(1, 0). Here the longjmp is a Go panic the
//     invoke recovers from. The table call itself is the module's own
//     fx_dyncall_<sig> export: with -sEMULATE_FUNCTION_POINTER_CASTS a call
//     made from the host by the import's signature traps ("indirect call type
//     mismatch"), one made inside the module does not; fx_dyncall_* keeps
//     the stack pointer it was called with, and fx_unwind puts it back after
//     a longjmp. A module without them gets wazero's own emscripten
//     invokes.
//   - emscripten_notify_memory_growth: nothing to do.
//   - every other name: a function the link left undefined. Reaching one
//     ends the run with "missing function: <name>", as the browser's
//     "Aborted(missing function: ...)" does.

// missingFunction is a run that reached a function the build does not have.
type missingFunction struct{ name string }

func (m *missingFunction) Error() string { return "missing function: " + m.name }

// errLongjmp is what _emscripten_throw_longjmp panics with.
var errLongjmp = errors.New("office engine: longjmp")

// instantiateEnv puts the "env" module x2t imports from into rt. It returns
// the names answered with a stand-in that stops the run.
func instantiateEnv(ctx context.Context, rt wazero.Runtime, guest wazero.CompiledModule) ([]string, error) {
	b := rt.NewHostModuleBuilder("env")
	exports := guest.ExportedFunctions()
	own := false
	for name := range exports {
		if strings.HasPrefix(name, "fx_dyncall_") {
			own = true
			break
		}
	}
	if _, ok := exports["fx_unwind"]; own && !ok {
		return nil, errors.New("office engine: x2t exports fx_dyncall_* but no fx_unwind (not the server build)")
	}
	if !own {
		exp, err := emscripten.NewFunctionExporterForModule(guest)
		if err != nil {
			return nil, err
		}
		exp.ExportFunctions(b)
	}
	var missing []string
	for _, fn := range guest.ImportedFunctions() {
		module, name, ok := fn.Import()
		if !ok || module != "env" {
			continue
		}
		var impl api.GoModuleFunc
		switch {
		case strings.HasPrefix(name, "invoke_") || name == "_emscripten_throw_longjmp" || name == "emscripten_notify_memory_growth":
			if !own {
				continue
			}
			switch name {
			case "_emscripten_throw_longjmp":
				impl = func(context.Context, api.Module, []uint64) { panic(errLongjmp) }
			case "emscripten_notify_memory_growth":
				impl = func(context.Context, api.Module, []uint64) {}
			default:
				dyn := "fx_dyncall_" + strings.TrimPrefix(name, "invoke_")
				if _, ok := exports[dyn]; !ok {
					return nil, fmt.Errorf("office engine: x2t imports %s and exports no %s", name, dyn)
				}
				impl = invoke(dyn, len(fn.ResultTypes()))
			}
		default:
			missing = append(missing, name)
			n := name
			impl = func(context.Context, api.Module, []uint64) { panic(&missingFunction{name: n}) }
		}
		b.NewFunctionBuilder().WithGoModuleFunction(impl, fn.ParamTypes(), fn.ResultTypes()).Export(name)
	}
	if _, err := b.Instantiate(ctx); err != nil {
		return nil, err
	}
	sort.Strings(missing)
	return missing, nil
}

// invoke is invoke_<sig>:
//
//	function invoke_vii(index, a1, a2) {
//	  var sp = stackSave();
//	  try { getWasmTableEntry(index)(a1, a2); }
//	  catch (e) { stackRestore(sp); if (e !== e+0) throw e; _setThrew(1, 0); }
//	}
//
// with the module's fx_dyncall_<sig>(index, a1, a2) for the table call (it
// keeps the stack pointer itself, so no stackSave call is made from here: x2t
// goes through invoke_* millions of times in a document with pictures).
func invoke(dynCall string, nResults int) api.GoModuleFunc {
	return func(ctx context.Context, mod api.Module, stack []uint64) {
		err := cachedExport(ctx, mod, dynCall).CallWithStack(ctx, stack)
		if err == nil {
			return
		}
		var exit *sys.ExitError
		if errors.As(err, &exit) || !errors.Is(err, errLongjmp) {
			panic(err)
		}
		var st [2]uint64
		mustCall(ctx, mod, "fx_unwind", st[:])
		st[0], st[1] = 1, 0
		mustCall(ctx, mod, "setThrew", st[:])
		for i := 0; i < nResults; i++ {
			stack[i] = 0
		}
	}
}

func mustCall(ctx context.Context, mod api.Module, name string, stack []uint64) {
	if err := cachedExport(ctx, mod, name).CallWithStack(ctx, stack); err != nil {
		panic(err)
	}
}

// The functions invoke_* calls, looked up once per run: wazero makes a new
// callable (with a stack of its own) at every ExportedFunction, and a lookup
// per call cost a third of a picture-heavy conversion (measured by the app,
// 62.7 s for sample.docx to odt).
type callCache struct{ fns map[string]api.Function }

type callCacheKey struct{}

// withCallCache gives one run a cache of the functions invoke_* calls.
func withCallCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, callCacheKey{}, &callCache{fns: map[string]api.Function{}})
}

func cachedExport(ctx context.Context, mod api.Module, name string) api.Function {
	c, _ := ctx.Value(callCacheKey{}).(*callCache)
	if c != nil {
		if f, ok := c.fns[name]; ok {
			return f
		}
	}
	f := mod.ExportedFunction(name)
	if f == nil {
		panic(fmt.Errorf("office engine: x2t does not export %s", name))
	}
	if c != nil {
		c.fns[name] = f
	}
	return f
}
