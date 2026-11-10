//go:build !unix

package drive

import "os"

// WatchDrain catches nothing here: there is no daemon to drain a drive.
func WatchDrain(bool) (<-chan os.Signal, func()) { return nil, func() {} }
