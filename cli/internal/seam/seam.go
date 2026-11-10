// Package seam swaps a package variable for a test, such as shell.Capture or
// gitx.Exec, and puts it back when the test ends.
package seam

import "testing"

// Swap sets *v to fake for the rest of the test. It also sets an
// environment variable, so Go refuses t.Parallel in that test: a swapped
// package variable is shared by every test running at the time.
func Swap[T any](t testing.TB, v *T, fake T) {
	t.Helper()
	t.Setenv("F10_TEST_SEAM", "swapped")

	prev := *v
	*v = fake

	t.Cleanup(func() { *v = prev })
}
