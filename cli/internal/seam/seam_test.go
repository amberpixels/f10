package seam

import "testing"

var hook = "real"

func TestSwapRestores(t *testing.T) {
	t.Run("swapped", func(t *testing.T) {
		Swap(t, &hook, "fake")

		if hook != "fake" {
			t.Fatalf("hook = %q, want fake", hook)
		}
	})

	if hook != "real" {
		t.Fatalf("hook = %q after the test, want real", hook)
	}
}

func TestSwapRefusesParallel(t *testing.T) {
	t.Run("parallel", func(t *testing.T) {
		Swap(t, &hook, "fake")

		defer func() {
			if recover() == nil {
				t.Error("t.Parallel after Swap did not panic")
			}
		}()

		t.Parallel()
	})
}
