package main

import "testing"

func TestGreeting(t *testing.T) {
	if got := greeting("Ada"); got != "Hello, Ada!" {
		t.Errorf("greeting = %q", got)
	}
}
