//go:build wasip1 && wasm

package pluginkit

import (
	pdk "github.com/extism/go-pdk"
)

// The raw imports. Each takes an Extism memory offset and returns one; the
// wrappers in host.go do the JSON and the framing.

//go:wasmimport extism:host/user file_open
func hostFileOpenRaw(ptr uint64) uint64

//go:wasmimport extism:host/user file_read
func hostFileReadRaw(ptr uint64) uint64

//go:wasmimport extism:host/user file_create
func hostFileCreateRaw(ptr uint64) uint64

//go:wasmimport extism:host/user file_write
func hostFileWriteRaw(ptr uint64) uint64

//go:wasmimport extism:host/user file_close
func hostFileCloseRaw(ptr uint64) uint64

//go:wasmimport extism:host/user job_progress
func hostJobProgressRaw(ptr uint64) uint64

//go:wasmimport extism:host/user settings_get
func hostSettingsGetRaw(ptr uint64) uint64

//go:wasmimport extism:host/user state_get
func hostStateGetRaw(ptr uint64) uint64

//go:wasmimport extism:host/user state_set
func hostStateSetRaw(ptr uint64) uint64

//go:wasmimport extism:host/user state_list
func hostStateListRaw(ptr uint64) uint64

//go:wasmimport extism:host/user file_lock
func hostFileLockRaw(ptr uint64) uint64

//go:wasmimport extism:host/user file_unlock
func hostFileUnlockRaw(ptr uint64) uint64

//go:wasmimport extism:host/user engine_available
func hostEngineAvailableRaw(ptr uint64) uint64

//go:wasmimport extism:host/user engine_run
func hostEngineRunRaw(ptr uint64) uint64

//go:wasmimport extism:host/user users_lookup
func hostUsersLookupRaw(ptr uint64) uint64

//go:wasmimport extism:host/user notify_send
func hostNotifySendRaw(ptr uint64) uint64

//go:wasmimport extism:host/user mail_send
func hostMailSendRaw(ptr uint64) uint64

//go:wasmimport extism:host/user http_request
func hostHTTPRequestRaw(ptr uint64) uint64

//go:wasmimport extism:host/user asset_fetch
func hostAssetFetchRaw(ptr uint64) uint64

//go:wasmimport extism:host/user share_create
func hostShareCreateRaw(ptr uint64) uint64

//go:wasmimport extism:host/user share_revoke
func hostShareRevokeRaw(ptr uint64) uint64

//go:wasmimport extism:host/user share_state
func hostShareStateRaw(ptr uint64) uint64

//go:wasmimport extism:host/user share_pin
func hostSharePINRaw(ptr uint64) uint64

//go:wasmimport extism:host/user host_sign_info
func hostSignInfoRaw(ptr uint64) uint64

//go:wasmimport extism:host/user cert_issue
func hostCertIssueRaw(ptr uint64) uint64

//go:wasmimport extism:host/user host_sign
func hostSignRaw(ptr uint64) uint64

//go:wasmimport extism:host/user key_destroy
func hostKeyDestroyRaw(ptr uint64) uint64

// call moves bytes in, runs the import, moves bytes out and frees both.
func call(fn func(uint64) uint64, in []byte) []byte {
	mem := pdk.AllocateBytes(in)
	defer mem.Free()
	off := fn(mem.Offset())
	if off == 0 {
		return []byte{}
	}
	out := pdk.FindMemory(off)
	defer out.Free()
	return out.ReadBytes()
}

func hostFileOpen(b []byte) []byte        { return call(hostFileOpenRaw, b) }
func hostFileRead(b []byte) []byte        { return call(hostFileReadRaw, b) }
func hostFileCreate(b []byte) []byte      { return call(hostFileCreateRaw, b) }
func hostFileWrite(b []byte) []byte       { return call(hostFileWriteRaw, b) }
func hostFileClose(b []byte) []byte       { return call(hostFileCloseRaw, b) }
func hostJobProgress(b []byte) []byte     { return call(hostJobProgressRaw, b) }
func hostSettingsGet(b []byte) []byte     { return call(hostSettingsGetRaw, b) }
func hostStateGet(b []byte) []byte        { return call(hostStateGetRaw, b) }
func hostStateSet(b []byte) []byte        { return call(hostStateSetRaw, b) }
func hostStateList(b []byte) []byte       { return call(hostStateListRaw, b) }
func hostFileLock(b []byte) []byte        { return call(hostFileLockRaw, b) }
func hostFileUnlock(b []byte) []byte      { return call(hostFileUnlockRaw, b) }
func hostEngineAvailable(b []byte) []byte { return call(hostEngineAvailableRaw, b) }
func hostEngineRun(b []byte) []byte       { return call(hostEngineRunRaw, b) }
func hostUsersLookup(b []byte) []byte     { return call(hostUsersLookupRaw, b) }
func hostNotifySend(b []byte) []byte      { return call(hostNotifySendRaw, b) }
func hostMailSend(b []byte) []byte        { return call(hostMailSendRaw, b) }
func hostHTTPRequest(b []byte) []byte     { return call(hostHTTPRequestRaw, b) }
func hostAssetFetch(b []byte) []byte      { return call(hostAssetFetchRaw, b) }
func hostShareCreate(b []byte) []byte     { return call(hostShareCreateRaw, b) }
func hostShareRevoke(b []byte) []byte     { return call(hostShareRevokeRaw, b) }
func hostShareState(b []byte) []byte      { return call(hostShareStateRaw, b) }
func hostSharePIN(b []byte) []byte        { return call(hostSharePINRaw, b) }
func hostSignInfo(b []byte) []byte        { return call(hostSignInfoRaw, b) }
func hostCertIssue(b []byte) []byte       { return call(hostCertIssueRaw, b) }
func hostSign(b []byte) []byte            { return call(hostSignRaw, b) }
func hostKeyDestroy(b []byte) []byte      { return call(hostKeyDestroyRaw, b) }
