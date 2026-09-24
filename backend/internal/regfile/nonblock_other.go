//go:build !unix

package regfile

// nonBlock is nothing here: Windows keeps its pipes in their own namespace
// (\\.\pipe\), never inside a folder, so an open under a storage root cannot
// meet one. A device name inside a folder is the device, not a file — measured
// on Windows 11, `<dir>\NUL` stats as a character device — and that is refused
// by the look OpenFile takes before it opens anything.
const nonBlock = 0
