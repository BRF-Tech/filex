package officeengine

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// reserve reserves n bytes of address space (MEM_RESERVE): no memory, and
// nothing counted against the commit limit, until a part is committed.
func reserve(n int) (*reservedMemory, error) {
	addr, err := windows.VirtualAlloc(0, uintptr(n), windows.MEM_RESERVE, windows.PAGE_NOACCESS)
	if err != nil {
		return nil, err
	}
	// The address is the system's, never Go's: read it as a pointer without
	// a uintptr conversion vet would question.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	return &reservedMemory{all: unsafe.Slice((*byte)(p), n)}, nil
}

// commit commits part of a reservation (MEM_COMMIT, read-write): counted
// against the commit limit from here, backed when a page is first written.
func commit(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	_, err := windows.VirtualAlloc(uintptr(unsafe.Pointer(unsafe.SliceData(b))), uintptr(len(b)), windows.MEM_COMMIT, windows.PAGE_READWRITE)
	return err
}

// release gives a whole reservation back (MEM_RELEASE).
func release(all []byte) error {
	return windows.VirtualFree(uintptr(unsafe.Pointer(unsafe.SliceData(all))), 0, windows.MEM_RELEASE)
}
