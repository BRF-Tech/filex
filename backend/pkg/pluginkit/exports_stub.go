//go:build !(wasip1 && wasm)

package pluginkit

import "log"

// Log on a non-wasm build (tests, `go vet` on the host) goes to the standard
// logger, so a plugin's unit tests can run natively.
func Log(level string, msg string) { log.Printf("[%s] %s", level, msg) }
