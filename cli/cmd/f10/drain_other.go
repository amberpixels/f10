//go:build !unix

package main

import "os"

// watchDrain catches nothing here: there is no daemon to drain a drive.
func watchDrain(bool) (<-chan os.Signal, func()) { return nil, func() {} }
