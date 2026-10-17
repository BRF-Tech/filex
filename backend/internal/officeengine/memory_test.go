package officeengine

import (
	"context"
	"os"
	"runtime/debug"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// Instance after instance, the engine process holds the memory x2t used, not
// the cap. With the reservation made as a Go allocation of the cap's size
// only the first instance's was untouched: every later one reused the heap
// the last one left, and Go zeroes reused memory before handing it out - the
// whole cap became resident (2026-10-16: 320 MB used, 2.4 GB resident, the
// second thumbnail's process ended at twice the cap).
func TestReservedMemory_ResidentFollowsWhatIsUsed(t *testing.T) {
	rss := func() int64 {
		n, ok := processRSS(os.Getpid())
		if !ok {
			t.Skip("this system's process memory is not read here")
		}
		return n
	}
	const (
		max   = 1 << 30
		used  = 64 << 20
		slack = 48 << 20
	)
	debug.FreeOSMemory()
	base := rss()
	for i := 0; i < 4; i++ {
		m := reserveAllocator{}.Allocate(0, max)
		buf := m.Reallocate(used)
		require.Len(t, buf, used)
		fresh := true
		for j := 0; j < len(buf); j += 4096 {
			fresh = fresh && buf[j] == 0
			buf[j] = byte(i + 1)
		}
		assert.True(t, fresh, "instance %d: a new memory reads as zeros", i)
		held := rss() - base
		m.Free()
		debug.FreeOSMemory() // as the engine does after every job
		after := rss() - base
		t.Logf("MEASURE instance %d: %d MiB used, %d MiB resident while held, %d MiB after", i, used>>20, held>>20, after>>20)
		assert.Less(t, held, int64(used+slack), "instance %d: resident memory follows what is used, not the %d MiB cap", i, max>>20)
		assert.Less(t, after, held-used/2, "instance %d: freeing gives the memory back", i)
	}
}

// The memory grows in place up to the cap, keeps what was written, and
// says no (nil: the module's memory.grow fails) past it.
func TestReservedMemory_GrowsInPlaceUpToTheCap(t *testing.T) {
	const page = 65536
	for name, m := range map[string]experimental.LinearMemory{
		"reserved": reserveAllocator{}.Allocate(page, 16*page),
		"heap":     &heapMemory{max: 16 * page},
	} {
		t.Run(name, func(t *testing.T) {
			defer m.Free()
			a := m.Reallocate(page)
			require.Len(t, a, page)
			a[0], a[page-1] = 1, 2
			b := m.Reallocate(4 * page)
			require.Len(t, b, 4*page)
			assert.Equal(t, []byte{1, 2}, []byte{b[0], b[page-1]}, "what was written stays")
			assert.Zero(t, b[page], "grown memory reads as zeros")
			c := m.Reallocate(16 * page)
			require.Len(t, c, 16*page)
			if name == "reserved" {
				assert.Equal(t, unsafe.SliceData(a), unsafe.SliceData(c), "a reservation never moves")
			}
			c[16*page-1] = 3
			assert.Nil(t, m.Reallocate(17*page), "past the cap")
			assert.Len(t, m.Reallocate(16*page), 16*page, "a refusal leaves the memory as it was")
		})
	}
	m := reserveAllocator{}.Allocate(page, page)
	m.Free()
	assert.Nil(t, m.Reallocate(page), "a freed memory does not grow")
	m.Free()
}

// growWasm is a module whose memory declares x2t's maximum (65536 pages, 4
// GiB) and exports grow (memory.grow) and poke (i32.store8).
var growWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// types: (i32) -> i32, (i32, i32) -> ()
	0x01, 0x0b, 0x02, 0x60, 0x01, 0x7f, 0x01, 0x7f, 0x60, 0x02, 0x7f, 0x7f, 0x00,
	// functions
	0x03, 0x03, 0x02, 0x00, 0x01,
	// memory: min 1, max 65536
	0x05, 0x06, 0x01, 0x01, 0x01, 0x80, 0x80, 0x04,
	// exports: memory, grow, poke
	0x07, 0x18, 0x03,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x04, 'g', 'r', 'o', 'w', 0x00, 0x00,
	0x04, 'p', 'o', 'k', 'e', 0x00, 0x01,
	// code
	0x0a, 0x12, 0x02,
	0x06, 0x00, 0x20, 0x00, 0x40, 0x00, 0x0b,
	0x09, 0x00, 0x20, 0x00, 0x20, 0x01, 0x3a, 0x00, 0x00, 0x0b,
}

// The cap holds as it did: the module's declared maximum is clamped to it
// (WithMemoryLimitPages), the allocator is handed that, and memory.grow past
// it fails inside the module.
func TestReservedMemory_TheCapHoldsInAModule(t *testing.T) {
	ctx := context.Background()
	const limit = 16
	rt := wazero.NewRuntimeWithConfig(ctx, runtimeConfig(nil, limit))
	defer rt.Close(ctx)
	mod, err := rt.CompileModule(ctx, growWasm)
	require.NoError(t, err)
	var gotMax uint64
	alloc := experimental.MemoryAllocatorFunc(func(capacity, max uint64) experimental.LinearMemory {
		gotMax = max
		return reserveAllocator{}.Allocate(capacity, max)
	})
	inst, err := rt.InstantiateModule(experimental.WithMemoryAllocator(ctx, alloc), mod, wazero.NewModuleConfig().WithName(""))
	require.NoError(t, err)
	defer inst.Close(ctx)
	assert.Equal(t, uint64(limit*65536), gotMax, "the allocator is handed the cap, not the module's 4 GiB")

	grow := func(pages uint32) int32 {
		res, err := inst.ExportedFunction("grow").Call(ctx, api.EncodeU32(pages))
		require.NoError(t, err)
		return int32(res[0])
	}
	poke := func(at uint32, v byte) {
		_, err := inst.ExportedFunction("poke").Call(ctx, api.EncodeU32(at), api.EncodeU32(uint32(v)))
		require.NoError(t, err)
	}
	poke(10, 7)
	assert.Equal(t, int32(1), grow(limit-1), "up to the cap")
	poke(limit*65536-1, 9)
	assert.Equal(t, int32(-1), grow(1), "past the cap")
	b, ok := inst.Memory().Read(0, limit*65536)
	require.True(t, ok)
	assert.Equal(t, byte(7), b[10])
	assert.Equal(t, byte(9), b[limit*65536-1])
	assert.Zero(t, b[65536], "grown memory reads as zeros")
}
