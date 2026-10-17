package officeengine

import (
	"math"
	"os"

	"github.com/tetratelabs/wazero/experimental"
)

// ── x2t's memory, reserved up to the cap ────────────────────────────────
//
// wazero grows a module's memory by copying it into a larger buffer unless
// its capacity was taken from the maximum up front. A 200,000-row csv took
// x2t to 2.6 GiB of memory and the process to 6.2 GB with the copies
// (measured, docs/SERVER.md of the app). wazero's own switch for it
// (WithMemoryCapacityFromMax) takes the module's DECLARED maximum - x2t's is
// 4 GiB - and then refuses to compile it under a lower cap ("max 65536 pages
// over limit", wazero 1.9 binary/decoder.go newMemorySizer). So the
// reservation is made here, by an allocator that is handed the maximum the
// cap leaves (WithMemoryLimitPages clamps it): one range of that size, grown
// in place, never copied.
//
// The range is the system's, not Go's. A Go allocation of the cap's size
// (make([]byte, 0, max)) was untouched only the first time: every later
// instance in the process reused the heap the last one left, and Go zeroes
// reused memory before handing it out, so the whole cap became resident -
// 320 MB used, 2.4 GB resident, and the process ended at twice the cap on
// the second thumbnail (measured 2026-10-16, TestEngine_TheRealModules).
// Instead the cap is reserved as address space only - mmap PROT_NONE on
// Linux and macOS (memory_unix.go), VirtualAlloc MEM_RESERVE on Windows
// (memory_windows.go) - and each grow commits the part it adds (mprotect
// read-write, MEM_COMMIT). The system backs a page with memory when x2t
// first writes it, counts against its commit limit only what is committed
// (Windows, and Linux with strict overcommit), and takes all of it back
// when the instance closes (munmap, MEM_RELEASE). A new reservation is new
// memory, zero without anyone writing it.
//
// The layout's workers keep wazero's own memory (a Go slice): a worker that
// does not end when asked is closed while its _start may still run on
// another goroutine, and an unmapped memory under running code would end the
// process; a Go slice stays valid as long as anything refers to it. An x2t
// instance runs on the job's goroutine and is closed only after it returns
// (or from its own proc_exit, after which no wasm runs).

// reserveAllocator hands out x2t's memories: a reservation where the system
// makes one, else heapMemory.
type reserveAllocator struct{}

func (reserveAllocator) Allocate(capacity, max uint64) experimental.LinearMemory {
	if max < capacity {
		max = capacity
	}
	if max <= math.MaxInt {
		if m, err := reserve(int(max)); err == nil {
			return m
		}
	}
	// No range of the cap's size (a 32-bit system, a limit on address space):
	// a memory that grows by copying, as wazero's own.
	return &heapMemory{max: max}
}

// reservedMemory is a linear memory in a range reserved up to the cap: it
// never moves, and only its committed part is backed.
type reservedMemory struct {
	all       []byte // the whole range; nil once freed
	committed int    // how much of it is readable and writable
}

// Reallocate grows the memory in place; nil (the module's memory.grow fails)
// once it would pass the reservation, which is the cap, or when the system
// does not commit the part it adds.
func (m *reservedMemory) Reallocate(size uint64) []byte {
	if m.all == nil || size > uint64(len(m.all)) {
		return nil
	}
	n := int(size)
	if n > m.committed {
		upto := min(roundUp(n, os.Getpagesize()), len(m.all))
		if err := commit(m.all[m.committed:upto]); err != nil {
			return nil
		}
		m.committed = upto
	}
	return m.all[:n:n]
}

// Free gives the whole range back to the system.
func (m *reservedMemory) Free() {
	if m.all == nil {
		return
	}
	_ = release(m.all)
	m.all, m.committed = nil, 0
}

// heapMemory is a linear memory that is a Go slice grown by copying (where
// no range could be reserved): it holds what is used and a copy while it
// grows.
type heapMemory struct {
	buf []byte
	max uint64
}

func (m *heapMemory) Reallocate(size uint64) []byte {
	if size > m.max || size > math.MaxInt {
		return nil
	}
	n := int(size)
	if n > cap(m.buf) {
		m.buf = append(m.buf[:cap(m.buf)], make([]byte, n-cap(m.buf))...)
	}
	return m.buf[:n:n]
}

func (m *heapMemory) Free() { m.buf = nil }

func roundUp(n, to int) int {
	return (n + to - 1) / to * to
}

// pagesOf is a cap in MiB as 64 KiB pages, at most the 4 GiB a 32-bit
// memory addresses.
func pagesOf(mb int) uint32 {
	p := uint64(mb) * 16
	if p > 65536 {
		p = 65536
	}
	return uint32(p)
}
