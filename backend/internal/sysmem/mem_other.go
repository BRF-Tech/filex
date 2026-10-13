//go:build !linux && !windows && !darwin

package sysmem

func cgroupLimit() (uint64, string) { return 0, "" }

// hostTotal has no implementation here: no budget, no share.
func hostTotal() uint64 { return 0 }
