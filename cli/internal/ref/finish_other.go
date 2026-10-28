//go:build !unix

package ref

// processAlive cannot probe here, so a record counts as live.
func processAlive(int) bool { return true }
