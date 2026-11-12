// Package daemon runs drives for one user, outside any session. Each drive is
// its own `f10 drive --foreground` child in the client's directory and
// environment, so leases and crash recovery stay the drive's own.
package daemon
