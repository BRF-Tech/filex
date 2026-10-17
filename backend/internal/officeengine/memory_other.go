//go:build !unix && !windows

package officeengine

import "errors"

// reserve: no reservation on this system; x2t's memory is a heapMemory.
func reserve(int) (*reservedMemory, error) {
	return nil, errors.New("office engine: no address space reservation on this system")
}

func commit([]byte) error { return errors.New("office engine: no reservation") }

func release([]byte) error { return nil }
