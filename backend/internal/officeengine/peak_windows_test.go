package officeengine

// processPeakRSS is the most a process has held resident (its peak working
// set).
func processPeakRSS(pid int) (int64, bool) {
	c, ok := processMemoryInfo(pid)
	return int64(c.peakWorkingSetSize), ok
}
