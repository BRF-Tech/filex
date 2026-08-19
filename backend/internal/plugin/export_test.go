package plugin

// ExecName exposes execName to the package's external tests.
func ExecName(filename, goos string) string { return execName(filename, goos) }
