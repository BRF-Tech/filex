//go:build wasip1 && wasm

package pluginkit

import (
	pdk "github.com/extism/go-pdk"
)

// The six exports filex calls. Each reads the call's JSON from Extism's
// input buffer, dispatches, and writes JSON back; an error becomes the
// guest error message filex shows the user (code plugin_error).

//go:wasmexport describe
func exportDescribe() int32 { return finish(dispatchDescribe(pdk.Input())) }

//go:wasmexport action_run
func exportActionRun() int32 { return finish(dispatchAction(pdk.Input())) }

//go:wasmexport view_event
func exportViewEvent() int32 { return finish(dispatchView(pdk.Input(), false)) }

//go:wasmexport page_event
func exportPageEvent() int32 { return finish(dispatchView(pdk.Input(), true)) }

//go:wasmexport tick
func exportTick() int32 { return finish(dispatchTick(pdk.Input())) }

// Reserved: nothing calls this yet, and the `events:<name>` permission that
// would gate it is refused at install until something does.
//
//go:wasmexport on_event
func exportOnEvent() int32 { return 0 }

func finish(out []byte, err error) int32 {
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(out)
	return 0
}

// Log writes to the plugin's log in the filex admin panel.
func Log(level string, msg string) {
	switch level {
	case "debug":
		pdk.Log(pdk.LogDebug, msg)
	case "warn":
		pdk.Log(pdk.LogWarn, msg)
	case "error":
		pdk.Log(pdk.LogError, msg)
	default:
		pdk.Log(pdk.LogInfo, msg)
	}
}
