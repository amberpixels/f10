// Package drives keeps `f10 drive` durable: a record per drive under the
// storage root, and a lease per task, so two drives, or a drive and
// `f10 finish`, never act on one task at once.
package drives
