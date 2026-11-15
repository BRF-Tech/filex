package sync

import (
	gosync "sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// memPeak samples the process's memory while a full scan walks a storage and
// keeps the highest figure (task #353). RunOnce records it on the run, and
// the storage form compares it with what the process was given
// (internal/syncadvice) to warn before a scan runs filex out of memory.
//
// It reads sysmem.InUse - the memory the Go runtime holds and has not handed
// back - through runtime/metrics, which does not stop the world, once every
// memPeakEvery and once more at the end. The figure is the WHOLE process's:
// another storage's scan, an upload or the search index running at the same
// time are in it too, which is also what an out-of-memory kill would count.
type memPeak struct {
	mu   gosync.Mutex
	peak uint64
	stop chan struct{}
	done chan struct{}
	once gosync.Once
}

// memPeakEvery is how often memPeak samples; memInUse is what it reads. Package
// variables so a test can run it at test speed with a figure it chose.
var (
	memPeakEvery = time.Second
	memInUse     = sysmem.InUse
)

// startMemPeak takes a first sample and starts sampling.
func startMemPeak() *memPeak {
	m := &memPeak{stop: make(chan struct{}), done: make(chan struct{})}
	m.sample()
	every := memPeakEvery
	go func() {
		defer close(m.done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.sample()
			}
		}
	}()
	return m
}

func (m *memPeak) sample() {
	v := memInUse()
	m.mu.Lock()
	if v > m.peak {
		m.peak = v
	}
	m.mu.Unlock()
}

// end stops the sampling, takes a last sample and returns the peak. Safe to
// call more than once; every call returns the same figure.
func (m *memPeak) end() uint64 {
	m.once.Do(func() {
		close(m.stop)
		<-m.done
		m.sample()
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peak
}
