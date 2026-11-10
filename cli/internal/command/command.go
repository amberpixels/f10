// Package command holds what every f10 command package shares; each command
// lives in a package of its own under it.
package command

// Plumbing marks a verb the steps and skills call: Hidden from
// `f10 --help`, listed under this heading by `f10 help --all`. A Hidden
// command with no category is internal (hooks, the daemon) and never listed.
const Plumbing = "plumbing"
