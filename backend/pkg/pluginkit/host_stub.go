//go:build !(wasip1 && wasm)

package pluginkit

// Off-wasm every host call answers nil, which the wrappers turn into
// ErrNotWasm. A plugin's pure logic can still be unit-tested on the host;
// anything that touches files must be tested through filex's own
// wasmplugin test harness against the built module.

func hostFileOpen([]byte) []byte        { return nil }
func hostFileRead([]byte) []byte        { return nil }
func hostFileCreate([]byte) []byte      { return nil }
func hostFileWrite([]byte) []byte       { return nil }
func hostFileClose([]byte) []byte       { return nil }
func hostJobProgress([]byte) []byte     { return nil }
func hostSettingsGet([]byte) []byte     { return nil }
func hostStateGet([]byte) []byte        { return nil }
func hostStateSet([]byte) []byte        { return nil }
func hostStateList([]byte) []byte       { return nil }
func hostFileLock([]byte) []byte        { return nil }
func hostFileUnlock([]byte) []byte      { return nil }
func hostEngineAvailable([]byte) []byte { return nil }
func hostEngineRun([]byte) []byte       { return nil }
func hostUsersLookup([]byte) []byte     { return nil }
func hostNotifySend([]byte) []byte      { return nil }
func hostMailSend([]byte) []byte        { return nil }
func hostHTTPRequest([]byte) []byte     { return nil }
func hostAssetFetch([]byte) []byte      { return nil }
func hostShareCreate([]byte) []byte     { return nil }
func hostShareRevoke([]byte) []byte     { return nil }
func hostShareState([]byte) []byte      { return nil }
func hostSharePIN([]byte) []byte        { return nil }
func hostSignInfo([]byte) []byte        { return nil }
func hostCertIssue([]byte) []byte       { return nil }
func hostSign([]byte) []byte            { return nil }
func hostKeyDestroy([]byte) []byte      { return nil }
