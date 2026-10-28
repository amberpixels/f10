//go:build !unix

package main

import "os"

// drainSignal never arrives here: there is no daemon to send it.
var drainSignal = os.Interrupt
