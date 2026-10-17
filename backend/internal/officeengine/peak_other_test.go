//go:build !linux && !windows

package officeengine

func processPeakRSS(int) (int64, bool) { return 0, false }
